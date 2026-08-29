// Desktop keyboard-shortcut customization (D8).
//
// Bindings are stored in localStorage as a JSON map of action → key combo,
// keyed under icode.shortcuts. Every global keydown handler (App.tsx, the
// command palette) resolves its binding through loadShortcuts() instead of
// hardcoding, so an edit in the Settings → Shortcuts page takes effect on the
// next keystroke without a restart.
//
// A binding is a simple struct rather than a string, so the recorder in the
// settings page can capture a real KeyboardEvent losslessly (modifier state is
// explicit — meta is normalized onto ctrl because Windows/macOS differ).

export type ShortcutAction =
  | 'commandPalette'
  | 'newSession'
  | 'focusInput'
  | 'openSettings'
  | 'stopGeneration'
  | 'shortcutPanel';

export interface ShortcutBinding {
  key: string; // KeyboardEvent.key, e.g. 'k', ',', 'Escape', '?'
  ctrl?: boolean;
  shift?: boolean;
  alt?: boolean;
}

export const SHORTCUT_ACTIONS: ShortcutAction[] = [
  'commandPalette',
  'newSession',
  'focusInput',
  'openSettings',
  'stopGeneration',
  'shortcutPanel',
];

export const DEFAULT_SHORTCUTS: Record<ShortcutAction, ShortcutBinding> = {
  commandPalette: { key: 'k', ctrl: true },
  newSession: { key: 'n', ctrl: true },
  focusInput: { key: 'l', ctrl: true },
  openSettings: { key: ',', ctrl: true },
  stopGeneration: { key: 'Escape' },
  shortcutPanel: { key: '?' },
};

const STORAGE_KEY = 'icode.shortcuts';

// Load the user's bindings, merging over defaults so a partially-migrated
// stored map (or a new action added in a later version) never drops bindings.
export function loadShortcuts(): Record<ShortcutAction, ShortcutBinding> {
  const merged: Record<ShortcutAction, ShortcutBinding> = {
    ...DEFAULT_SHORTCUTS,
  };
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return merged;
    const parsed = JSON.parse(raw) as Partial<Record<ShortcutAction, ShortcutBinding>>;
    for (const action of SHORTCUT_ACTIONS) {
      const b = parsed[action];
      if (b && typeof b.key === 'string' && b.key !== '') {
        merged[action] = b;
      }
    }
  } catch {
    /* storage unavailable — fall back to defaults */
  }
  return merged;
}

export function saveShortcuts(map: Record<ShortcutAction, ShortcutBinding>) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(map));
  } catch {
    /* best-effort */
  }
}

export function resetShortcuts() {
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* best-effort */
  }
}

// Does a keydown event match the given binding? Ctrl and Meta are treated as
// interchangeable (⌘ on macOS behaves like Ctrl in this app's shortcuts).
export function matchesBinding(e: KeyboardEvent, b: ShortcutBinding): boolean {
  if (e.key !== b.key) return false;
  const ctrl = e.ctrlKey || e.metaKey;
  if (!!b.ctrl !== ctrl) return false;
  if (!!b.shift !== e.shiftKey) return false;
  if (!!b.alt !== e.altKey) return false;
  return true;
}

// Key fragments for a binding, e.g. ["Ctrl", "K"] or ["Esc"] — used by the
// shortcut panel to render each key as its own chip.
export function bindingParts(b: ShortcutBinding): string[] {
  const parts: string[] = [];
  if (b.ctrl) parts.push('Ctrl');
  if (b.alt) parts.push('Alt');
  if (b.shift) parts.push('Shift');
  const key =
    b.key === 'Escape' ? 'Esc'
    : b.key === ' ' ? 'Space'
    : b.key.length === 1 ? b.key.toUpperCase()
    : b.key;
  parts.push(key);
  return parts;
}

// Human-readable label for a binding, e.g. "Ctrl+K", "Ctrl+Shift+?", "Esc".
export function bindingLabel(b: ShortcutBinding): string {
  return bindingParts(b).join('+');
}

// Capture a key combo from a settings-editor keydown. Returns null when the
// event is a pure modifier press (Control/Alt/Shift/Meta alone) or Esc (used
// to cancel recording).
export function recordBinding(e: KeyboardEvent): ShortcutBinding | null {
  if (e.key === 'Escape') return null;
  if (e.key === 'Control' || e.key === 'Alt' || e.key === 'Shift' || e.key === 'Meta') return null;
  return {
    key: e.key,
    ctrl: e.ctrlKey || e.metaKey,
    shift: e.shiftKey,
    alt: e.altKey,
  };
}
