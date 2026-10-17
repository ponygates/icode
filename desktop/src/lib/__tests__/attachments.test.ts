import { describe, it, expect } from 'vitest';
import { toAttachmentPayload } from '../attachments';

describe('toAttachmentPayload', () => {
  it('tags images as image and carries the original name', () => {
    const [att] = toAttachmentPayload([{ mime: 'image/png', data: 'AAA', name: 'shot.png' }]);
    expect(att.type).toBe('image');
    expect(att.name).toBe('shot.png');
  });

  it('tags PDFs explicitly so the backend can route them', () => {
    const [att] = toAttachmentPayload([
      { mime: 'application/pdf', data: 'JVBERi0=', name: '合同.pdf' },
    ]);
    expect(att.type).toBe('pdf');
    expect(att.mime).toBe('application/pdf');
    expect(att.name).toBe('合同.pdf');
  });

  it('omits the name key when the source has none', () => {
    const [att] = toAttachmentPayload([{ mime: 'image/jpeg', data: 'AAA' }]);
    expect('name' in att).toBe(false);
  });

  it('keeps order and maps every file', () => {
    const out = toAttachmentPayload([
      { mime: 'image/png', data: '1' },
      { mime: 'application/pdf', data: '2' },
      { mime: 'image/webp', data: '3' },
    ]);
    expect(out.map(a => a.type)).toEqual(['image', 'pdf', 'image']);
    expect(out.map(a => a.data)).toEqual(['1', '2', '3']);
  });
});
