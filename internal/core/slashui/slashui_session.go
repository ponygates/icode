package slashui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/checkpoint"
	"github.com/ponygates/icode/internal/core/hooks"
	"github.com/ponygates/icode/internal/core/sessionum"
	"github.com/ponygates/icode/internal/types"
)

// cmdCD moves the session's working directory (Claude Code / Qwen Code
// parity). Unlike /add-dir (which only grants extra access), /cd relocates
// the whole session: every later tool resolves relative paths from here.
// No args prints the current directory.
func cmdCD(st *State, args []string) Result {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if len(args) == 0 {
		return ok("当前工作目录: " + cwd + "\n用法: /cd <path> — 移动会话工作目录（相对路径基于当前目录解析）")
	}
	target := args[0]
	if target == "~" || target == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			target = home
		}
	} else if strings.HasPrefix(target, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			target = filepath.Join(home, strings.TrimPrefix(target, "~/"))
		}
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return errf("解析路径失败: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return errf("目录不存在或不是文件夹: %s", abs)
	}
	if err := os.Chdir(abs); err != nil {
		return errf("切换工作目录失败: %v", err)
	}
	// Refresh engine context so tools see the new directory immediately.
	if ncwd, err := os.Getwd(); err == nil {
		abs = ncwd
	}
	// Persist the directory so the CLI/TUI/server start there next launch
	// (the launch directory is otherwise lost when the process exits).
	if st == nil || !st.NoPersistCWD {
		persistSetting(func(c *config.Config) { c.Defaults.WorkingDir = abs })
	}
	return Result{Output: "工作目录已切换到: " + abs, CWD: abs}
}

// cmdRename retitles the active session (Cursor / Codex / Claude parity).
// With no title it shows the current one.
func cmdRename(b *Backend, st *State, args []string) Result {
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	if st.SessionID == "" {
		return ok("没有活跃会话。先开始对话再重命名。")
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return errf("会话不存在: %s", st.SessionID)
	}
	if len(args) == 0 {
		title := sess.Title
		if title == "" {
			title = "(untitled)"
		}
		return ok(fmt.Sprintf("当前会话标题: %s\n用法: /rename <新标题>", title))
	}
	title := strings.Join(args, " ")
	sess.Title = title
	if err := b.SessStore.Update(sess); err != nil {
		return errf("保存标题失败: %v", err)
	}
	return ok(fmt.Sprintf("会话已重命名为: %s", title))
}

// cmdCopy copies the last assistant output (or an explicit target file) to
// the Windows/Unix clipboard — Cursor / Codex / Gemini parity. It reads the
// session's most recent assistant message when no arg is given.
func cmdCopy(b *Backend, st *State, args []string) Result {
	if len(args) > 0 {
		path := args[0]
		data, err := os.ReadFile(path)
		if err != nil {
			return errf("读取文件失败: %v", err)
		}
		if err := copyToClipboard(string(data)); err != nil {
			return errf("复制失败: %v", err)
		}
		return ok("已复制文件内容到剪贴板: " + path)
	}
	if b == nil || b.SessStore == nil || st.SessionID == "" {
		return ok("没有可复制的输出（需要活跃会话）.\n用法: /copy [文件路径]")
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return errf("会话不存在: %s", st.SessionID)
	}
	text := ""
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == types.RoleAssistant {
			text = sess.Messages[i].Content
			break
		}
	}
	if text == "" {
		return ok("没有可复制的助手输出。")
	}
	if err := copyToClipboard(text); err != nil {
		return errf("复制失败: %v", err)
	}
	return ok(fmt.Sprintf("已复制最后一段助手输出（%d 字符）到剪贴板。", len(text)))
}

func cmdSessions(b *Backend, st *State, _ []string) Result {
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	if st.SessionID != "" {
		return ok("活跃会话: " + st.SessionID)
	}
	sessions, err := sessionum.ListNonDeleted(b.SessStore, 20)
	if err != nil || len(sessions) == 0 {
		return ok("暂无已保存会话。开始对话后自动创建。")
	}
	var sb strings.Builder
	kept := 0
	sb.WriteString(fmt.Sprintf("已保存会话 (%d):\n", len(sessions)))
	for _, s := range sessions {
		kept++
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		line := fmt.Sprintf("  %s  %s  [%s]", s.ID, title, s.ModelID)
		if sum := sessionum.Get(&s); sum != "" {
			first := sum
			if idx := strings.Index(first, "\n"); idx > 0 {
				first = first[:idx]
			}
			if r := []rune(first); len(r) > 60 {
				first = string(r[:60]) + "…"
			}
			line += fmt.Sprintf("\n      ↳ %s", first)
		}
		sb.WriteString(line + "\n")
	}
	if kept == 0 {
		sb.WriteString("  (无可用会话 — 全部已软删除，可用 /restore <id> 恢复)\n")
	}
	sb.WriteString("\n/resume <session_id> 载入某个会话")
	return ok(sb.String())
}

// defaultLiteN is how many recent messages /resume --lite replays alongside
// the archived summary (the rest is dropped from the model context).
const defaultLiteN = 4

func cmdResume(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("用法: /resume <session-id> [--lite[=<最近消息数>] | --compact[=<最近消息数>]]\n  --lite 只把摘要+最近 N 条消息送给模型，省 token（需要该会话已有存档摘要）\n  --compact 同 --lite，但摘要不足时先用模型生成语义摘要并缓存")
	}
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	id := args[0]
	lite := 0
	compact := false
	if len(args) > 1 {
		switch a := strings.TrimSpace(args[1]); {
		case a == "--lite":
			lite = defaultLiteN
		case strings.HasPrefix(a, "--lite="):
			if v, err := strconv.Atoi(strings.TrimPrefix(a, "--lite=")); err == nil && v > 0 {
				lite = v
			} else {
				return errf("用法: /resume <session-id> --lite=<最近消息数>（应为正整数）")
			}
		case a == "--compact":
			compact = true
			lite = defaultLiteN
		case strings.HasPrefix(a, "--compact="):
			compact = true
			if v, err := strconv.Atoi(strings.TrimPrefix(a, "--compact=")); err == nil && v > 0 {
				lite = v
			} else {
				return errf("用法: /resume <session-id> --compact=<最近消息数>（应为正整数）")
			}
		default:
			return errf("未知参数: %s（支持 --lite、--lite=<n>、--compact、--compact=<n>）", a)
		}
	}
	if st.SessionID != "" && st.SessionID != id {
		archiveSession(b, st)
	}
	sess, err := b.SessStore.Get(id)
	if err != nil {
		return errf("会话不存在: %s", id)
	}
	if sessionum.IsDeleted(sess) {
		return errf("该会话已被软删除，先用 /restore %s 恢复。", id)
	}
	if lite > 0 {
		if compact && !sessionum.IsSemantic(sess) && b.Engine != nil {
			// Resume compaction (Claude Code parity): generate a semantic
			// summary on the spot and cache it, so the resumed session feeds
			// the model only summary + recent turns. A cached semantic summary
			// is reused without a second model call.
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			sum, serr := b.Engine.SummarizeConversation(ctx, id, "")
			cancel()
			switch {
			case serr != nil:
				return errf("⚠ 模型摘要生成失败（%s），已回退为本地存档摘要。", clip(serr.Error(), 140))
			case sum == "":
				return errf("该会话轮次太少，无需模型摘要；如已有本地摘要可改用 /resume %s --lite。", id)
			default:
				_ = sessionum.Save(b.SessStore, sess, sum)
				_ = sessionum.MarkSemantic(b.SessStore, sess)
			}
		}
		if sessionum.Get(sess) == "" {
			return errf("该会话还没有存档摘要，无法压缩恢复。先 /summarize、/compact 或退出时自动存档后再试。")
		}
		if err := sessionum.SetLite(b.SessStore, sess, lite); err != nil {
			return errf("设置 lite 模式失败: %v", err)
		}
	}
	st.SessionID = sess.ID
	out := fmt.Sprintf("已载入会话 %s — %d 条消息", sess.ID, len(sess.Messages))
	if lite > 0 {
		if compact {
			out = fmt.Sprintf("已载入会话 %s（紧凑恢复）— 语义摘要 + 最近 %d 条消息（共 %d 条）送入模型，后续轮次自动生效", sess.ID, lite, len(sess.Messages))
		} else {
			out = fmt.Sprintf("已载入会话 %s（lite 模式）— 摘要 + 最近 %d 条消息（共 %d 条）送入模型", sess.ID, lite, len(sess.Messages))
		}
	}
	return Result{
		Output:    out,
		Model:     sess.ModelID,
		Provider:  sess.ProviderName,
		SessionID: sess.ID,
	}
}

// cmdFork branches a new independent session from a source session (or a
// prefix of it): /fork <session-id>[@<n>]. The fork shares history up to
// message n (or the whole session when n is omitted) but diverges from there.
func cmdFork(b *Backend, st *State, args []string) Result {
	if len(args) == 0 {
		return ok("用法: /fork <session-id>[@<消息数>] — 从历史会话分支出一个独立会话")
	}
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	spec := args[0]
	srcID := spec
	n := 0
	if at := strings.LastIndex(spec, "@"); at > 0 {
		srcID = spec[:at]
		if v, err := strconv.Atoi(spec[at+1:]); err == nil {
			n = v
		} else {
			return errf("用法: /fork <session-id>[@<消息数>]（消息数应为数字）")
		}
	}
	if st.SessionID != "" && st.SessionID != srcID {
		archiveSession(b, st)
	}
	forked, err := sessionum.Fork(b.SessStore, srcID, n)
	if err != nil {
		return errf("分叉失败: %v", err)
	}
	st.SessionID = forked.ID
	return Result{
		Output:    fmt.Sprintf("已从 %s 分叉出独立会话 %s — %d 条消息（后续互不影响）", srcID, forked.ID, len(forked.Messages)),
		Model:     forked.ModelID,
		Provider:  forked.ProviderName,
		SessionID: forked.ID,
	}
}

// archiveSession best-effort writes the current session's local summary into
// its Metadata before the user leaves it (/new, /resume). Failures are
// swallowed — archiving must never block navigation.
func archiveSession(b *Backend, st *State) {
	if b == nil || b.SessStore == nil || st == nil || st.SessionID == "" {
		return
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return
	}
	_ = sessionum.Save(b.SessStore, sess, sessionum.Generate(sess, st.Model, st.Provider, st.Mode))
}

func cmdClear(b *Backend, st *State) Result {
	if b != nil && b.SessStore != nil && st.SessionID != "" {
		if sess, err := b.SessStore.Get(st.SessionID); err == nil {
			if err := sessionum.MarkDeleted(b.SessStore, sess); err != nil {
				return errf("归档失败（会话未删除）: %v", err)
			}
			// SessionEnd lifecycle hook — the session is being deleted, so
			// external scripts can flush per-session state.
			b.Engine.FireHook(hooks.SessionEnd, hooks.Input{SessionID: st.SessionID})
		}
	}
	return Result{Output: "会话已归档并标记删除，可从列表移除（/restore <id> 可恢复）。", ClearSession: true}
}

func cmdRestore(b *Backend, args []string) Result {
	if len(args) == 0 {
		return ok("用法: /restore <session-id> — 恢复被 /clear 软删除的会话")
	}
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	sess, err := b.SessStore.Get(args[0])
	if err != nil {
		return errf("会话不存在: %s", args[0])
	}
	if !sessionum.IsDeleted(sess) {
		return ok("该会话未被删除，无需恢复。")
	}
	if err := sessionum.Restore(b.SessStore, sess); err != nil {
		return errf("恢复失败: %v", err)
	}
	return ok(fmt.Sprintf("已恢复会话 %s — %d 条消息", sess.ID, len(sess.Messages)))
}

func cmdUndo(st *State, args []string) Result {
	if st.SessionID == "" {
		return ok("没有活跃会话。")
	}
	n := 1
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil {
			n = v
		}
	}
	if n <= 0 {
		return ok("用法: /undo [N] — 回滚前 N 步文件更改")
	}
	store, err := checkpoint.GetOrOpen(st.SessionID)
	if err != nil {
		return errf("打开检查点失败: %v", err)
	}
	files, err := store.Rewind(context.Background(), n)
	if err != nil {
		return errf("回滚失败: %v", err)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("已回滚 %d 步。影响文件:\n", n))
	for _, f := range files {
		b.WriteString("  " + f + "\n")
	}
	if len(files) == 0 {
		b.WriteString("  （无检查点文件）\n")
	}
	return ok(b.String())
}

// cmdReplay lists the session's checkpoint timeline, or diffs any two points
// in it (OpenCode git-backed session review parity — the low-cost route: no
// storage migration, the checkpoint git log already is the timeline).
//
//	/replay            — timeline (index · time · message)
//	/replay <a> [b]    — diff between two checkpoints (a,b = timeline index or
//	                     hash; b defaults to the latest snapshot)
func cmdReplay(st *State, args []string) Result {
	store, err := checkpoint.GetOrOpen(st.SessionID)
	if err != nil {
		return errf("打开检查点失败: %v", err)
	}
	entries, err := store.List(context.Background())
	if err != nil {
		return errf("读取检查点失败: %v", err)
	}
	if len(entries) == 0 {
		return ok("本会话还没有检查点（改动文件后会自动快照）。")
	}
	// git log is newest-first; index 1 = most recent for the user.
	if len(args) == 0 {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("本会话检查点时间轴（%d 个，1 = 最新）:\n", len(entries)))
		for i, e := range entries {
			b.WriteString(fmt.Sprintf("  %2d. [%s] %s\n", i+1,
				e.When.Format("01-02 15:04:05"), truncateIcode(e.Message, 60)))
		}
		b.WriteString("\n对比两点差异: /replay <a> [b]（a、b 为序号，如 /replay 1 3）")
		return ok(b.String())
	}

	resolve := func(tok string) (string, error) {
		if n, aerr := strconv.Atoi(tok); aerr == nil {
			if n < 1 || n > len(entries) {
				return "", fmt.Errorf("序号 %d 超出范围（1-%d）", n, len(entries))
			}
			return entries[n-1].Hash, nil
		}
		return tok, nil // treat as a raw hash / revision
	}
	from, err := resolve(args[0])
	if err != nil {
		return errf("解析失败: %v", err)
	}
	to := "HEAD"
	if len(args) > 1 {
		to, err = resolve(args[1])
		if err != nil {
			return errf("解析失败: %v", err)
		}
	}
	diff, err := store.DiffBetween(context.Background(), from, to)
	if err != nil {
		return errf("对比失败: %v", err)
	}
	if strings.TrimSpace(diff) == "" {
		return ok(fmt.Sprintf("两个检查点之间没有文件差异（%s..%s）。", fromShort(from), fromShort(to)))
	}
	return ok(fmt.Sprintf("检查点差异 %s..%s:\n\n```diff\n%s\n```",
		fromShort(from), fromShort(to), strings.TrimRight(diff, "\n")))
}

func fromShort(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

// truncateIcode caps s at n runes with an ellipsis (used by /replay timeline).
func truncateIcode(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
