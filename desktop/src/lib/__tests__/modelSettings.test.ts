// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { buildModelPayload, saveModelSettings, type ModelSettingsValues } from '../modelSettings';
import type { Model } from '../../stores/appStore';

const BASE = 'http://127.0.0.1:8899';

const builtin: Model = {
  id: 'deepseek/deepseek-chat',
  model_id: 'deepseek-chat',
  name: 'DeepSeek Chat',
  provider: 'deepseek',
  plan: 'Coding Plan',
};
const handAdded: Model = {
  id: 'demo/demo-c',
  model_id: 'demo-c',
  name: 'Demo C',
  provider: 'demo',
  plan: 'default',
  custom: true,
};

const values: ModelSettingsValues = {
  apiKey: '',
  apiBase: '',
  temperature: 0,
  maxTokens: 2048,
  topP: 0.35,
  contextWindow: 64000,
};

let calls: Array<{ url: string; init?: RequestInit }> = [];
let respond: (url: string) => Response;

function jsonRes(body: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response;
}

beforeEach(() => {
  calls = [];
  respond = () => jsonRes({ ok: true });
  globalThis.fetch = vi.fn(async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, init });
    return respond(url);
  }) as unknown as typeof fetch;
});

afterEach(() => { vi.restoreAllMocks(); });

describe('buildModelPayload', () => {
  it('keeps the bare vendor id and preserves the custom flag', () => {
    const p = buildModelPayload(handAdded, values);
    expect(p.model_id).toBe('demo-c');
    expect(p.custom).toBe(true);
  });

  it('falls back to id when model_id is absent', () => {
    const p = buildModelPayload({ ...builtin, model_id: undefined }, values);
    expect(p.model_id).toBe('deepseek/deepseek-chat');
  });

  it('does not mark a built-in as custom — that would duplicate it in the list', () => {
    expect(buildModelPayload(builtin, values).custom).toBe(false);
  });

  it('maps the dialog fields onto the backend names and keeps an explicit 0', () => {
    const p = buildModelPayload(builtin, values);
    expect(p.max_output_tokens).toBe(2048);
    expect(p.context_window).toBe(64000);
    // 0 is a legitimate "deterministic" choice — it must not be dropped.
    expect(p.temperature).toBe(0);
    expect(p.top_p).toBe(0.35);
  });
});

describe('saveModelSettings', () => {
  it('writes parameters to /api/config/model, not to /api/config/key', async () => {
    await saveModelSettings(BASE, builtin, values);

    const urls = calls.map(c => c.url);
    expect(urls).toEqual([`${BASE}/api/config/model`]);
    expect(calls[0].init?.method).toBe('PUT');

    const body = JSON.parse(String(calls[0].init?.body));
    expect(body.temperature).toBe(0);
    // The values that used to be posted to /api/config/key — where they were
    // silently discarded.
    expect(body.top_p).toBe(0.35);
    expect(body.max_output_tokens).toBe(2048);
    expect(body.context_window).toBe(64000);
  });

  it('leaves stored credentials alone when nothing was typed', async () => {
    await saveModelSettings(BASE, builtin, values);
    expect(calls.some(c => c.url.includes('/api/config/key'))).toBe(false);
  });

  it('also writes credentials when the user typed a key', async () => {
    await saveModelSettings(BASE, builtin, { ...values, apiKey: 'sk-1' });

    const keyCall = calls.find(c => c.url.includes('/api/config/key'));
    expect(keyCall).toBeTruthy();
    const body = JSON.parse(String(keyCall!.init?.body));
    expect(body.api_key).toBe('sk-1');
    expect(body.provider).toBe('deepseek');
    // Credentials must not be smuggled into the model entry.
    expect(body.temperature).toBeUndefined();
  });

  it('surfaces the server message verbatim instead of a bare status', async () => {
    respond = (url) => url.includes('/api/config/model')
      ? jsonRes({ error: 'demo 已内置模型 demo-c，无需重复添加' }, 409)
      : jsonRes({ ok: true });

    const err = await saveModelSettings(BASE, handAdded, values);
    expect(err).toBe('demo 已内置模型 demo-c，无需重复添加');
  });

  it('reports a bare status only when the body carries no message', async () => {
    respond = () => jsonRes({}, 500);
    expect(await saveModelSettings(BASE, builtin, values)).toBe('HTTP 500');
  });

  it('returns empty string on success and reports a backend-less call', async () => {
    expect(await saveModelSettings(BASE, builtin, values)).toBe('');
    expect(await saveModelSettings('', builtin, values)).toBe('Backend not connected');
  });

  it('stops before the credential call if the model write failed', async () => {
    respond = (url) => url.includes('/api/config/model') ? jsonRes({ error: 'nope' }, 400) : jsonRes({ ok: true });

    const err = await saveModelSettings(BASE, builtin, { ...values, apiKey: 'sk-1' });
    expect(err).toBe('nope');
    expect(calls.some(c => c.url.includes('/api/config/key'))).toBe(false);
  });
});
