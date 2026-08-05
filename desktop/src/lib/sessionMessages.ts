// Session-message persistence via the backend REST API.
//
// The desktop UI has several local-only helpers (# memory, ! shell, fork,
// clear) that mutate the zustand session state in memory. To keep history in
// sync across the CLI, simpleui, and desktop (which share the same SQLite
// DB), every such mutation is also flushed through the message-level API.
// These thin wrappers centralize that contract so the calls are testable and
// the pages stay readable. fetch is injectable for tests.

export type Message = {
  id: string;
  role: string;
  content: string;
  timestamp?: number;
};

// Append a message to a session (POST). Missing id is assigned server-side.
export function apiAppendMessage(
  base: string,
  sessionId: string,
  msg: Message,
  fetchImpl: typeof fetch = fetch,
): Promise<Response> {
  return fetchImpl(`${base}/api/sessions/${sessionId}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: msg.id, role: msg.role, content: msg.content }),
  });
}

// Update a message in place (PUT /messages/{id}). Used when a shell
// placeholder is replaced by its real output after the command runs.
export function apiUpdateMessage(
  base: string,
  sessionId: string,
  msgId: string,
  role: string,
  content: string,
  fetchImpl: typeof fetch = fetch,
): Promise<Response> {
  return fetchImpl(`${base}/api/sessions/${sessionId}/messages/${msgId}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ role, content }),
  });
}

// Copy every message of a source session into a fork session (POST each).
export async function apiForkMessages(
  base: string,
  newSessionId: string,
  messages: Message[],
  fetchImpl: typeof fetch = fetch,
): Promise<Response[]> {
  const out: Promise<Response>[] = [];
  for (const m of messages) {
    const copy: Message = { ...m, id: Math.random().toString(36).slice(2) };
    out.push(apiAppendMessage(base, newSessionId, copy, fetchImpl));
  }
  return Promise.all(out).catch(() => out as unknown as Response[]);
}

// Wipe a session's messages server-side so a cleared chat does not resurrect
// in the CLI/simpleui.
export function apiClearSession(
  base: string,
  sessionId: string,
  fetchImpl: typeof fetch = fetch,
): Promise<Response> {
  return fetchImpl(`${base}/api/sessions/${sessionId}/clear`, { method: 'POST' });
}