import { useEffect, useRef } from 'react';

// Keyboard/focus semantics for a modal dialog: move focus into the dialog on
// open, trap Tab within it, close on Escape, and restore the previously focused
// element on close. Attach the returned ref to the dialog container and mark it
// `role="dialog" aria-modal="true"`. Only active while `open` is true.
export function useDialogA11y(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDivElement>(null);
  const restoreRef = useRef<Element | null>(null);
  useEffect(() => {
    if (!open) return;
    restoreRef.current = document.activeElement;
    const el = ref.current;
    const focusables = (): HTMLElement[] =>
      Array.from(
        el?.querySelectorAll<HTMLElement>(
          'button,[href],input,select,textarea,[tabindex]:not([tabindex="-1"])'
        ) ?? []
      ).filter((n) => !n.hasAttribute('disabled'));
    // Focus the first control once the dialog has painted.
    const raf = requestAnimationFrame(() => focusables()[0]?.focus());
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.stopPropagation(); onClose(); return; }
      if (e.key !== 'Tab') return;
      const f = focusables();
      if (f.length === 0) return;
      const first = f[0];
      const last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    };
    document.addEventListener('keydown', onKey, true);
    return () => {
      cancelAnimationFrame(raf);
      document.removeEventListener('keydown', onKey, true);
      (restoreRef.current as HTMLElement | null)?.focus?.();
    };
  }, [open, onClose]);
  return ref;
}
