// Streaming SSE -> message accumulation reducer (pure, testable).
//
// ChatPage's `onEvent` handler grew a lot of subtle rules: fold buffered
// thinking into a collapsible block once real text arrives, de-duplicate the
// engine's `[Tool: name] <200-char summary>` wrapper when we already streamed
// the full live output via tool_progress, cap the thinking buffer, and settle
// the turn on done/error. Extracting the accumulation into a pure reducer
// keeps those rules in one place and unit-testable without a DOM or a network.
//
// The reducer owns only the *text assembly*; side effects it cannot perform
// (store writes, permission queue, token ticks) are returned as `effects` for
// the caller to run. `changedFiles`/turn-review lives in the caller because it
// depends on tool-target extraction that touches component helpers.

export interface StreamState {
  accumulated: string;
  thinkingBuf: string;
  toolHadProgress: boolean;
  settled: boolean;
}

export const initialStreamState = (): StreamState => ({
  accumulated: '',
  thinkingBuf: '',
  toolHadProgress: false,
  settled: false,
});

export type StreamEvent =
  | { type: 'text'; content?: string }
  | { type: 'thinking'; content?: string }
  | { type: 'system'; content?: string }
  | { type: 'tool_use'; name: string; argSummary?: string }
  | { type: 'tool_progress'; content?: string }
  | { type: 'permission' }
  | { type: 'plan_proposal' }
  | { type: 'done' }
  | { type: 'error'; content?: string };

export type StreamEffect =
  | { type: 'system-message'; content: string }
  | { type: 'permission' }
  | { type: 'plan-proposal' }
  | { type: 'settle'; content: string }
  | { type: 'error-settle'; content: string };

export interface ReduceResult {
  state: StreamState;
  // When true the caller should coalesce a store write of `state.accumulated`
  // into the next animation frame (the rAF flush in ChatPage).
  flush: boolean;
  effects: StreamEffect[];
}

const THINKING_CAP = 3000;

// Fold any pending thinking deltas into the transcript as a labelled block.
function foldThinking(accumulated: string, thinkingBuf: string): string {
  if (!thinkingBuf) return accumulated;
  return accumulated + '\n[Thinking]\n' + thinkingBuf + '\n';
}

export function applyStreamEvent(state: StreamState, event: StreamEvent): ReduceResult {
  // Once settled, late events (e.g. a straggler token) are dropped — mirrors
  // the `if (settled) return;` guard in the original handler.
  if (state.settled) return { state, flush: false, effects: [] };

  switch (event.type) {
    case 'thinking': {
      let thinkingBuf = state.thinkingBuf + (event.content || '');
      if (thinkingBuf.length > THINKING_CAP) thinkingBuf = thinkingBuf.slice(0, THINKING_CAP);
      return { state: { ...state, thinkingBuf }, flush: false, effects: [] };
    }

    case 'system':
      return {
        state,
        flush: false,
        effects: [{ type: 'system-message', content: event.content || '' }],
      };

    case 'plan_proposal':
      return { state, flush: false, effects: [{ type: 'plan-proposal' }] };

    case 'permission':
      return { state, flush: false, effects: [{ type: 'permission' }] };

    case 'text': {
      let accumulated = foldThinking(state.accumulated, state.thinkingBuf);
      const thinkingBuf = state.thinkingBuf ? '' : state.thinkingBuf;
      const contentStr = event.content || '';
      const toolWrap = accumulated.endsWith('\n') ? '' : '\n';
      const tm = contentStr.match(/^\[Tool: ([^\]]+)\](?:\n|$)/);
      if (tm) {
        // A `[Tool: …]` summary wrapper. Only append its body when we have NOT
        // already streamed the full live output for that tool (else it dupes).
        if (!state.toolHadProgress) accumulated += toolWrap + contentStr.slice(tm[0].length);
      } else {
        accumulated += contentStr;
      }
      return { state: { ...state, accumulated, thinkingBuf }, flush: true, effects: [] };
    }

    case 'tool_use': {
      const accumulated = state.accumulated + `\n[Tool: ${event.name}]${event.argSummary || ''}\n`;
      return {
        state: { ...state, accumulated, toolHadProgress: false },
        flush: true,
        effects: [],
      };
    }

    case 'tool_progress': {
      const accumulated = state.accumulated + (event.content || '');
      return {
        state: { ...state, accumulated, toolHadProgress: true },
        flush: true,
        effects: [],
      };
    }

    case 'done': {
      const accumulated = foldThinking(state.accumulated, state.thinkingBuf);
      return {
        state: { ...state, accumulated, thinkingBuf: '', settled: true },
        flush: false,
        effects: [{ type: 'settle', content: accumulated }],
      };
    }

    case 'error': {
      const accumulated = state.accumulated + '\n❌ ' + (event.content || '未知错误');
      return {
        state: { ...state, accumulated, settled: true },
        flush: false,
        effects: [{ type: 'error-settle', content: accumulated }],
      };
    }

    default:
      return { state, flush: false, effects: [] };
  }
}
