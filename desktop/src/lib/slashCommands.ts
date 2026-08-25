// Slash command system for the desktop chat page — parity with the TUI's
// 49-command dispatcher (internal/tui/slash_commands.go), scoped to the
// commands that make sense in a GUI. Client-side commands run against the
// Zustand store / REST API directly; commands that need the agent loop
// (/compact, /diff, /review) are REWRITTEN into natural-language instructions
// for the engine, because the HTTP chat path has no slash layer (the engine
// would otherwise receive the literal text "/compact").
//
// Pure module: no React. Everything goes through useAppStore.getState() and
// i18n, so it is unit-testable without a DOM.
import i18n from '../i18n';
import { useAppStore } from '../stores/appStore';

export type SlashOutcome =
  | { type: 'handled' }                 // command ran locally; nothing to send
  | { type: 'rewrite'; text: string }   // send this instruction to the engine instead
  | { type: 'passthrough' };            // not a known command — send as-is

export interface SlashCommand {
  name: string;          // without the leading slash
  aliases?: string[];
  usage?: string;        // arg hint shown in the autocomplete dropdown
  descKey: string;       // i18n key under slash.*
  run: (args: string) => Promise<SlashOutcome> | SlashOutcome;
}

const handled: SlashOutcome = { type: 'handled' };

function sysMsg(content: string) {
  const { activeSessionId, addMessage } = useAppStore.getState();
  if (!activeSessionId) return;
  addMessage(activeSessionId, {
    id: Date.now().toString(36) + Math.random().toString(36).slice(2, 6),
    role: 'system',
    content,
    timestamp: Date.now(),
  });
}

// Loose shapes for backend API responses used by slash commands.
interface StatusResponse { version?: string; git_branch?: string; }
interface HealthResponse { ok?: boolean; }
interface AnalyticsResponse {
  tokens_saved?: number;
  cache_hit_tokens?: number;
  cache_hit_rate?: number;
  estimated_cost?: number;
  estimated_saved_cost?: number;
}
interface SkillInfo { name: string; enabled?: boolean; description?: string; }
interface SkillsResponse { ok?: boolean; skills?: SkillInfo[]; }
interface McpServerInfo { name: string; status?: string; trust_mode?: string; }
interface TeamInfo { name: string; description?: string; }
interface TeamsResponse { ok?: boolean; teams?: TeamInfo[]; }

async function getJSON<T>(path: string): Promise<T | null> {
  const { backendUrl } = useAppStore.getState();
  if (!backendUrl) return null;
  try {
    const res = await fetch(`${backendUrl}${path}`, { cache: 'no-cache' });
    if (!res.ok) return null;
    return (await res.json()) as T;
  } catch {
    return null;
  }
}

const fmtNum = (n: unknown) => (typeof n === 'number' ? n.toLocaleString() : '0');
const fmtCost = (n: unknown) => (typeof n === 'number' ? `¥${n.toFixed(4)}` : '¥0.00');

export const slashCommands: SlashCommand[] = [
  {
    name: 'clear',
    descKey: 'slash.descClear',
    run: () => {
      const { activeSessionId, clearMessages } = useAppStore.getState();
      if (activeSessionId) clearMessages(activeSessionId);
      return handled;
    },
  },
  {
    name: 'new',
    descKey: 'slash.descNew',
    run: () => {
      const st = useAppStore.getState();
      const m = st.models.find((x) => x.id === st.selectedModel);
      st.createSession(st.selectedModel, m?.provider || 'openrouter');
      return handled;
    },
  },
  {
    name: 'model',
    usage: '<id|#>',
    descKey: 'slash.descModel',
    run: (args) => {
      const st = useAppStore.getState();
      const arg = args.trim();
      if (!arg) {
        const lines = st.models.map(
          (m, i) => `${m.id === st.selectedModel ? '▶' : '  '} ${i + 1}. ${m.name || m.id}  (${m.provider})`
        );
        sysMsg(`**${i18n.t('slash.modelListTitle')}**\n\n${lines.join('\n')}\n\n_${i18n.t('slash.modelListHint')}_`);
        return handled;
      }
      // Numeric (1-based) or exact id — mirrors the TUI /model picker.
      let target = st.models.find((m) => m.id === arg);
      const n = parseInt(arg, 10);
      if (!target && !isNaN(n) && n >= 1 && n <= st.models.length) {
        target = st.models[n - 1];
      }
      if (!target) {
        sysMsg(`❌ ${i18n.t('slash.modelNotFound', { id: arg })}`);
        return handled;
      }
      st.setSelectedModel(target.id);
      sysMsg(`✅ Model → ${target.name || target.id}`);
      return handled;
    },
  },
  {
    name: 'plan',
    descKey: 'slash.descPlan',
    run: () => {
      const st = useAppStore.getState();
      st.setMode('plan');
      sysMsg(`🧭 Mode → plan`);
      return handled;
    },
  },
  {
    name: 'ask',
    descKey: 'slash.descAsk',
    run: () => {
      const st = useAppStore.getState();
      st.setMode('ask');
      sysMsg(`❓ Mode → ask`);
      return handled;
    },
  },
  {
    name: 'debug',
    descKey: 'slash.descDebug',
    run: () => {
      const st = useAppStore.getState();
      st.setMode('agent');
      sysMsg(`🐞 Mode → agent`);
      return handled;
    },
  },
  {
    name: 'mode',
    usage: '<agent|plan|yolo|ask>',
    descKey: 'slash.descMode',
    run: (args) => {
      const valid = ['plan', 'agent', 'yolo', 'ask', 'auto'];
      const arg = args.trim().toLowerCase();
      if (!arg) {
        const st = useAppStore.getState();
        sysMsg(`🎛️ 当前模式: ${st.mode}\n\n用法: /mode <${valid.join('|')}>`);
        return handled;
      }
      if (!valid.includes(arg)) {
        sysMsg(`❌ 无效模式: ${arg}\n用法: /mode <${valid.join('|')}>`);
        return handled;
      }
      const st = useAppStore.getState();
      st.setMode(arg);
      const label = { plan: '🧭', agent: '🤖', yolo: '🚀', ask: '❓', auto: '⚡' }[arg] || '•';
      sysMsg(`${label} Mode → ${arg}`);
      return handled;
    },
  },
  {
    name: 'lang',
    usage: '<zh-CN|zh-TW|en>',
    descKey: 'slash.descLang',
    run: (args) => {
      const langs = ['zh-CN', 'zh-TW', 'en'];
      const arg = args.trim();
      if (!arg) {
        const st = useAppStore.getState();
        sysMsg(`🌐 当前语言: ${st.language}\n用法: /lang <${langs.join('|')}>`);
        return handled;
      }
      if (!langs.includes(arg)) {
        sysMsg(`❌ 无效语言: ${arg}\n用法: /lang <${langs.join('|')}>`);
        return handled;
      }
      const st = useAppStore.getState();
      st.setLanguage(arg);
      sysMsg(`🌐 语言已切换为 ${arg}`);
      return handled;
    },
  },
  {
    name: 'security',
    usage: '<local|desensitize|local-llm|foreign-llm|unrestricted>',
    descKey: 'slash.descSecurity',
    run: (args) => {
      const valid = ['local', 'desensitize', 'local-llm', 'foreign-llm', 'unrestricted'];
      const arg = args.trim();
      const st = useAppStore.getState();
      if (!arg) {
        sysMsg(`🛡️ 当前安全等级: ${st.securityLevel}\n用法: /security <${valid.join('|')}>`);
        return handled;
      }
      if (!valid.includes(arg)) {
        sysMsg(`❌ 无效安全等级: ${arg}\n用法: /security <${valid.join('|')}>`);
        return handled;
      }
      st.setSecurityLevel(arg);
      sysMsg(`🛡️ 安全等级已设为 ${arg}`);
      return handled;
    },
  },
  {
    name: 'update',
    descKey: 'slash.descUpdate',
    run: async () => {
      const st = useAppStore.getState();
      await st.refreshModels();
      sysMsg(`🔄 模型目录已刷新`);
      return handled;
    },
  },
  {
    name: 'goal',
    usage: '<set|show|clear> [文本]',
    descKey: 'slash.descGoal',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'budget',
    usage: '<set|show|warn|clear> [值]',
    descKey: 'slash.descBudget',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'todo',
    descKey: 'slash.descTodo',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'memory',
    usage: '<edit|prefs|list|forget|clear>',
    descKey: 'slash.descMemory',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'resume',
    usage: '<session-id> [--lite[=<n>]]',
    descKey: 'slash.descResume',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'sessions',
    aliases: ['session'],
    descKey: 'slash.descSessions',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'add-dir',
    usage: '<dir>',
    descKey: 'slash.descAddDir',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'undo',
    aliases: ['rewind'],
    usage: '[N]',
    descKey: 'slash.descUndo',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'wipe',
    descKey: 'slash.descWipe',
    run: () => {
      const st = useAppStore.getState();
      if (st.activeSessionId) st.clearMessages(st.activeSessionId);
      sysMsg('🗑️ 对话已清空并重置。');
      return handled;
    },
  },
  {
    name: 'share',
    usage: '[filename]',
    descKey: 'slash.descShare',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'context',
    descKey: 'slash.descContext',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'output-style',
    usage: '<concise|normal|verbose>',
    descKey: 'slash.descOutputStyle',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'hooks',
    descKey: 'slash.descHooks',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'keys',
    descKey: 'slash.descKeys',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'models',
    usage: '[add|rm] [参数]',
    descKey: 'slash.descModels',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'config',
    usage: 'set <key> <value>',
    descKey: 'slash.descConfig',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'agents',
    descKey: 'slash.descAgents',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'init',
    descKey: 'slash.descInit',
    run: () => ({ type: 'passthrough' as const }),
  },
  {
    name: 'admin',
    usage: '[off]',
    descKey: 'slash.descAdmin',
    run: () => ({ type: 'passthrough' as const }),
  },
  // Backend-implemented commands — pass through so the server's /api/slash
// layer (internal/core/slashui, same vocabulary as the TUI) runs them. These
// entries only exist so they show up in the autocomplete dropdown.
  { name: 'cd', usage: '<dir>', descKey: 'slash.descCd', run: () => ({ type: 'passthrough' as const }) },
  {
    name: 'rename',
    usage: '<title>',
    descKey: 'slash.descRename',
    run: (args) => {
      const title = args.trim();
      if (!title) {
        sysMsg(i18n.t('slash.renameHint'));
        return handled;
      }
      const st = useAppStore.getState();
      if (st.activeSessionId) st.renameSession(st.activeSessionId, title);
      sysMsg(`✏️ ${title}`);
      return handled;
    },
  },
  { name: 'copy', usage: '[file]', descKey: 'slash.descCopy', run: () => ({ type: 'passthrough' as const }) },
  { name: 'branch', descKey: 'slash.descBranch', run: () => ({ type: 'passthrough' as const }) },
  { name: 'checkpoint', descKey: 'slash.descCheckpoint', run: () => ({ type: 'passthrough' as const }) },
  {
    name: 'compact',
    usage: '[instructions]',
    descKey: 'slash.descCompact',
    run: (args) => ({
      type: 'rewrite',
      text: args.trim()
        ? i18n.t('slash.rewriteCompactCustom', { instructions: args.trim() })
        : i18n.t('slash.rewriteCompact'),
    }),
  },
  {
    name: 'summarize',
    descKey: 'slash.descSummarize',
    run: () => ({ type: 'rewrite', text: i18n.t('slash.rewriteSummarize') }),
  },
  {
    name: 'diff',
    descKey: 'slash.descDiff',
    run: () => ({ type: 'rewrite', text: i18n.t('slash.rewriteDiff') }),
  },
  {
    name: 'review',
    usage: '[file]',
    descKey: 'slash.descReview',
    run: (args) => ({
      type: 'rewrite',
      text: args.trim()
        ? i18n.t('slash.rewriteReviewFile', { file: args.trim() })
        : i18n.t('slash.rewriteReview'),
    }),
  },
  {
    name: 'cost',
    aliases: ['token', 'usage', 'stats'],
    descKey: 'slash.descCost',
    run: async () => {
      const { activeSessionId } = useAppStore.getState();
      const data = activeSessionId ? await getJSON<AnalyticsResponse>(`/api/analytics/${activeSessionId}`) : null;
      if (!data) {
        sysMsg(i18n.t('slash.noStats'));
        return handled;
      }
      sysMsg(
        `**${i18n.t('slash.statsTitle')}**\n\n` +
          `🪙 ${i18n.t('slash.statsSaved')}: ${fmtNum(data.tokens_saved)}\n` +
          `⚡ ${i18n.t('slash.statsCacheHit')}: ${fmtNum(data.cache_hit_tokens)} (${((data.cache_hit_rate || 0) * 100).toFixed(1)}%)\n` +
          `💰 ${i18n.t('slash.statsCost')}: ${fmtCost(data.estimated_cost)}\n` +
          `✅ ${i18n.t('slash.statsSavedCost')}: ${fmtCost(data.estimated_saved_cost)}`
      );
      return handled;
    },
  },
  {
    name: 'status',
    descKey: 'slash.descStatus',
    run: async () => {
      const data = await getJSON<StatusResponse>('/api/status');
      if (!data) {
        sysMsg(i18n.t('slash.backendOffline'));
        return handled;
      }
      const st = useAppStore.getState();
      sysMsg(
        `**${i18n.t('slash.statusTitle')}**\n\n` +
          `• ${i18n.t('slash.statusVersion')}: ${data.version || st.backendVersion || '?'}\n` +
          `• ${i18n.t('slash.statusModel')}: ${st.selectedModel}\n` +
          `• ${i18n.t('slash.statusBranch')}: ${data.git_branch || '-'}\n` +
          `• ${i18n.t('slash.statusMode')}: ${st.mode}\n` +
          `• ${i18n.t('slash.statusSecurity')}: ${st.securityLevel}`
      );
      return handled;
    },
  },
  {
    name: 'doctor',
    descKey: 'slash.descDoctor',
    run: async () => {
      const health = await getJSON<HealthResponse>('/api/health');
      const status = await getJSON<StatusResponse>('/api/status');
      const st = useAppStore.getState();
      const lines = [
        `${health ? '✅' : '❌'} backend ${health ? 'OK' : 'unreachable'}`,
        `${st.backendUrl ? '✅' : '❌'} url: ${st.backendUrl || '-'}`,
        `${st.models.length > 0 ? '✅' : '⚠️'} models: ${st.models.length}`,
        `${status?.git_branch ? '✅' : '⚠️'} git: ${status?.git_branch || 'n/a'}`,
      ];
      sysMsg(`**${i18n.t('slash.doctorTitle')}**\n\n${lines.join('\n')}`);
      return handled;
    },
  },
  {
    name: 'skills',
    descKey: 'slash.descSkills',
    run: async () => {
      const data = await getJSON<SkillsResponse>('/api/skills');
      const list: SkillInfo[] = data?.skills || [];
      if (!Array.isArray(list) || list.length === 0) {
        sysMsg(i18n.t('slash.noSkills'));
        return handled;
      }
      const lines = list.slice(0, 30).map((s) => `• ${s.enabled === false ? '○' : '●'} **${s.name}** — ${s.description || ''}`);
      sysMsg(`**${i18n.t('slash.skillsTitle')}** (${list.length})\n\n${lines.join('\n')}`);
      return handled;
    },
  },
  {
    name: 'mcp',
    descKey: 'slash.descMcp',
    run: async () => {
      const data = await getJSON<McpServerInfo[]>('/api/mcp');
      const list = data || [];
      if (!Array.isArray(list) || list.length === 0) {
        sysMsg(i18n.t('slash.noMcp'));
        return handled;
      }
      const lines = list.map((s) => `• **${s.name}** — ${s.status || s.trust_mode || ''}`);
      sysMsg(`**${i18n.t('slash.mcpTitle')}** (${list.length})\n\n${lines.join('\n')}`);
      return handled;
    },
  },
  {
    name: 'teams',
    descKey: 'slash.descTeams',
    run: async () => {
      const data = await getJSON<TeamsResponse>('/api/teams');
      const list: TeamInfo[] = data?.teams || [];
      if (!Array.isArray(list) || list.length === 0) {
        sysMsg(i18n.t('slash.noTeams'));
        return handled;
      }
      const lines = list.map((tm) => `• **${tm.name}** — ${tm.description || ''}`);
      sysMsg(`**${i18n.t('slash.teamsTitle')}** (${list.length})\n\n${lines.join('\n')}`);
      return handled;
    },
  },
  {
    name: 'theme',
    usage: '[dark|light]',
    descKey: 'slash.descTheme',
    run: (args) => {
      const arg = args.trim();
      const cur = document.documentElement.getAttribute('data-theme') || 'dark';
      const next = arg === 'dark' || arg === 'light' ? arg : cur === 'dark' ? 'light' : 'dark';
      document.documentElement.setAttribute('data-theme', next);
      try { localStorage.setItem('icode.theme', next); } catch {}
      sysMsg(`🎨 Theme → ${next}`);
      return handled;
    },
  },
  {
    name: 'export',
    descKey: 'slash.descExport',
    run: () => {
      exportActiveSession();
      return handled;
    },
  },
  {
    name: 'version',
    descKey: 'slash.descVersion',
    run: () => {
      const st = useAppStore.getState();
      sysMsg(`iCode ${st.backendVersion || ''}`.trim() || 'iCode');
      return handled;
    },
  },
  {
    name: 'help',
    descKey: 'slash.descHelp',
    run: () => {
      const lines = slashCommands.map((c) => {
        const aliases = c.aliases?.length ? ` (${c.aliases.map((a) => '/' + a).join(', ')})` : '';
        const usage = c.usage ? ` ${c.usage}` : '';
        return `• **/${c.name}**${usage}${aliases} — ${i18n.t(c.descKey)}`;
      });
      sysMsg(`**${i18n.t('slash.helpTitle')}**\n\n${lines.join('\n')}\n\n_${i18n.t('slash.helpHint')}_`);
      return handled;
    },
  },
];

// exportActiveSession downloads the active session as Markdown. Shared by the
// /export command and (via re-export) the header export button.
export function exportActiveSession() {
  const st = useAppStore.getState();
  const sess = st.sessions.find((s) => s.id === st.activeSessionId);
  if (!sess || !sess.messages.length) return;
  const md = sess.messages
    .map((m) => `### ${m.role === 'user' ? '👤' : m.role === 'system' ? 'ℹ️' : '🤖 iCode'}\n\n${m.content}\n`)
    .join('\n---\n\n');
  const blob = new Blob([md], { type: 'text/markdown' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `icode-${sess.title || 'session'}.md`;
  a.click();
  URL.revokeObjectURL(url);
}

// fuzzyScore scores name against a typed query using Claude Code-style
// subsequence matching: contiguous prefix ranks best (0), in-order
// subsequences are penalised by the gap since the previous matched rune.
// Returns -1 when the query cannot be embedded in order. Mirrors the TUI's
// Go implementation so both ends rank identically.
export function fuzzyScore(query: string, name: string): number {
  if (!query) return 0;
  const q = query.toLowerCase();
  const n = name.toLowerCase();
  if (n.startsWith(q)) return 0;
  let score = 0;
  let qi = 0;
  let last = 0;
  for (let ni = 0; ni < n.length && qi < q.length; ni++) {
    if (n[ni] !== q[qi]) continue;
    if (qi > 0) score -= ni - last - 1; // relative gap penalty
    last = ni;
    qi++;
  }
  return qi < q.length ? -1 : score;
}

const recentSlashKey = 'icode.recentSlash';

// noteRecentSlash records a dispatched command (most recent first, max 8)
// in localStorage so the dropdown ranks frequently-used commands on top.
export function noteRecentSlash(name: string) {
  try {
    const prev: string[] = JSON.parse(localStorage.getItem(recentSlashKey) || '[]');
    const next = [name, ...prev.filter((n) => n !== name)].slice(0, 8);
    localStorage.setItem(recentSlashKey, JSON.stringify(next));
  } catch {
    /* storage unavailable — recency is best-effort */
  }
}

function recentSlashes(): string[] {
  try {
    return JSON.parse(localStorage.getItem(recentSlashKey) || '[]');
  } catch {
    return [];
  }
}

// filterSlash returns commands matching the current input buffer (which must
// start with '/'). Matching is fuzzy on name or alias — prefix matches rank
// first, then contiguous subsequences, then gappy ones; recently used
// commands jump to the top within their score band (Claude Code parity).
export function filterSlash(input: string): SlashCommand[] {
  if (!input.startsWith('/')) return [];
  if (input.includes(' ')) return []; // args already entered — no dropdown
  const partial = input.slice(1).split(/\s/)[0].toLowerCase();
  const recent = recentSlashes();
  const recency = new Map(recent.map((n, i) => [n, recent.length - i]));
  const scored = slashCommands
    .map((c) => {
      let s = fuzzyScore(partial, c.name);
      if (s < 0 && c.aliases) {
        for (const a of c.aliases) {
          s = Math.max(s, fuzzyScore(partial, a));
        }
      }
      return { c, s };
    })
    .filter((x) => x.s >= 0);
  // Sort: score ascending (best first), then recency descending, stable.
  const withIdx = scored.map((x, i) => ({ ...x, i }));
  withIdx.sort((a, b) => {
    if (a.s !== b.s) return a.s - b.s;
    const ra = recency.get(a.c.name) ?? 0;
    const rb = recency.get(b.c.name) ?? 0;
    if (ra !== rb) return rb - ra;
    return a.i - b.i;
  });
  return withIdx.map((x) => x.c);
}

// executeSlash parses and runs a slash command. Unknown commands return
// 'passthrough' so the caller forwards the raw text to the engine (same as
// before this feature existed). Dispatched commands are recorded for
// recency-ranked autocomplete.
export async function executeSlash(input: string): Promise<SlashOutcome> {
  const trimmed = input.trim();
  if (!trimmed.startsWith('/')) return { type: 'passthrough' };
  const sp = trimmed.indexOf(' ');
  const name = (sp < 0 ? trimmed.slice(1) : trimmed.slice(1, sp)).toLowerCase();
  const args = sp < 0 ? '' : trimmed.slice(sp + 1);
  const cmd = slashCommands.find((c) => c.name === name || (c.aliases || []).includes(name));
  if (!cmd) return { type: 'passthrough' };
  noteRecentSlash('/' + cmd.name);
  return cmd.run(args);
}
