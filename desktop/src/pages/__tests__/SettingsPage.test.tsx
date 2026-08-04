// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import SettingsPage from '../SettingsPage';
import { useAppStore } from '../../stores/appStore';

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

// jsdom does not implement matchMedia; SettingsPage needs it for the "auto"
// theme branch.
beforeEach(() => {
  window.matchMedia = window.matchMedia || (() => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
  })) as unknown as typeof window.matchMedia;
  useAppStore.setState({ models: [], customModels: [] });
});

function clickTheme(label: string) {
  fireEvent.click(screen.getByText(label));
}

describe('SettingsPage theme switching', () => {
  beforeEach(() => cleanup());

  it('renders nothing when closed', () => {
    const { container } = render(<SettingsPage visible={false} onClose={() => {}} />);
    expect(container.firstChild).toBeNull();
  });

  it('does not touch the theme on open (App owns the initial data-theme)', () => {
    render(<SettingsPage visible onClose={() => {}} />);
    expect(document.documentElement.getAttribute('data-theme')).toBeNull();
    expect(localStorage.getItem('icode.theme')).toBeNull();
  });

  it('switches to light theme and persists it', () => {
    render(<SettingsPage visible onClose={() => {}} />);
    clickTheme('settings.light');
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
    expect(localStorage.getItem('icode.theme')).toBe('light');
  });

  it('switches back to dark theme', () => {
    render(<SettingsPage visible onClose={() => {}} />);
    clickTheme('settings.light');
    clickTheme('settings.dark');
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    expect(localStorage.getItem('icode.theme')).toBe('dark');
  });

  it('auto theme resolves via prefers-color-scheme', () => {
    window.matchMedia = (() => ({ matches: true })) as unknown as typeof window.matchMedia;
    render(<SettingsPage visible onClose={() => {}} />);
    clickTheme('settings.auto');
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
    expect(localStorage.getItem('icode.theme')).toBe('auto');
  });
});
