// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import {
  matchesBinding,
  recordBinding,
  eventKey,
  DEFAULT_SHORTCUTS,
  type ShortcutBinding,
} from './shortcuts';

// Build a keydown-like event. jsdom's KeyboardEvent supports key/code plus
// the modifier init flags, which is all the shortcut layer reads.
function kev(
  key: string,
  opts: { ctrl?: boolean; shift?: boolean; alt?: boolean; code?: string } = {},
): KeyboardEvent {
  return new KeyboardEvent('keydown', {
    key,
    code: opts.code ?? '',
    ctrlKey: !!opts.ctrl,
    shiftKey: !!opts.shift,
    altKey: !!opts.alt,
  });
}

describe('matchesBinding — 正常（非 IME）事件', () => {
  it('Ctrl+, 打开设置', () => {
    expect(matchesBinding(kev(',', { ctrl: true, code: 'Comma' }), DEFAULT_SHORTCUTS.openSettings)).toBe(true);
  });
  it('Ctrl+K 打开命令面板', () => {
    expect(matchesBinding(kev('k', { ctrl: true, code: 'KeyK' }), DEFAULT_SHORTCUTS.commandPalette)).toBe(true);
  });
  it('修饰键不符时拒绝（缺 Ctrl）', () => {
    expect(matchesBinding(kev('k', { code: 'KeyK' }), DEFAULT_SHORTCUTS.commandPalette)).toBe(false);
  });
  it('Escape 停止生成（无修饰键）', () => {
    expect(matchesBinding(kev('Escape'), DEFAULT_SHORTCUTS.stopGeneration)).toBe(true);
  });
});

describe('matchesBinding — 中文输入法（e.key === "Process"）兼容', () => {
  // Windows CJK IME swallows character keydowns and reports key='Process';
  // the recovery path maps e.code back to the logical key.
  it('Ctrl+, 在 IME 激活时仍打开设置', () => {
    expect(
      matchesBinding(kev('Process', { ctrl: true, code: 'Comma' }), DEFAULT_SHORTCUTS.openSettings),
    ).toBe(true);
  });
  it('Ctrl+K 在 IME 激活时仍打开命令面板', () => {
    expect(
      matchesBinding(kev('Process', { ctrl: true, code: 'KeyK' }), DEFAULT_SHORTCUTS.commandPalette),
    ).toBe(true);
  });
  it('Ctrl+N / Ctrl+L 数字字母类全部恢复', () => {
    expect(matchesBinding(kev('Process', { ctrl: true, code: 'KeyN' }), DEFAULT_SHORTCUTS.newSession)).toBe(true);
    expect(matchesBinding(kev('Process', { ctrl: true, code: 'KeyL' }), DEFAULT_SHORTCUTS.focusInput)).toBe(true);
  });
  it('Shift+? 在 IME 激活时仍打开快捷键面板', () => {
    expect(matchesBinding(kev('Process', { shift: true, code: 'Slash' }), DEFAULT_SHORTCUTS.shortcutPanel)).toBe(true);
  });
  it('默认 ? 绑定必须记录 shift:true（否则真实按键 Shift+/ 被拒绝，面板永远打不开）', () => {
    expect(DEFAULT_SHORTCUTS.shortcutPanel).toEqual({ key: '?', shift: true });
  });
  it('不带 Shift 的 ? 不触发快捷键面板', () => {
    // IME 恢复出 '?' 但 shiftKey=false —— binding 要求 Shift，拒绝
    expect(matchesBinding(kev('Process', { code: 'Slash' }), DEFAULT_SHORTCUTS.shortcutPanel)).toBe(false);
  });
  it('IME Process 但 code 无法识别时不误触', () => {
    expect(matchesBinding(kev('Process', { ctrl: true }), DEFAULT_SHORTCUTS.commandPalette)).toBe(false);
  });
  it('IME Process 但修饰键不符时拒绝', () => {
    expect(matchesBinding(kev('Process', { code: 'KeyK' }), DEFAULT_SHORTCUTS.commandPalette)).toBe(false);
  });
  it('IME 激活但用户只按了字母（无修饰键）不触发命令面板', () => {
    // code 恢复出 'k'，但 binding 需要 Ctrl —— 不匹配
    expect(matchesBinding(kev('Process', { code: 'KeyK' }), DEFAULT_SHORTCUTS.commandPalette)).toBe(false);
  });
});

describe('eventKey', () => {
  it('正常 key 原样返回', () => {
    expect(eventKey(kev('k', { ctrl: true, code: 'KeyK' }))).toBe('k');
  });
  it('Process + KeyJ → j', () => {
    expect(eventKey(kev('Process', { code: 'KeyJ' }))).toBe('j');
  });
  it('Process + Digit5 → 5', () => {
    expect(eventKey(kev('Process', { code: 'Digit5' }))).toBe('5');
  });
  it('Process + 无法识别的 code 保留 Process', () => {
    expect(eventKey(kev('Process', { code: 'KanaMode' }))).toBe('Process');
  });
});

describe('recordBinding', () => {
  it('录制 Ctrl+J', () => {
    const b = recordBinding(kev('j', { ctrl: true, code: 'KeyJ' }));
    expect(b).toEqual({ key: 'j', ctrl: true, shift: false, alt: false } satisfies ShortcutBinding);
  });
  it('IME 激活时录制 Ctrl+J 依然得到 key="j" 而非 "Process"', () => {
    const b = recordBinding(kev('Process', { ctrl: true, code: 'KeyJ' }));
    expect(b).toEqual({ key: 'j', ctrl: true, shift: false, alt: false } satisfies ShortcutBinding);
  });
  it('纯修饰键返回 null', () => {
    expect(recordBinding(kev('Control', { ctrl: true, code: 'ControlLeft' }))).toBeNull();
    expect(recordBinding(kev('Shift', { shift: true, code: 'ShiftLeft' }))).toBeNull();
  });
  it('Escape 取消录制', () => {
    expect(recordBinding(kev('Escape'))).toBeNull();
  });
  it('IME Process 且 code 无法识别时忽略（继续等待下一次按键）', () => {
    expect(recordBinding(kev('Process'))).toBeNull();
  });
});
