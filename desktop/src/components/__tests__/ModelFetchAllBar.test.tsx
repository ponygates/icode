// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import { ModelFetchAllBar } from '../ModelFetchAllBar';
import type { UseModelFetch } from '../../lib/useModelFetch';

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

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

/** Only the fields the bar actually reads need to be real. */
function fakeMf(over: Partial<UseModelFetch> = {}): UseModelFetch {
  return {
    fetching: null, panel: null, lists: {}, ticked: {}, error: {},
    saving: false, meta: {},
    loadMeta: vi.fn(),
    fetchModels: vi.fn(),
    fetchAll: vi.fn().mockResolvedValue(null),
    allProgress: null,
    allSummary: null,
    clearAllSummary: vi.fn(),
    toggle: vi.fn(), selectAll: vi.fn(),
    save: vi.fn().mockResolvedValue(undefined),
    close: vi.fn(),
    ...over,
  } as UseModelFetch;
}

describe('ModelFetchAllBar', () => {
  it('invokes the sweep with every vendor', () => {
    const mf = fakeMf();
    render(<ModelFetchAllBar mf={mf} providers={['a', 'b', 'c']} />);

    fireEvent.click(screen.getByText('models.fetchAll'));
    expect(mf.fetchAll).toHaveBeenCalledWith(['a', 'b', 'c']);
  });

  it('is disabled when there are no vendors to sweep', () => {
    render(<ModelFetchAllBar mf={fakeMf()} providers={[]} />);
    expect((screen.getByText('models.fetchAll') as HTMLButtonElement).disabled).toBe(true);
  });

  it('shows live progress and blocks a second run while sweeping', () => {
    const mf = fakeMf({ allProgress: { done: 2, total: 7 } });
    render(<ModelFetchAllBar mf={mf} providers={['a', 'b']} />);

    expect(screen.getByText('models.fetchingAll({"done":2,"total":7})')).toBeTruthy();
    const btn = screen.getByText(/models.fetchingAll/) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);

    fireEvent.click(btn);
    expect(mf.fetchAll).not.toHaveBeenCalled();
  });

  it('breaks the tally down by outcome instead of one error count', () => {
    const mf = fakeMf({
      allSummary: { total: 13, ok: 9, unsupported: 2, failed: 2 },
    });
    render(<ModelFetchAllBar mf={mf} providers={['a']} />);

    // "2 vendors have no live endpoint" and "2 rejected your key" need
    // different reactions, so they must not be merged.
    expect(screen.getByText(/fetchAllOk/)).toBeTruthy();
    expect(screen.getByText('models.fetchAllUnsupported({"count":2})')).toBeTruthy();
    expect(screen.getByText('models.fetchAllFailed({"count":2})')).toBeTruthy();
    expect(screen.getByText(/models.fetchAllNext/)).toBeTruthy();
  });

  it('hides zero-value outcomes', () => {
    const mf = fakeMf({ allSummary: { total: 3, ok: 3, unsupported: 0, failed: 0 } });
    render(<ModelFetchAllBar mf={mf} providers={['a']} />);

    expect(screen.queryByText(/fetchAllUnsupported/)).toBeNull();
    expect(screen.queryByText(/fetchAllFailed/)).toBeNull();
  });

  it('dismisses the summary', () => {
    const mf = fakeMf({ allSummary: { total: 1, ok: 1, unsupported: 0, failed: 0 } });
    render(<ModelFetchAllBar mf={mf} providers={['a']} />);

    fireEvent.click(screen.getByTitle('models.dismiss'));
    expect(mf.clearAllSummary).toHaveBeenCalled();
  });

  it('still offers the button after a sweep so it can be re-run', () => {
    const mf = fakeMf({ allSummary: { total: 1, ok: 1, unsupported: 0, failed: 0 } });
    render(<ModelFetchAllBar mf={mf} providers={['a']} />);

    const btn = screen.getByText('models.fetchAll') as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    fireEvent.click(btn);
    expect(mf.fetchAll).toHaveBeenCalled();
  });
});
