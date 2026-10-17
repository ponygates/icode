// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, within } from '@testing-library/react';
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
let grantStatus = 200;
let revocable = true;
let grants: Array<{ path: string; granted_at: string }> = [];

function jsonRes(body: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response;
}

const enabledState = {
  runtime: {
    enabled: true, backend: 'appcontainer', available: true,
    mode: 'auto', network: 'deny', notices: ['created AppContainer profile iCode.Sandbox'],
    confine_user_shell: false,
  },
  config: {
    mode: 'auto', network: 'deny', writable_paths: [], grant_workspace: true,
    confine_user_shell: false,
  },
  restart_required: false,
};

const disabledState = {
  runtime: { enabled: false, available: false, mode: 'auto', network: 'deny', confine_user_shell: false },
  config: {
    mode: 'auto', network: 'deny', writable_paths: [], grant_workspace: true,
    confine_user_shell: false,
  },
  restart_required: false,
};

// offState is the only mode from which switching confinement back on asks for
// consent: that is when new ACEs start appearing on disk.
const offState = {
  ...enabledState,
  config: { ...enabledState.config, mode: 'off' },
};

let sandboxState: unknown = enabledState;

beforeEach(() => {
  calls = [];
  nextStatus = 200;
  grantStatus = 200;
  revocable = true;
  grants = [];
  sandboxState = enabledState;
  globalThis.fetch = vi.fn(async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method || 'GET';
    calls.push({ url, method, body: init?.body as string | undefined });

    if (url.endsWith('/api/sandbox/grants')) {
      if (method === 'DELETE') {
        if (grantStatus !== 200) return jsonRes({ error: 'denied' }, grantStatus);
        const { path } = JSON.parse(String(init?.body || '{}'));
        const had = grants.some((g) => g.path === path);
        grants = grants.filter((g) => g.path !== path);
        return jsonRes({ ok: true, revoked: had, grants });
      }
      return jsonRes({ grants, revocable });
    }

    if (method === 'PUT') {
      if (nextStatus !== 200) return jsonRes({ error: 'boom' }, nextStatus);
      const patch = JSON.parse(String(init?.body || '{}'));
      const base = sandboxState as typeof enabledState;
      // The backend merges the patch into the stored config; mirroring that
      // here keeps the rendered state honest after a save.
      return jsonRes({
        ...base,
        config: { ...base.config, ...patch },
        restart_required: true,
      });
    }
    return jsonRes(sandboxState);
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
    sandboxState = disabledState;
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

describe('SandboxSection grant visibility', () => {
  it('lists the directories the sandbox wrote an entry into', async () => {
    grants = [
      { path: 'E:\\icode', granted_at: '2026-10-17T09:00:00Z' },
      { path: 'D:\\work\\report', granted_at: '2026-10-17T08:00:00Z' },
    ];
    render(<SandboxSection />);
    expect(await screen.findByText('E:\\icode')).toBeTruthy();
    expect(screen.getByText('D:\\work\\report')).toBeTruthy();
  });

  it('says there is nothing granted rather than rendering an empty box', async () => {
    render(<SandboxSection />);
    expect(await screen.findByText('tools.sandboxGrantsEmpty')).toBeTruthy();
  });
});

describe('SandboxSection revocation', () => {
  const setup = async () => {
    grants = [{ path: 'E:\\icode', granted_at: '2026-10-17T09:00:00Z' }];
    render(<SandboxSection />);
    await screen.findByText('E:\\icode');
  };

  it('asks before removing a grant, so one misclick cannot undo it', async () => {
    await setup();
    fireEvent.click(screen.getByText('tools.sandboxGrantsRevoke'));

    expect(screen.getByText('tools.sandboxGrantsRevokeConfirm')).toBeTruthy();
    // Nothing has been sent yet.
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);

    fireEvent.click(screen.getByText('tools.sandboxGrantsCancel'));
    expect(screen.queryByText('tools.sandboxGrantsRevokeConfirm')).toBeNull();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('revokes on confirm and drops the row', async () => {
    await setup();
    fireEvent.click(screen.getByText('tools.sandboxGrantsRevoke'));
    fireEvent.click(screen.getByText('tools.sandboxGrantsRevokeConfirm'));

    await waitFor(() => expect(screen.queryByText('E:\\icode')).toBeNull());
    const del = calls.find((c) => c.method === 'DELETE');
    expect(del).toBeTruthy();
    expect(del!.url).toContain('/api/sandbox/grants');
    expect(JSON.parse(del!.body!)).toEqual({ path: 'E:\\icode' });
    expect(screen.getByText(/tools\.sandboxGrantsRevoked/)).toBeTruthy();
  });

  it('does not claim a revocation when the entry was never there', async () => {
    grants = [{ path: 'E:\\stale', granted_at: '2026-10-17T09:00:00Z' }];
    render(<SandboxSection />);
    await screen.findByText('E:\\stale');
    // Remove it server-side first, so the DELETE reports revoked=false.
    grants = [];

    fireEvent.click(screen.getByText('tools.sandboxGrantsRevoke'));
    fireEvent.click(screen.getByText('tools.sandboxGrantsRevokeConfirm'));

    await waitFor(() => expect(screen.getByText(/tools\.sandboxGrantsAlreadyClean/)).toBeTruthy());
  });

  it('surfaces a failed revocation instead of hiding the row', async () => {
    grantStatus = 500;
    await setup();
    fireEvent.click(screen.getByText('tools.sandboxGrantsRevoke'));
    fireEvent.click(screen.getByText('tools.sandboxGrantsRevokeConfirm'));

    await waitFor(() => expect(screen.getByText(/tools\.sandboxGrantsFailed/)).toBeTruthy());
    // The directory is still granted, so it must still be listed.
    expect(screen.getByText('E:\\icode')).toBeTruthy();
  });

  it('disables revocation where the platform cannot undo it', async () => {
    revocable = false;
    await setup();
    const btn = screen.getByText('tools.sandboxGrantsRevoke').closest('button')!;
    expect(btn.disabled).toBe(true);
    expect(screen.getByText('tools.sandboxGrantsUnsupported')).toBeTruthy();
  });
});

describe('SandboxSection user shell scope', () => {
  it('leaves user commands unrestricted by default and says so', async () => {
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxActive');

    const box = screen.getByRole('checkbox') as HTMLInputElement;
    expect(box.checked).toBe(false);
    // The gap is named rather than glossed over: "sandbox enabled" does not
    // mean the user's own ! commands are confined.
    expect(screen.getByText('tools.sandboxUserShellGap')).toBeTruthy();
  });

  it('saves the opt-in and keeps reporting the live gap until a restart', async () => {
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxActive');

    fireEvent.click(screen.getByRole('checkbox'));

    await waitFor(() => {
      const put = calls.find((c) => c.method === 'PUT');
      expect(put).toBeTruthy();
      expect(JSON.parse(put!.body!)).toEqual({ confine_user_shell: true });
    });
    expect(screen.getByText('tools.sandboxRestartHint')).toBeTruthy();
    // The badge and the gap note follow runtime, not config. The running
    // process still has the old scope, so claiming otherwise would be the
    // exact false assurance this section is meant to prevent.
    expect(screen.getByText('tools.sandboxUserShellGap')).toBeTruthy();
  });

  it('does not warn about the gap when no sandbox is installed', async () => {
    sandboxState = disabledState;
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxInactive');
    // There is nothing to fall short of: no boundary exists at all, which the
    // inactive hint already states.
    expect(screen.queryByText('tools.sandboxUserShellGap')).toBeNull();
  });
});

describe('SandboxSection consent', () => {
  it('explains the disk change before turning confinement back on', async () => {
    sandboxState = offState;
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxActive');

    fireEvent.click(screen.getByText('tools.sandboxMode_on'));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('tools.sandboxConsentTitle')).toBeTruthy();
    // Consent comes first: no PUT until the user agrees.
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
  });

  it('cancelling consent leaves the mode untouched', async () => {
    sandboxState = offState;
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxActive');

    fireEvent.click(screen.getByText('tools.sandboxMode_on'));
    await screen.findByRole('dialog');
    fireEvent.click(screen.getByText('tools.sandboxConsentCancel'));

    expect(screen.queryByRole('dialog')).toBeNull();
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
  });

  it('saves the mode once consent is given', async () => {
    sandboxState = offState;
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxActive');

    fireEvent.click(screen.getByText('tools.sandboxMode_on'));
    await screen.findByRole('dialog');
    fireEvent.click(screen.getByText('tools.sandboxConsentConfirm'));

    await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
    const put = calls.find((c) => c.method === 'PUT')!;
    expect(JSON.parse(put.body!)).toEqual({ mode: 'on' });
  });

  it('does not interrupt a change between two enabled modes', async () => {
    render(<SandboxSection />);
    await screen.findByText('tools.sandboxActive');

    fireEvent.click(screen.getByText('tools.sandboxMode_on'));

    await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
