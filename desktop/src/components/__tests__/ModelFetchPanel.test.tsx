// @vitest-environment jsdom
import React, { useEffect } from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { ModelFetchPanel } from '../ModelFetchPanel';
import { useModelFetch } from '../../lib/useModelFetch';

vi.mock('i18next', () => ({
  default: { use: () => ({ init: () => {} }), t: (key: string) => key },
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) =>
      opts ? `${key}(${JSON.stringify(opts)})` : key,
  }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

const BASE = 'http://127.0.0.1:8899';
let calls: Array<{ url: string; init?: RequestInit }> = [];

function jsonRes(body: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response;
}

const liveModels = [
  { id: 'deepseek-v4-flash', name: 'DeepSeek V4 Flash', known: true, enabled: true, context_window: 128000 },
  { id: 'deepseek-v4', name: 'DeepSeek V4', known: true, enabled: true, context_window: 128000 },
];

function Harness() {
  const mf = useModelFetch(BASE);
  useEffect(() => { void mf.fetchModels('deepseek'); }, []);
  return <ModelFetchPanel provider="deepseek" color="#4F46E5" mf={mf} />;
}

beforeEach(() => {
  calls = [];
  globalThis.fetch = vi.fn(async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, init });
    if (url.includes('/api/providers')) {
      return jsonRes([{ name: 'deepseek', models: 2, enabled: 2, filtered: false }]);
    }
    if (url.includes('/api/models/fetch')) {
      return jsonRes({ provider: 'deepseek', count: 2, filtered: false, models: liveModels });
    }
    if (url.includes('/api/models/selection')) return jsonRes({ ok: true, enabled: 2 });
    return jsonRes({}, 404);
  }) as unknown as typeof fetch;
});

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('ModelFetchPanel', () => {
  it('lists the fetched models with every one ticked', async () => {
    render(<Harness />);

    const boxes = await waitFor(() => {
      const found = screen.getAllByRole('checkbox') as HTMLInputElement[];
      expect(found).toHaveLength(2);
      return found;
    });
    expect(boxes.every(b => b.checked)).toBe(true);
    expect(screen.getByText('DeepSeek V4 Flash')).toBeTruthy();
    // The raw vendor id stays visible so a mismatch with the display name is obvious.
    expect(screen.getByText('deepseek-v4')).toBeTruthy();
  });

  it('unticking a model and applying persists the reduced set', async () => {
    render(<Harness />);
    await waitFor(() => expect(screen.getAllByRole('checkbox')).toHaveLength(2));

    fireEvent.click(screen.getAllByRole('checkbox')[1]);          // untick DeepSeek V4
    expect((screen.getAllByRole('checkbox')[1] as HTMLInputElement).checked).toBe(false);

    fireEvent.click(screen.getByText('models.applySelection'));

    await waitFor(() => {
      const put = calls.find(c => c.url.includes('/api/models/selection'));
      expect(put?.init?.method).toBe('PUT');
      expect(JSON.parse(String(put!.init!.body)))
        .toEqual({
          provider: 'deepseek',
          models: ['deepseek-v4-flash'],
          // The ticked model's vendor-reported window travels with the ids, so
          // an auto-registered entry is not stored blank.
          meta: { 'deepseek-v4-flash': { context_window: 128000, max_output_tokens: 0 } },
        });
    });
  });

  it('"select none" blocks the apply button — an empty set must not be saved', async () => {
    render(<Harness />);
    await waitFor(() => expect(screen.getAllByRole('checkbox')).toHaveLength(2));

    fireEvent.click(screen.getByText('models.selectNone'));
    expect(screen.getAllByRole('checkbox').every(b => !(b as HTMLInputElement).checked)).toBe(true);

    const apply = screen.getByText('models.applySelection') as HTMLButtonElement;
    expect(apply.disabled).toBe(true);

    fireEvent.click(apply);
    expect(calls.some(c => c.url.includes('/api/models/selection'))).toBe(false);
  });
});
