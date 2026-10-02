// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import ChangedFilesBar, { toolTargetFile } from '../ChangedFilesBar';
import { useAppStore } from '../../stores/appStore';

// Stub i18next: the store's bootstrap must not initialize a real instance.
vi.mock('i18next', () => ({
  default: {
    use: () => ({ init: () => {} }),
    t: (key: string) => key,
  },
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string, opts?: any) => key.replace('{{count}}', String(opts?.count ?? '')) }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));
// DiffViewer issues a real fetch 鈥?stub the whole viewer behind a marker div.
vi.mock('../DiffViewer', () => ({
  default: ({ steps }: { steps: number }) => <div data-testid="diff-viewer">steps:{steps}</div>,
}));

const FILES = [
  'src/a.ts',
  'src/b.go',
  'src/c.py',
  'src/d.rs',
  'src/e.java',
  'src/f.kt',
  'src/g.zig',
  'src/very/deeply/nested/h/i.tsx',
];

beforeEach(() => {
  useAppStore.setState({
    backendUrl: 'http://x',
    sessions: [{
      id: 's1', title: 't', modelId: 'm', provider: 'p', createdAt: 0,
      messages: [{ id: 'm1', role: 'assistant', content: 'hi', timestamp: 0, changedFiles: FILES }],
    }],
  } as any);
  vi.spyOn(window, 'confirm').mockReturnValue(true);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('toolTargetFile', () => {
  it('maps each file-modifying tool to its path argument', () => {
    expect(toolTargetFile('write_file', '{"path":"a.txt","content":"x"}')).toBe('a.txt');
    expect(toolTargetFile('edit', '{"file_path":"b.ts","old_string":"1","new_string":"2"}')).toBe('b.ts');
    expect(toolTargetFile('search_replace', '{"file_path":"c.go"}')).toBe('c.go');
  });

  it('returns empty for non-file tools, missing args, bad JSON, non-string values', () => {
    expect(toolTargetFile('bash', '{"command":"rm -rf"}')).toBe('');
    expect(toolTargetFile('write_file')).toBe('');
    expect(toolTargetFile('edit', 'not json')).toBe('');
    expect(toolTargetFile('write_file', '{"path":42}')).toBe('');
  });
});

describe('ChangedFilesBar', () => {
  it('renders header with count, chips with basenames, and +N overflow', () => {
    render(<ChangedFilesBar sessionId="s1" msgId="m1" files={FILES} />);
    expect(screen.getByText('chat.changedFiles')).toBeTruthy();
    expect(screen.getByTitle('src/a.ts').textContent).toBe('a.ts');
    // 6 chips render; the 7th+ collapse into the +N overflow counter.
    expect(screen.getByTitle(FILES[5]).textContent).toBe('f.kt');
    expect(screen.queryByTitle(FILES[7])).toBeNull();
    expect(screen.getByText('+' + (FILES.length - 6))).toBeTruthy();
  });

  it('opens the turn diff viewer on click (steps=1)', () => {
    render(<ChangedFilesBar sessionId="s1" msgId="m1" files={FILES} />);
    fireEvent.click(screen.getByText('chat.viewDiff'));
    expect(screen.getByTestId('diff-viewer').textContent).toBe('steps:1');
  });

  it('rewinds one step and strips changedFiles from the message', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ files: ['src/a.ts'] }) });
    vi.stubGlobal('fetch', fetchMock);
    render(<ChangedFilesBar sessionId="s1" msgId="m1" files={FILES} />);
    fireEvent.click(screen.getByText('chat.rewindTurn'));
    await waitFor(() => {
      const msg = useAppStore.getState().sessions[0].messages[0];
      expect(msg.changedFiles).toBeUndefined();
    });
    expect(fetchMock).toHaveBeenCalledWith(
      'http://x/api/checkpoints/rewind',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ session_id: 's1', steps: 1 }),
      }),
    );
    vi.unstubAllGlobals();
  });

  it('keeps the bar when the user cancels the confirm', () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    render(<ChangedFilesBar sessionId="s1" msgId="m1" files={FILES} />);
    fireEvent.click(screen.getByText('chat.rewindTurn'));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByText('chat.changedFiles')).toBeTruthy();
    vi.unstubAllGlobals();
  });
});
