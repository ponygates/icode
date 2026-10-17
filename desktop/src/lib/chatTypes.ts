// Wire types shared by the chat transport (`lib/chatStream`) and the chat UI.
// Kept in lib/ rather than in the page so non-UI callers (transport, unit
// tests) don't have to import a .tsx module just to name a payload.

// A permission prompt surfaced from the engine's tool gate while a session is
// blocked waiting on the user's decision.
export interface PermissionRequest {
  request_id: string;
  tool: string;
  prompt?: string;
  sid?: string;
  // Risk level from the backend's permission.RiskSeverity: "high" (rm -rf,
  // git push --force) renders the dialog red, "low" (read-tier confirm)
  // calm accent, "medium"/unset keeps the familiar warning yellow.
  severity?: string;
  // Graded-auth escalation progress (Claude Code parity) surfaced by the
  // engine: consecutive ask/deny count and the threshold that forces manual.
  strikes?: number;
  threshold?: number;
}

// Token usage as reported by the backend (snake_case) or a provider (PascalCase).
export interface UsageInfo {
  prompt_tokens?: number;
  completion_tokens?: number;
  cache_hit_tokens?: number;
  PromptTokens?: number;
  CompletionTokens?: number;
  TotalTokens?: number;
  total_tokens?: number;
}

export interface CostParts { input: number; output: number; }

export interface PlanInfo {
  type?: string;
  name?: string;
  cost?: CostParts;
}

// A single server-sent event from /api/chat's SSE stream.
export type ChatEvent =
  | { type: 'text'; content: string }
  | { type: 'thinking'; content: string }
  | { type: 'system'; content: string }
  | { type: 'tool_use'; tool_call?: { name: string; arguments?: string }; ToolCall?: { Name: string; Arguments?: string } }
  | { type: 'tool_progress'; content: string }
  | { type: 'permission'; permission?: PermissionRequest; Permission?: PermissionRequest }
  | { type: 'plan_proposal' }
  | { type: 'mode_changed'; mode?: string }
  | { type: 'done'; meta?: { usage?: UsageInfo } }
  | { type: 'error'; content: string };

// Request body for POST /api/chat (and the slash-command re-entry path).
export interface ChatPayload {
  session_id: string;
  content: string;
  model: string;
  provider: string;
  attachments?: Array<{ type: string; mime: string; data: string; name?: string }>;
}
