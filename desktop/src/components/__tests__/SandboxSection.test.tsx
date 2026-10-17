// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { SandboxSection } from '../SandboxSection';

vi.mock('i18next', () => ({
  default: { use: () => ({ init: () => {} }), t: (key: string) => key },
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));
vi.mock('../../stores/appStore', () => ({
  useAppStore: (sel: (s: { backendUrl: string }) => unknown) => sel({ backendUrl: 'http://127.0.0.1:8899' }),
}));

let calls: Array<{ url: string; method: string; body?: string }> = [];
let nextStatus = 200;

function jsonRes(body: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response;
}

const enabledState = {
  runtime: {
    enabled: true, backend: 'appcontainer', available: true,
    mode: 'auto', network: 'deny', notices: ['created AppContainer profile iCode.Sandbox'],
  },
  config: { mode: 'auto', network: 'deny', writable_paths: [], grant_workspace: true },
  restart_required: false,
};

const disabledState = {
  runtime: { enabled: false, available: false, mode: 'auto', network: 'deny' },
  config: { mode: 'auto', network: 'deny', writable_paths: [], grant_workspace: true },
  restart_required: false,
};

beforeEach(() => {
  calls = [];
  nextStatus = 200;
  globalThis.fetch = vi.fn(async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method || 'GET';
    calls.push({ url, method, body: init?.body as string | undefined });
    if (method === 'PUT') {
      if (nextStatus !== 200) return jsonRes({ error: 'boom' }, nextStatus);
      const body = JSON.parse(String(init?.body || '{}'));
      return jsonRes({
        ...enabledState,
        config: { ...enabledState.config, mode: body.mode ?? 'auto' },
        restart_required: true,
      });
    }
    return jsonRes(url.includes('/api/sandbox') ? enabledState : {});
  }) as unknown as typeof fetch;
});

afterEach(() => cleanup());

describe('SandboxSection', () => {
  it('reports an enabled kernel-level sandbox with its backend name', async () => {
    render(<SandboxSection />);
    expect(await screen.findByText('tools.sandboxActive')).toBeTruthy();
    expect(screen.getByText(/appcontainer/)).toBeTruthy();
  });

  it('does not claim confinement when no backend is installed', async () => {
    globalThis.fetch = vi.fn(async () => jsonRes(disabledState)) as unknown as typeof fetch;
    render(<SandboxSection />);
    expect(await screen.findByText('tools.sandboxInactive')).toBeTruthy();
    // The badge must never read "enabled" while runtime.enabled is false.
    expect(screen.queryByText('tools.sandboxActive')).toBeNull();
    expect(screen.getByText('tools.sandboxInactiveHint')).toBeTruthy();
  });

  it('surfaces the backend notices so a degraded start is explainable', async () => {
    render(<SandboxSection />);
    expect(await screen.findByText(/created AppContainer profile/)).toBeTruthy();
  });

  it('saves a mode change and says a restart is required', async () => {
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxActive');

    fireEvent.click(screen.getByText('tools.sandboxMode_on'));

    await waitFor(() => expect(screen.getByText('tools.sandboxRestartHint')).toBeTruthy());
    const put = calls.find((c) => c.method === 'PUT');
    expect(put).toBeTruthy();
    expect(put!.url).toContain('/api/sandbox');
    expect(JSON.parse(put!.body!)).toEqual({ mode: 'on' });
  });

  it('reports a failed save instead of silently keeping the old mode', async () => {
    nextStatus = 500;
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxActive');

    fireEvent.click(screen.getByText('tools.sandboxMode_off'));

    await waitFor(() => expect(screen.getByText(/tools\.sandboxError/)).toBeTruthy());
    expect(screen.queryByText('tools.sandboxRestartHint')).toBeNull();
  });
});
