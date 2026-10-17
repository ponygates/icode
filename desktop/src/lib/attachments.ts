// Composer attachment helpers.
//
// The backend routes attachments by `type`: images go inline to every
// provider; PDFs become native document blocks on providers that support
// them and a visible "not readable" note elsewhere. Tagging is therefore a
// data-contract concern, not a rendering detail — keep it testable here.

export interface ComposerFile {
  mime: string;
  data: string;
  name?: string;
}

export interface AttachmentPayload {
  type: 'image' | 'pdf';
  mime: string;
  data: string;
  name?: string;
}

export function toAttachmentPayload(files: ComposerFile[]): AttachmentPayload[] {
  return files.map(f => ({
    type: f.mime === 'application/pdf' ? 'pdf' : 'image',
    mime: f.mime,
    data: f.data,
    ...(f.name ? { name: f.name } : {}),
  }));
}
