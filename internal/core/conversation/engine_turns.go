package conversation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/llm/tokenopt"
	"github.com/ponygates/icode/internal/types"
)

// longSessionThreshold is how many turns (user+assistant messages) trigger the
// one-time /compact nudge in a session with no active compression. The nudge
// is the FALLBACK path: with auto-compact on (the default) longSessionHint
// stays silent because the engine already compacts at the token threshold.
const longSessionThreshold = 40

// longSessionHint returns a one-time, non-blocking nudge suggesting /compact
// when a session has grown long WITHOUT any compression active (no /budget,
// no --lite). It fires once per session so it never nags on every turn.
func (e *Engine) longSessionHint(sess *types.Session) string {
	if sess == nil || len(sess.Messages) < longSessionThreshold {
		return ""
	}
	// With auto-compact enabled the engine already distills history at the
	// token threshold, so the manual /compact nudge would be pure noise.
	if e.autoCompactPct > 0 {
		return ""
	}
	if sessionum.BudgetMax(sess) > 0 || sessionum.LiteN(sess) > 0 {
		return ""
	}
	e.hintMu.Lock()
	if e.compactHinted[sess.ID] {
		e.hintMu.Unlock()
		return ""
	}
	e.compactHinted[sess.ID] = true
	e.hintMu.Unlock()

	turns := 0
	for _, m := range sess.Messages {
		if m.Role == types.RoleUser || m.Role == types.RoleAssistant {
			turns++
		}
	}
	return fmt.Sprintf("ⓘ [省 token] 本会话已有 %d 条消息。上下文较长时运行 /compact 压缩历史（只把「语义摘要 + 最近消息」喂给模型），旧会话用 /resume --compact 同理。", turns)
}

// truncateForHook caps tool output passed to hook processes at 32KB so huge
// outputs don't blow up the hook's stdin pipe.
func truncateForHook(s string) string {
	const max = 32 * 1024
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}

// snapshotBeforeTool creates a checkpoint + file-level undo before mutating
// tool calls. The checkpoint stores session-level rollback data; the undo
// snapshot stores actual file content for the /undo command.
func (e *Engine) snapshotBeforeTool(ctx context.Context, tc types.ToolCall) {
	mutating := map[string]bool{"write_file": true, "edit": true, "bash": true}
	if !mutating[tc.Name] {
		return
	}
	sessionID := tool.SessionIDFromContext(ctx)
	if sessionID == "" {
		return
	}
	// File-level undo snapshot (OpenCode parity)
	var filePath string
	if m := parseToolArgs(tc.Arguments); m != nil {
		if p, ok := m["path"]; ok {
			filePath, _ = p.(string)
		} else if p, ok := m["file_path"]; ok {
			filePath, _ = p.(string)
		}
	}
	checkpoint.BeforeTool(ctx, tc.Name, filePath)
	store, err := checkpoint.GetOrOpen(sessionID)
	if err != nil {
		discardOnce("open checkpoint store", err)
		return // checkpoints stay best-effort
	}
	_, snapErr := store.Snapshot(ctx, "before "+tc.Name)
	discardOnce("checkpoint snapshot", snapErr)
}

// stashIfPivot inspects the session before a new user message and, if the
// user is continuing a DIFFERENT task (a pivot) rather than answering the
// current one, snapshots a checkpoint and refreshes the archived summary so
// the interrupted task stays recoverable. Best-effort: failures are ignored.
func (e *Engine) stashIfPivot(ctx context.Context, sess *types.Session, newContent string) {
	if e.sessionSt == nil || sess == nil {
		return
	}

	// "In progress" = we have at least one prior user turn AND some tool
	// activity after it. Without both there is nothing worth stashing.
	lastUser := ""
	hasToolActivity := false
	for _, m := range sess.Messages {
		switch m.Role {
		case types.RoleUser:
			if t := strings.TrimSpace(m.Content); t != "" {
				lastUser = t
			}
		case types.RoleTool:
			hasToolActivity = true
		}
	}
	newContent = strings.TrimSpace(newContent)
	if lastUser == "" || newContent == "" || !hasToolActivity {
		return
	}
	// Is this actually a pivot? Two complementary signals:
	//  1. an explicit new-goal opener ("另外…", "顺便…", "新任务:"), or
	//  2. a topic switch — the new request shares almost no vocabulary with
	//     the last user request, so it is very likely a different task rather
	//     than a continuation ("帮我修个 bug" right after "写个报告").
	// Both are cheap and deterministic; no LLM call.
	if !isNewTaskPrompt(newContent) && !diverges(newContent, lastUser) {
		return
	}

	// Snapshot a checkpoint labeled with the *interrupted* task so /rewind can
	// return here. Best-effort: an empty shadow repo yields no commit, which
	// is fine — the summary note below is the authoritative stash record.
	store, err := checkpoint.GetOrOpen(sess.ID)
	if err == nil {
		_, snapErr := store.Snapshot(ctx, "stash before: "+truncStashMsg(lastUser))
		discardOnce("stash snapshot", snapErr)
	} else {
		discardOnce("open checkpoint store", err)
	}
	// Refresh the archived summary so the resume layer sees the interrupted
	// work at a glance.
	summary := sessionum.Get(sess)
	if g := sessionum.Generate(sess, sess.ModelID, sess.ProviderName, ""); g != "" {
		summary = g
	}
	if strings.TrimSpace(summary) != "" {
		summary += fmt.Sprintf("\n\n(stash) 上个任务未完成: %s — 检查点已保存，可 /rewind 恢复。", truncStashMsg(lastUser))
		discard("save session summary", sessionum.Save(e.sessionSt, sess, summary))
	}
}

// diverges reports whether newMsg is a different task than lastMsg by the
// share of significant tokens they have in common. Short follow-ups like
// "继续", "还有", "另外补一句" share little vocabulary but are continuations,
// so we require the new message to be long enough to be a real request and
// to share essentially no content words.
func diverges(newMsg, lastMsg string) bool {
	na := tokenizeForPivot(newMsg)
	la := tokenizeForPivot(lastMsg)
	if len(na) < 4 || len(la) < 4 {
		// Too short to judge by vocabulary — fall back to opener detection.
		return false
	}
	laSet := make(map[string]bool, len(la))
	for _, w := range la {
		laSet[w] = true
	}
	shared := 0
	for _, w := range na {
		if laSet[w] {
			shared++
		}
	}
	// <30% shared content vocabulary = a different topic.
	return float64(shared)/float64(len(na)) < 0.3
}

// tokenizeForPivot yields content tokens: whole ASCII words plus individual
// CJK characters (Chinese has no spaces, so character-level is the honest
// granularity here). Pure connectors/particles and single-char filler are
// dropped so overlap reflects real topic words.
func tokenizeForPivot(s string) []string {
	lower := strings.ToLower(s)
	stop := map[rune]bool{
		'的': true, '了': true, '是': true, '我': true, '你': true, '他': true, '她': true, '它': true,
		'们': true, '在': true, '和': true, '与': true, '或': true, '就': true, '都': true, '也': true,
		'还': true, '又': true, '要': true, '会': true, '能': true, '很': true, '更': true, '最': true,
		'吧': true, '吗': true, '呢': true, '啊': true, '嗯': true, '这': true, '那': true, '个': true,
		'有': true, '做': true, '用': true, '给': true, '让': true, '把': true, '被': true, '帮': true,
		'请': true, '下': true, '一': true, '并': true, '么': true, '些': true, '里': true,
		'来': true, '去': true, '到': true, '从': true, '对': true, '为': true, '上': true, '中': true,
		'等': true, '以': true, '可': true, '好': true, '大': true, '小': true, '新': true, '旧': true,
	}
	stopWord := map[string]bool{
		"the": true, "a": true, "an": true, "to": true, "of": true, "in": true, "on": true,
		"and": true, "or": true, "for": true, "with": true, "at": true, "it": true, "is": true,
		"are": true, "was": true, "i": true, "you": true, "me": true, "my": true, "please": true,
		"help": true, "do": true,
	}
	var out []string
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		w := string(word)
		if len(word) >= 2 && !stopWord[w] {
			out = append(out, w)
		}
		word = word[:0]
	}
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			word = append(word, r)
		case r >= 0x4e00 && r <= 0x9fff:
			flush()
			if !stop[r] {
				out = append(out, string(r))
			}
		default:
			flush()
		}
	}
	flush()
	return out
}

// isNewTaskPrompt is a conservative heuristic: does this user message OPEN a
// new/parallel task rather than continue the current one? It matches short
// command-like openers ("另外…", "顺便…", "新任务:", "同时…"). Plain
// continuation sentences are NOT treated as pivots.
func isNewTaskPrompt(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, m := range []string{
		"另外", "顺便", "与此同时", "同时", "接着", "然后", "新任务", "换个", "另外帮我",
		"紧接着", "另外，", "还有", "以及", "aside", "by the way", "also, ", "next: ",
	} {
		if strings.HasPrefix(lower, m) {
			return true
		}
	}
	return false
}

// truncStashMsg shortens a task reference used in stash labels/notes.
func truncStashMsg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 60 {
		return s
	}
	return s[:57] + "..."
}

func (e *Engine) genPermID() string {
	e.permMu.Lock()
	e.permSeq++
	id := fmt.Sprintf("perm-%d", e.permSeq)
	e.permMu.Unlock()
	return id
}

func orMaxTokens(cfg, modelDefault int) int {
	if cfg > 0 {
		return cfg
	}
	return modelDefault
}

// runToolTurn persists the assistant message with its tool calls, executes the
// batch (read-only tools concurrently, mutating tools sequentially), feeds any
// generated images and LSP diagnostics back in, and resumes the agent loop.
func (e *Engine) runToolTurn(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	assistantMsg types.Message,
	toolCalls []types.ToolCall,
	out chan types.StreamEvent,
	depth int,
) {
	assistantMsg.ToolCalls = toolCalls
	assistantMsg = e.appendPersisted(sessionID, opt, assistantMsg)

	e.executeToolBatch(ctx, sessionID, toolCalls, out)
	for _, tc := range toolCalls {
		if tc.Result != nil {
			opt.AddMessage(types.Message{
				Role: types.RoleTool, Content: tc.Result.Content,
				ToolID: tc.ID, Timestamp: time.Now(),
			})
		}
	}
	// Feed any generated images back into the conversation so vision-capable
	// models can see them on the next turn.
	e.ingestToolAttachments(sessionID, toolCalls, opt)
	// LSP diagnostics: inject compile errors as auto-fix hints so the model
	// can correct them in the continuation.
	if e.lspManager != nil {
		if diagnosticsMsg := e.collectDiagnostics(toolCalls); diagnosticsMsg != "" {
			opt.AddMessage(types.Message{
				Role: types.RoleSystem, Content: diagnosticsMsg, Timestamp: time.Now(),
			})
			out <- types.StreamEvent{Type: types.EventText, Content: diagnosticsMsg}
		}
	}
	out <- types.StreamEvent{
		Type:    types.EventText,
		Content: "\n[Continuing with tool results...]\n\n",
	}
	e.continueAgentLoop(ctx, sessionID, provider, opt, modelInfo, out, depth+1)
}

// finishTextTurn closes out a turn that produced no tool calls. If the model
// was cut off at max_tokens it transparently retries with a bigger budget and
// a continuation hint (truncation recovery) before persisting the reply.
func (e *Engine) finishTextTurn(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	assistantMsg types.Message,
	done types.StreamEvent,
	startTime time.Time,
	out chan types.StreamEvent,
	depth int,
) {
	opt.RecordUsage(done.Meta.Usage, calculateCostWithPricing(done.Meta.Usage, modelInfo, e.pricingOverrides), startTime)
	if e.truncDet != nil && e.truncDet.IsTruncated(done.Meta.FinishReason, assistantMsg.Content) && ctx.Err() == nil {
		if recovered := e.recoverTruncation(ctx, sessionID, provider, opt, modelInfo, &assistantMsg, out); recovered {
			if len(assistantMsg.ToolCalls) > 0 {
				e.runToolTurn(ctx, sessionID, provider, opt, modelInfo, assistantMsg, assistantMsg.ToolCalls, out, depth)
				return
			}
		}
	}
	assistantMsg = e.appendPersisted(sessionID, opt, assistantMsg)
	// Plan mode: the finished reply is a proposal, so surface it as a pending
	// plan the UI can offer to confirm (Enter) or discard (Esc). Confirmation
	// switches the gate out of read-only plan and continues execution.
	if e.gate != nil && e.gate.Mode() == permission.ModePlan {
		out <- types.StreamEvent{Type: types.EventPlanProposal}
	}
	// finishTextTurn is the SINGLE EventDone emitter for this round: the
	// outer loops no longer forward a second one (the old double-Done made
	// the Stop-hook continuation rounds trip the UI's turn-end handler).
	// Stamping the model here keeps parity with the former outer event.
	done.Meta.Model = modelInfo.WireModel()
	out <- done
}

// recoverTruncation retries a truncated assistant reply with a larger
// max_tokens and a continuation hint (Claude Code parity). The continuation
// text is appended to msg; if the model instead emits tool calls they are
// returned on msg.ToolCalls for normal batch execution. Escalates through
// 8K→16K→32K→64K up to MaxRetries.
func (e *Engine) recoverTruncation(
	ctx context.Context,
	sessionID string,
	provider types.Provider,
	opt *tokenopt.Optimizer,
	modelInfo types.ModelInfo,
	msg *types.Message,
	out chan types.StreamEvent,
) bool {
	tr := e.truncDet
	if tr == nil {
		return false
	}
	temperature, topP, modelMaxOut := e.applyModelParams(modelInfo)
	maxTokens := orMaxTokens(e.maxTokens, modelMaxOut)
	recovered := false
	for attempt := 0; attempt < tr.config.MaxRetries && ctx.Err() == nil; attempt++ {
		nextMax := tr.config.NextTokens(maxTokens)
		if nextMax <= maxTokens {
			return recovered // already at the ceiling, nothing more to escalate
		}
		maxTokens = nextMax

		// Continue from the cut-off tail — never apologise or restart.
		opt.AddMessage(types.Message{
			Role:      types.RoleUser,
			Content:   tr.config.BuildRetryPrompt(firstN(msg.Content, 1500)),
			Timestamp: time.Now(),
		})

		ch, err := provider.ChatStream(ctx, types.ChatRequest{
			SessionID:        sessionID,
			Messages:         opt.CompactRequest(""),
			Model:            modelInfo.WireModel(),
			ProviderName:     modelInfo.Provider,
			SystemPrompt:     opt.BuildPrefix(),
			Tools:            e.toolReg.ListDefs(),
			MaxTokens:        maxTokens,
			Temperature:      temperature,
			TopP:             topP,
			CacheBreakpoints: opt.BuildCacheBreakpoints(),
			Thinking:         e.thinkingConfig(),
			CacheTTL:         e.cacheTTL,
		})
		if err != nil {
			out <- types.StreamEvent{Type: types.EventError, Content: friendlyModelError(err)}
			return recovered
		}
		recovered = true

		var contCalls []types.ToolCall
		stillTruncated := false
		for ev := range ch {
			switch ev.Type {
			case types.EventText:
				msg.Content += ev.Content
				out <- ev
			case types.EventThinking:
				out <- ev
			case types.EventToolUse:
				contCalls = append(contCalls, types.ToolCall{
					ID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments,
				})
				out <- ev
			case types.EventError:
				out <- ev
				stillTruncated = false
			case types.EventDone:
				stillTruncated = tr.IsTruncated(ev.Meta.FinishReason, msg.Content)
			}
		}
		if len(contCalls) > 0 {
			msg.ToolCalls = contCalls
			return recovered
		}
		if !stillTruncated {
			return recovered
		}
	}
	return recovered
}
