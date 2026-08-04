// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import ModelPicker from '../ModelPicker';
import { useAppStore, type Model } from '../../stores/appStore';

// Stub i18next entirely so the store's i18n bootstrap (i18n/index.ts) does not
// initialize a real instance inside jsdom.
vi.mock('i18next', () => ({
  default: {
    use: () => ({ init: () => {} }),
    t: (key: string) => key,
  },
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

const models: Model[] = [
  { id: 'deepseek/deepseek-chat', name: 'DeepSeek Chat', provider: 'deepseek', plan: 'free' },
  { id: 'openrouter/claude', name: 'Claude Sonnet', provider: 'openrouter', plan: 'pro' },
  { id: 'zhipu/glm-4', name: 'GLM-4', provider: 'zhipu', plan: 'free' },
];

function resetStore() {
  useAppStore.setState({
    models: [],
    customModels: [],
    selectedModel: 'openrouter/free',
  });
}

describe('ModelPicker', () => {
  beforeEach(() => {
    resetStore();
    useAppStore.getState().setModels(models);
  });

  afterEach(() => {
    cleanup();
  });

  it('renders nothing when closed', () => {
    const { container } = render(<ModelPicker open={false} onClose={() => {}} />);
    expect(container.firstChild).toBeNull();
  });

  it('lists every provider group when no query is entered', () => {
    render(<ModelPicker open onClose={() => {}} />);
    expect(screen.getByText('deepseek')).toBeTruthy();
    expect(screen.getByText('openrouter')).toBeTruthy();
    expect(screen.getByText('zhipu')).toBeTruthy();
    expect(screen.getByText('DeepSeek Chat')).toBeTruthy();
    expect(screen.getByText('Claude Sonnet')).toBeTruthy();
  });

  it('filters by model name, id and provider', () => {
    render(<ModelPicker open onClose={() => {}} />);
    const input = screen.getByPlaceholderText('models.search');

    fireEvent.change(input, { target: { value: 'claude' } });
    expect(screen.getByText('Claude Sonnet')).toBeTruthy();
    expect(screen.queryByText('DeepSeek Chat')).toBeNull();

    fireEvent.change(input, { target: { value: 'glm' } });
    expect(screen.getByText('GLM-4')).toBeTruthy();
    expect(screen.queryByText('Claude Sonnet')).toBeNull();

    fireEvent.change(input, { target: { value: 'zhipu' } });
    expect(screen.getByText('GLM-4')).toBeTruthy();
  });

  it('switches the selected model and closes on click', () => {
    const onClose = vi.fn();
    render(<ModelPicker open onClose={onClose} />);
    fireEvent.click(screen.getByText('Claude Sonnet'));
    expect(useAppStore.getState().selectedModel).toBe('openrouter/claude');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('shows the no-results hint when nothing matches', () => {
    render(<ModelPicker open onClose={() => {}} />);
    fireEvent.change(screen.getByPlaceholderText('models.search'), { target: { value: 'zzz-no-match' } });
    expect(screen.getByText('models.noResults')).toBeTruthy();
  });
});
