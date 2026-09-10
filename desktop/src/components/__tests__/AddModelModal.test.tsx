// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { AddModelModal, type AddModelPayload } from '../AddModelModal';

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

const submitBtn = () => screen.getByText('models.add') as HTMLButtonElement;
const idInput = () => screen.getAllByRole('textbox')[0] as HTMLInputElement;
const nameInput = () => screen.getAllByRole('textbox')[1] as HTMLInputElement;

describe('AddModelModal', () => {
  it('renders nothing while closed', () => {
    const { container } = render(
      <AddModelModal open={false} onClose={() => {}} onSubmit={vi.fn()} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('locks the provider when opened from a vendor card', () => {
    render(<AddModelModal open presetProvider="demo" onClose={() => {}} onSubmit={vi.fn()} />);

    expect(screen.getByText('models.addModelTo({"provider":"demo"})')).toBeTruthy();
    // The vendor is implied by which card was clicked, so it is not a field.
    expect(screen.queryByText('models.providerName')).toBeNull();
  });

  it('asks for the vendor when opened from the toolbar', () => {
    render(<AddModelModal open onClose={() => {}} onSubmit={vi.fn()} />);
    expect(screen.getByText('models.providerName *')).toBeTruthy();
  });

  it('submits the bare vendor id, the parsed limits and a name fallback', async () => {
    const onSubmit = vi.fn().mockResolvedValue(null);
    const onClose = vi.fn();
    render(<AddModelModal open presetProvider="demo" onClose={onClose} onSubmit={onSubmit} />);

    const boxes = screen.getAllByRole('textbox') as HTMLInputElement[];
    fireEvent.change(boxes[0], { target: { value: 'demo-new' } });   // id
    fireEvent.change(boxes[1], { target: { value: 'New Model' } });  // name
    fireEvent.change(screen.getByPlaceholderText('128000'), { target: { value: '200000' } });
    fireEvent.change(screen.getByPlaceholderText('8192'), { target: { value: '16384' } });
    fireEvent.click(submitBtn());

    const payload: AddModelPayload = onSubmit.mock.calls[0][0];
    // The bare id matters: the composite "demo/demo-new" key is built server-side.
    expect(payload).toEqual({
      id: 'demo-new', name: 'New Model', provider: 'demo',
      contextWindow: 200000, maxOutputTokens: 16384,
    });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('falls back to the id when no display name is given', async () => {
    const onSubmit = vi.fn().mockResolvedValue(null);
    render(<AddModelModal open presetProvider="demo" onClose={() => {}} onSubmit={onSubmit} />);

    fireEvent.change(idInput(), { target: { value: 'demo-new' } });
    fireEvent.click(submitBtn());

    expect(onSubmit.mock.calls[0][0].name).toBe('demo-new');
  });

  it('omits the limits entirely when left blank', async () => {
    const onSubmit = vi.fn().mockResolvedValue(null);
    render(<AddModelModal open presetProvider="demo" onClose={() => {}} onSubmit={onSubmit} />);

    fireEvent.change(idInput(), { target: { value: 'demo-new' } });
    fireEvent.click(submitBtn());

    const payload = onSubmit.mock.calls[0][0];
    expect(payload.contextWindow).toBeUndefined();
    expect(payload.maxOutputTokens).toBeUndefined();
  });

  it('rejects an id containing whitespace without hitting the backend', async () => {
    const onSubmit = vi.fn();
    render(<AddModelModal open presetProvider="demo" onClose={() => {}} onSubmit={onSubmit} />);

    fireEvent.change(idInput(), { target: { value: 'bad id' } });
    fireEvent.click(submitBtn());

    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText('models.modelIdNoSpace')).toBeTruthy();
  });

  it('keeps the dialog open and shows a server error verbatim', async () => {
    // The server answers 409 with "this vendor already ships that model".
    const onSubmit = vi.fn().mockResolvedValue('demo 已内置模型 demo-a，无需重复添加');
    const onClose = vi.fn();
    render(<AddModelModal open presetProvider="demo" onClose={onClose} onSubmit={onSubmit} />);

    fireEvent.change(idInput(), { target: { value: 'demo-a' } });
    fireEvent.click(submitBtn());

    await waitFor(() =>
      expect(screen.getByText('demo 已内置模型 demo-a，无需重复添加')).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });

  it('will not submit without an id, and clears state on reopen', async () => {
    const onSubmit = vi.fn().mockResolvedValue(null);
    const { rerender } = render(
      <AddModelModal open presetProvider="demo" onClose={() => {}} onSubmit={onSubmit} />,
    );

    expect(submitBtn().disabled).toBe(true);
    fireEvent.change(idInput(), { target: { value: 'demo-new' } });
    expect(submitBtn().disabled).toBe(false);

    // Close and reopen — a half-typed id must not leak into the next attempt.
    rerender(<AddModelModal open={false} presetProvider="demo" onClose={() => {}} onSubmit={onSubmit} />);
    rerender(<AddModelModal open presetProvider="demo" onClose={() => {}} onSubmit={onSubmit} />);
    expect(idInput().value).toBe('');
    expect(submitBtn().disabled).toBe(true);
  });
});
