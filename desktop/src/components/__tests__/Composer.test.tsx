// @vitest-environment jsdom
import React, { useRef, useState } from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import Composer, { type ComposerProps, type AttachmentDraft } from '../Composer';
import { useAppStore } from '../../stores/appStore';

// Stub i18next entirely so the store's i18n bootstrap does not initialize a
// real instance inside jsdom. `t` returns the key, which keeps assertions
// stable across translation edits.
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

const setModeSpy = vi.fn();

function resetStore(mode = 'auto') {
  useAppStore.setState({
    tokenUsage: { input: 0, output: 0, cacheHit: 0, cost: '¥0.00', saved: 0 },
    mode,
    setMode: setModeSpy,
  });
}

function baseProps(overrides: Partial<ComposerProps> = {}): ComposerProps {
  return {
    value: '',
    onChange: vi.fn(),
    onSubmit: vi.fn(),
    onStop: vi.fn(),
    isStreaming: false,
    attachments: [],
    onAttachmentsChange: vi.fn(),
    onOpenPicker: vi.fn(),
    voiceActive: false,
    voiceBusy: false,
    onToggleVoice: vi.fn(),
    planPending: false,
    onConfirmPlan: vi.fn(),
    onDiscardPlan: vi.fn(),
    ctxWindow: 200000,
    ctxWindowLabel: '200K',
    modelLabel: 'glm-4',
    dragOver: false,
    onDragOver: vi.fn(),
    onDragLeave: vi.fn(),
    onDrop: vi.fn(),
    inputRef: React.createRef<HTMLTextAreaElement>(),
    ...overrides,
  };
}

/**
 * Keeps `value` in real state (the component is controlled) and clears it on
 * submit, which is what the page does — without this the history test would
 * recall the un-cleared draft instead of the submitted text.
 */
function Harness({ props }: { props: ComposerProps }) {
  const [value, setValue] = useState(props.value);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  return (
    <Composer
      {...props}
      inputRef={inputRef}
      value={value}
      onChange={setValue}
      onSubmit={(text) => { props.onSubmit(text); setValue(''); }}
    />
  );
}

function renderComposer(overrides: Partial<ComposerProps> = {}) {
  const props = baseProps(overrides);
  render(<Harness props={props} />);
  return { props, textarea: screen.getByRole('textbox') as HTMLTextAreaElement };
}

const type = (el: HTMLTextAreaElement, text: string) => fireEvent.change(el, { target: { value: text } });

beforeEach(() => {
  setModeSpy.mockClear();
  resetStore();
});

afterEach(cleanup);

describe('Composer — sending', () => {
  it('renders the placeholder and exposes the multiline input', () => {
    const { textarea } = renderComposer();
    expect(textarea.getAttribute('placeholder')).toBe('chat.placeholder');
    expect(textarea.getAttribute('aria-multiline')).toBe('true');
  });

  it('submits trimmed text on Enter and clears the box', () => {
    const { props, textarea } = renderComposer();
    type(textarea, '  hello  ');
    fireEvent.keyDown(textarea, { key: 'Enter' });

    expect(props.onSubmit).toHaveBeenCalledWith('hello');
    expect(textarea.value).toBe('');
  });

  it('inserts a newline instead of sending on Shift+Enter', () => {
    const { props, textarea } = renderComposer();
    type(textarea, 'line');
    fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: true });

    expect(props.onSubmit).not.toHaveBeenCalled();
    expect(textarea.value).toBe('line');
  });

  it('refuses to send whitespace-only input', () => {
    const { props, textarea } = renderComposer();
    type(textarea, '   ');
    fireEvent.keyDown(textarea, { key: 'Enter' });

    expect(props.onSubmit).not.toHaveBeenCalled();
    expect((screen.getByLabelText('chat.send') as HTMLButtonElement).disabled).toBe(true);
  });

  it('sends from the button once there is text', () => {
    const { props, textarea } = renderComposer();
    type(textarea, 'hi');

    const send = screen.getByLabelText('chat.send') as HTMLButtonElement;
    expect(send.disabled).toBe(false);
    fireEvent.click(send);

    expect(props.onSubmit).toHaveBeenCalledWith('hi');
  });

  it('recalls the most recent submission with ArrowUp', () => {
    const { textarea } = renderComposer();
    type(textarea, 'first');
    fireEvent.keyDown(textarea, { key: 'Enter' });
    type(textarea, 'second');
    fireEvent.keyDown(textarea, { key: 'Enter' });

    fireEvent.keyDown(textarea, { key: 'ArrowUp' });
    expect(textarea.value).toBe('second');

    fireEvent.keyDown(textarea, { key: 'ArrowUp' });
    expect(textarea.value).toBe('first');
  });
});

describe('Composer — streaming and voice', () => {
  it('swaps send for stop while streaming', () => {
    const { props } = renderComposer({ isStreaming: true });

    expect(screen.queryByLabelText('chat.send')).toBeNull();
    fireEvent.click(screen.getByLabelText('chat.stop'));

    expect(props.onStop).toHaveBeenCalledTimes(1);
  });

  it('reflects voice capture state and toggles it', () => {
    const { props } = renderComposer({ voiceActive: true });

    const mic = screen.getByLabelText('chat.voiceStop');
    expect(mic.getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(mic);

    expect(props.onToggleVoice).toHaveBeenCalledTimes(1);
  });

  it('disables the mic while a transcription is in flight', () => {
    renderComposer({ voiceBusy: true });
    expect((screen.getByLabelText('chat.voice') as HTMLButtonElement).disabled).toBe(true);
  });
});

describe('Composer — attachments', () => {
  const image: AttachmentDraft = { mime: 'image/png', data: 'AAA', name: 'shot.png' };
  const pdf: AttachmentDraft = { mime: 'application/pdf', data: 'BBB', name: 'spec.pdf' };

  it('previews images inline', () => {
    renderComposer({ attachments: [image] });
    const img = screen.getByAltText('shot.png') as HTMLImageElement;
    expect(img.src).toContain('data:image/png;base64,AAA');
  });

  it('renders a PDF as a named chip instead of a broken image', () => {
    renderComposer({ attachments: [pdf] });
    expect(screen.getByText('spec.pdf')).toBeTruthy();
    expect(screen.queryByAltText('spec.pdf')).toBeNull();
  });

  it('removes exactly the clicked attachment', () => {
    const onAttachmentsChange = vi.fn();
    renderComposer({ attachments: [image, pdf], onAttachmentsChange });

    fireEvent.click(screen.getAllByLabelText('chat.removeAttachment')[1]);

    expect(onAttachmentsChange).toHaveBeenCalledWith([image]);
  });

  it('sends an image with no caption (regression: Enter used to be swallowed)', () => {
    const { props, textarea } = renderComposer({ attachments: [image] });

    const send = screen.getByLabelText('chat.send') as HTMLButtonElement;
    expect(send.disabled).toBe(false);

    fireEvent.keyDown(textarea, { key: 'Enter' });
    expect(props.onSubmit).toHaveBeenCalledWith('');
  });
});

describe('Composer — slash menu and modes', () => {
  it('offers matching commands and completes with Tab', () => {
    const { textarea } = renderComposer();
    type(textarea, '/clear');

    expect(screen.getAllByRole('option').length).toBeGreaterThan(0);
    fireEvent.keyDown(textarea, { key: 'Tab' });

    expect(textarea.value).toBe('/clear ');
    // Menu closes once a space is present (args are being typed).
    expect(screen.queryAllByRole('option')).toHaveLength(0);
  });

  it('cycles the agent mode on Tab when no slash menu is open', () => {
    const { textarea } = renderComposer();
    // Store starts at 'auto', which is last in the cycle → wraps to 'plan'.
    fireEvent.keyDown(textarea, { key: 'Tab' });

    expect(setModeSpy).toHaveBeenCalledWith('plan');
  });

  it('opens the file picker on @ in an empty box', () => {
    const { props, textarea } = renderComposer();
    fireEvent.keyDown(textarea, { key: '@' });
    expect(props.onOpenPicker).toHaveBeenCalledTimes(1);
  });
});

describe('Composer — plan confirmation', () => {
  it('shows the accept bar only while a plan is pending', () => {
    renderComposer({ planPending: true });
    expect(screen.getByText(/chat.planAccept/)).toBeTruthy();
    cleanup();

    renderComposer({ planPending: false });
    expect(screen.queryByText(/chat.planAccept/)).toBeNull();
  });

  it('forwards accept and discard', () => {
    const onConfirmPlan = vi.fn();
    const onDiscardPlan = vi.fn();
    renderComposer({ planPending: true, onConfirmPlan, onDiscardPlan });

    fireEvent.click(screen.getByText('chat.planAccept'));
    fireEvent.click(screen.getByText('chat.planDiscard'));

    expect(onConfirmPlan).toHaveBeenCalledTimes(1);
    expect(onDiscardPlan).toHaveBeenCalledTimes(1);
  });
});
