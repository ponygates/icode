// Tool-call permission approval state transitions (pure, testable).
//
// The engine pauses server-side and pushes a permission request; the desktop
// keeps a per-session queue and the modal shows the head. Answering pops the
// head; finishing/stopping a session drops every queued request for it; and
// the "allow this tool for the whole session" button must both remember the
// tool AND let the current request through. These are exactly the transitions
// that were tangled inside ChatPage's handlers — pulled out here so the
// accept/deny/session-allow semantics can be asserted without rendering.

export type Decision = 'allow' | 'deny' | 'allow_all';

export interface PermEntry {
  request_id: string;
  tool: string;
  sid: string;
}

// A backend step the caller must perform to commit a decision.
export type ApprovalEffect =
  | { kind: 'respond'; requestId: string; decision: Decision }
  | { kind: 'allow-tool'; sessionId: string; tool: string };

// Pop the head request from the queue (the modal always answers queue[0]).
// Returns the resolved entry (null when nothing is pending) and the remainder.
export function popHead<T>(queue: T[]): { head: T | null; rest: T[] } {
  if (queue.length === 0) return { head: null, rest: [] };
  return { head: queue[0], rest: queue.slice(1) };
}

// Drop every queued request belonging to a session (used on done/error/stop).
export function dropSession<T extends { sid: string }>(queue: T[], sid: string): T[] {
  return queue.filter((p) => p.sid !== sid);
}

// Translate a user click into the ordered backend steps.
//   'allow'          -> just let this one request through.
//   'deny'           -> reject it (engine keeps asking / aborts).
//   'allow_all'      -> approve and switch the session to auto (single call).
//   alwaysForSession -> remember the tool for the session, THEN approve; the
//                       remember step must run first so a fast follow-up tool
//                       call is already covered.
export function planApproval(
  entry: PermEntry,
  decision: Decision,
  opts?: { alwaysForSession?: boolean }
): ApprovalEffect[] {
  const steps: ApprovalEffect[] = [];
  if (opts?.alwaysForSession) {
    steps.push({ kind: 'allow-tool', sessionId: entry.sid, tool: entry.tool });
  }
  steps.push({ kind: 'respond', requestId: entry.request_id, decision });
  return steps;
}

// Additive session-allow set transition — a fresh Set so callers relying on
// referential equality (memoized selectors) see the change.
export function withSessionAllow(allow: Set<string>, tool: string): Set<string> {
  if (allow.has(tool)) return allow;
  const next = new Set(allow);
  next.add(tool);
  return next;
}
