// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import i18n from '../../i18n';
import {
  streamChat, abortSessionStream, sessionStreams, estimateCost, encodeWav,
  STREAM_TIMEOUT_MS,
} from '../chatStream';
import type { Model } from '../../stores/appStore';
import type { ChatEvent, ChatPayload } from '../chatTypes';

const payload: ChatPayload = {
  session_id: 's1', content: 'hi', model: 'm', provider: 'p',
};

const enc = new TextEncoder();

/** A Response whose body emits the given strings as separate chunks. */
function sseResponse(chunks: string[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      for (const ch of chunks) c.enqueue(enc.encode(ch));
      c.close();
    },
  });
  return new Response(body, { status: 200 });
}

/**
 * A Response whose stream never ends on its own; aborting the request signal
 * errors the stream the way a real fetch does. `first` is emitted immediately
 * so the caller can observe the stream is live before stopping it.
 */
function hangingResponse(signal: AbortSignal, first: string): Response {
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      c.enqueue(enc.encode(first));
      signal.addEventListener('abort', () => {
        c.error(new DOMException('Aborted', 'AbortError'));
      });
    },
  });
  return new Response(body, { status: 200 });
}

let events: ChatEvent[];
let onEvent: (e: ChatEvent) => void;

beforeEach(() => {
  events = [];
  onEvent = (e) => events.push(e);
  sessionStreams.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('streamChat — SSE framing', () => {
  it('reassembles a data frame split across two chunks', async () => {
    const frame = JSON.stringify({ type: 'text', content: '你好' });
    // Split mid-JSON: the decoder must keep the tail in its buffer.
    const cut = 12;
    vi.stubGlobal('fetch', vi.fn(() =>
      Promise.resolve(sseResponse([`data: ${frame.slice(0, cut)}`, `${frame.slice(cut)}\n\n`]))));

    await streamChat('http://x', payload, onEvent);

    expect(events).toEqual([{ type: 'text', content: '你好' }]);
  });

  it('emits several frames arriving in one chunk in order', async () => {
    const a = JSON.stringify({ type: 'text', content: 'a' });
    const b = JSON.stringify({ type: 'text', content: 'b' });
    vi.stubGlobal('fetch', vi.fn(() =>
      Promise.resolve(sseResponse([`data: ${a}\ndata: ${b}\n\n`]))));

    await streamChat('http://x', payload, onEvent);

    expect(events.map((e) => (e as { content: string }).content)).toEqual(['a', 'b']);
  });

  it('ignores comments, blank data lines and non-data lines', async () => {
    const ok = JSON.stringify({ type: 'text', content: 'ok' });
    vi.stubGlobal('fetch', vi.fn(() =>
      Promise.resolve(sseResponse([`: keepalive\n\nevent: ping\n` + `data:\n\ndata: ${ok}\n\n`]))));

    await streamChat('http://x', payload, onEvent);

    expect(events).toEqual([{ type: 'text', content: 'ok' }]);
  });

  it('treats a malformed frame as activity, not as an empty stream', async () => {
    // fired=true is set before JSON.parse — a broken payload must not be
    // reported as "no response", which would hide the real (protocol) problem.
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(sseResponse(['data: {oops\n\n']))));

    await streamChat('http://x', payload, onEvent);

    expect(events).toEqual([]);
  });
});

describe('streamChat — failure paths', () => {
  it('surfaces the response body on a non-2xx status', async () => {
    vi.stubGlobal('fetch', vi.fn(() =>
      Promise.resolve(new Response('quota exceeded', { status: 429 }))));

    await streamChat('http://x', payload, onEvent);

    expect(events).toEqual([{ type: 'error', content: 'quota exceeded' }]);
  });

  it('falls back to the status code when the error body is empty', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 502 }))));

    await streamChat('http://x', payload, onEvent);

    expect(events).toEqual([{ type: 'error', content: 'HTTP 502' }]);
  });

  it('reports an error when the stream closes without any data frame', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(sseResponse(['', '\n']))));

    await streamChat('http://x', payload, onEvent);

    expect(events).toHaveLength(1);
    expect(events[0].type).toBe('error');
    expect((events[0] as { content: string }).content).toBe(i18n.t('chat.noResponse'));
  });

  it('reports "stopped" (not "timeout") when the user aborts', async () => {
    vi.stubGlobal('fetch', vi.fn(((_u: string, init: RequestInit) =>
      Promise.resolve(hangingResponse(init.signal as AbortSignal,
        'data: {"type":"text","content":"a"}\n\n')))));

    const done = streamChat('http://x', payload, onEvent, 's1');
    await Promise.resolve();
    expect(abortSessionStream('s1')).toBe(true);
    await done;

    expect((events[events.length - 1] as { content: string }).content).toBe(i18n.t('chat.stopped'));
  });

  it('reports "timeout" when the 120s deadline aborts the request', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn(((_u: string, init: RequestInit) =>
      Promise.resolve(hangingResponse(init.signal as AbortSignal,
        'data: {"type":"text","content":"a"}\n\n')))));

    const done = streamChat('http://x', payload, onEvent, 's1');
    await vi.advanceTimersByTimeAsync(STREAM_TIMEOUT_MS + 1);
    await done;

    expect((events[events.length - 1] as { content: string }).content).toBe(i18n.t('chat.timeoutError'));
  });

  it('drops the session entry once the stream settles', async () => {
    vi.stubGlobal('fetch', vi.fn(() =>
      Promise.resolve(sseResponse(['data: {"type":"done"}\n\n']))));

    await streamChat('http://x', payload, onEvent, 's1');

    expect(sessionStreams.has('s1')).toBe(false);
    expect(abortSessionStream('s1')).toBe(false);
  });
});

function modelWithPlan(cost: { input: number; output: number }): Model {
  return { id: 'm', plans: [{ type: 'token', cost }] } as unknown as Model;
}

describe('estimateCost', () => {
  it('shows ¥0.00 when the usage block is empty', () => {
    expect(estimateCost(null, null)).toBe('\xA50.00');
    expect(estimateCost({}, null)).toBe('\xA50.00');
  });

  it('uses the model token plan when one is present', () => {
    const m = modelWithPlan({ input: 1, output: 2 });
    // (1000*1 + 1000*2) / 1000 = 3
    expect(estimateCost({ PromptTokens: 1000, CompletionTokens: 1000 }, m)).toBe('\xA53.0000');
  });

  it('accepts snake_case usage and falls back to the default rate', () => {
    // (1000*0.00014 + 1000*0.00028) / 1000 = 0.00042 -> 0.0004
    expect(estimateCost({ prompt_tokens: 1000, completion_tokens: 1000 }, null)).toBe('\xA50.0004');
  });
});

describe('encodeWav', () => {
  async function bytes(blob: Blob): Promise<DataView> {
    const buf = await new Promise<ArrayBuffer>((resolve, reject) => {
      const fr = new FileReader();
      fr.onload = () => resolve(fr.result as ArrayBuffer);
      fr.onerror = () => reject(fr.error);
      fr.readAsArrayBuffer(blob);
    });
    return new DataView(buf);
  }

  it('writes a 16-bit mono PCM header matching the sample count', async () => {
    const blob = encodeWav([new Float32Array([0.5, -0.5]), new Float32Array([0, 0])], 16000);
    expect(blob.type).toBe('audio/wav');
    expect(blob.size).toBe(44 + 4 * 2);

    const dv = await bytes(blob);
    const tag = (o: number) => String.fromCharCode(
      dv.getUint8(o), dv.getUint8(o + 1), dv.getUint8(o + 2), dv.getUint8(o + 3));
    expect(tag(0)).toBe('RIFF');
    expect(tag(8)).toBe('WAVE');
    expect(tag(12)).toBe('fmt ');
    expect(tag(36)).toBe('data');
    expect(dv.getUint32(4, true)).toBe(36 + 4 * 2); // RIFF size
    expect(dv.getUint16(20, true)).toBe(1);         // PCM
    expect(dv.getUint16(22, true)).toBe(1);         // mono
    expect(dv.getUint32(24, true)).toBe(16000);     // sample rate
    expect(dv.getUint32(28, true)).toBe(32000);     // byte rate
    expect(dv.getUint16(34, true)).toBe(16);        // bits per sample
    expect(dv.getUint32(40, true)).toBe(4 * 2);     // data size
  });

  it('clamps out-of-range samples instead of wrapping around', async () => {
    const dv = await bytes(encodeWav([new Float32Array([2, -2])], 16000));
    expect(dv.getInt16(44, true)).toBe(0x7fff);
    expect(dv.getInt16(46, true)).toBe(-0x8000);
  });
});
