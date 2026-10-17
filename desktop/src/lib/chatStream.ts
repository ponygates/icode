// Chat transport: POST /api/chat, consume the SSE stream, and the small pure
// helpers that surround it (cost estimate, PCM→WAV encoding).
//
// Extracted from ChatPage.tsx so these rules can be unit-tested without
// mounting a 2,400-line component: SSE frames arrive split across arbitrary
// chunk boundaries, an empty stream must surface an error instead of spinning
// forever, and a user-initiated abort must not be reported as a timeout.

import i18n from '../i18n';
import type { Model } from '../stores/appStore';
import type { ChatEvent, ChatPayload, PlanInfo, UsageInfo } from './chatTypes';

/** Hard ceiling for one turn; the UI has no other way out of a hung socket. */
export const STREAM_TIMEOUT_MS = 120_000;

export interface StreamSessionEntry {
  ctrl: AbortController;
  /** Set by the stop button so the AbortError is reported as "stopped". */
  userStopped: boolean;
}

// Active streams keyed by session id — one AbortController per session so
// several tabs can generate in parallel and each can be stopped individually
// (the stop button / Esc only aborts the stream of the visible session).
export const sessionStreams = new Map<string, StreamSessionEntry>();

/**
 * abortSessionStream marks the session's stream as user-stopped and aborts it.
 * Returns true when there actually was an in-flight stream to abort, so the
 * caller can tell "stopped" from "nothing was running".
 */
export function abortSessionStream(sessionKey: string): boolean {
  const entry = sessionStreams.get(sessionKey);
  if (!entry) return false;
  entry.userStopped = true;
  entry.ctrl.abort();
  return true;
}

/**
 * streamChat — POST /api/chat, read the SSE stream, fire onEvent per message.
 * Includes a 120s timeout, an empty-stream guard, and AbortError handling.
 * sessionKey identifies the stream for stop/user-stop bookkeeping.
 */
export async function streamChat(
  baseUrl: string,
  payload: ChatPayload,
  onEvent: (e: ChatEvent) => void,
  sessionKey?: string,
): Promise<void> {
  const ctrl = new AbortController();
  if (sessionKey) sessionStreams.set(sessionKey, { ctrl, userStopped: false });
  const timer = setTimeout(() => ctrl.abort(), STREAM_TIMEOUT_MS);

  try {
    const res = await fetch(`${baseUrl}/api/chat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      signal: ctrl.signal,
    });

    if (!res.ok || !res.body) {
      const text = await res.text().catch(() => '');
      onEvent({ type: 'error', content: text || `HTTP ${res.status}` });
      return;
    }

    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    let fired = false;

    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const lines = buf.split('\n');
      buf = lines.pop() || '';
      for (const line of lines) {
        const s = line.trim();
        if (!s.startsWith('data:')) continue;
        const json = s.slice(5).trim();
        if (!json) continue;
        try { fired = true; onEvent(JSON.parse(json)); } catch { /* skip malformed frame */ }
      }
    }

    // A stream that closed without a single data frame would otherwise leave
    // the UI in "streaming" forever.
    if (!fired) {
      onEvent({ type: 'error', content: i18n.t('chat.noResponse') });
    }
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') {
      // User-initiated stop (Esc / stop button) aborts the fetch — show a
      // neutral "stopped" line instead of the misleading timeout message.
      const entry = sessionKey ? sessionStreams.get(sessionKey) : null;
      onEvent({
        type: 'error',
        content: entry?.userStopped ? i18n.t('chat.stopped') : i18n.t('chat.timeoutError'),
      });
    } else {
      onEvent({ type: 'error', content: e instanceof Error ? e.message : String(e) });
    }
  } finally {
    clearTimeout(timer);
    if (sessionKey) sessionStreams.delete(sessionKey);
  }
}

/**
 * estimateCost turns a usage block into the ¥ string shown next to the token
 * counters. Prefers the model's own token-plan pricing; otherwise falls back to
 * a conservative default rate rather than showing nothing.
 */
export function estimateCost(
  usage: UsageInfo | null | undefined,
  model: Model | null | undefined,
): string {
  if (!usage || (!usage.PromptTokens && !usage.prompt_tokens && !usage.TotalTokens && !usage.total_tokens))
    return '\xA50.00';
  const input = usage.PromptTokens || usage.prompt_tokens || 0;
  const output = usage.CompletionTokens || usage.completion_tokens || 0;
  // Use model's actual pricing if available
  const plans = (model?.plans as PlanInfo[] | undefined) ||
    (model as { Plans?: PlanInfo[] } | undefined)?.Plans ||
    [];
  const plan = plans.find((p) => p.type === 'token' || (p.name ? /token|coding/i.test(p.name) : false));
  if (plan?.cost) {
    const ip = plan.cost.input || 0;
    const op = plan.cost.output || 0;
    return '\xA5' + ((input * ip + output * op) / 1000).toFixed(4);
  }
  return '\xA5' + ((input * 0.00014 + output * 0.00028) / 1000).toFixed(4);
}

/**
 * encodeWav packs captured PCM float samples into a 16-bit mono RIFF/WAVE
 * blob. The ASR providers (Baidu / iFlytek / Zhipu) need real PCM/WAV —
 * Chromium's MediaRecorder can only emit webm, which Baidu rejects when merely
 * relabelled as .wav.
 */
export function encodeWav(chunks: Float32Array[], sampleRate: number): Blob {
  const total = chunks.reduce((n, c) => n + c.length, 0);
  const pcm = new ArrayBuffer(44 + total * 2);
  const view = new DataView(pcm);
  const w = (off: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(off + i, s.charCodeAt(i));
  };
  w(0, 'RIFF');
  view.setUint32(4, 36 + total * 2, true); // file size - 8
  w(8, 'WAVE');
  w(12, 'fmt ');
  view.setUint32(16, 16, true);        // fmt chunk size
  view.setUint16(20, 1, true);         // PCM
  view.setUint16(22, 1, true);         // mono
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true); // byte rate
  view.setUint16(32, 2, true);          // block align
  view.setUint16(34, 16, true);         // bits per sample
  w(36, 'data');
  view.setUint32(40, total * 2, true);
  let off = 44;
  for (const c of chunks) {
    for (let i = 0; i < c.length; i++, off += 2) {
      const s = Math.max(-1, Math.min(1, c[i]));
      view.setInt16(off, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    }
  }
  return new Blob([pcm], { type: 'audio/wav' });
}
