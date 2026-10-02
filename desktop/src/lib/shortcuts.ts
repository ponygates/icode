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
  // '?' can only be typed with Shift on every layout we support — the binding
  // must record that, or matchesBinding() (which compares shift strictly)
  // rejects the real keystroke and the panel can never open.
  shortcutPanel: { key: '?', shift: true },
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

// Recover the logical key from a Windows IME keydown. When a CJK IME
// (Chinese/Japanese pinyin etc.) is active, Windows + Chromium report
// e.key === 'Process' for character keys — the keystroke is routed to the IME
// composition engine first. That used to silently break EVERY character
// shortcut (Ctrl+K, Ctrl+N, Ctrl+L, Ctrl+, and ?) while the IME was on.
// The physical key is still available on e.code ('KeyK', 'Comma', 'Slash', …),
// so map it back to the KeyboardEvent.key spelling this file stores.
function keyFromImeEvent(e: KeyboardEvent): string {
  if (e.code === 'Comma') return ',';
  if (e.code === 'Period') return '.';
  if (e.code === 'Slash') return e.shiftKey ? '?' : '/';
  if (e.code === 'Semicolon') return e.shiftKey ? ':' : ';';
  if (e.code === 'Quote') return e.shiftKey ? '"' : "'";
  if (e.code === 'Space') return ' ';
  if (e.code.startsWith('Key')) return e.code.slice(3).toLowerCase();
  if (e.code.startsWith('Digit')) return e.code.slice(5);
  return e.key; // unknown layout — keep 'Process'; the binding simply won't match
}

// Logical key of a keydown, transparently handling the Windows IME 'Process'
// case so callers never need to care whether an IME is active.
export function eventKey(e: KeyboardEvent): string {
  return e.key === 'Process' ? keyFromImeEvent(e) : e.key;
}

// Does a keydown event match the given binding? Ctrl and Meta are treated as
// interchangeable (⌘ on macOS behaves like Ctrl in this app's shortcuts).
export function matchesBinding(e: KeyboardEvent, b: ShortcutBinding): boolean {
  if (eventKey(e) !== b.key) return false;
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
  const key = eventKey(e);
  if (key === 'Process') return null; // IME on + unmapped key — ignore, keep recording
  return {
    key,
    ctrl: e.ctrlKey || e.metaKey,
    shift: e.shiftKey,
    alt: e.altKey,
  };
}
