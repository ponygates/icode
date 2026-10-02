package slashui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/core/knowledge"
	"github.com/ponygates/icode/internal/core/searchreplace"
	"github.com/ponygates/icode/internal/executil"
)

func cmdSearch(b *Backend, args []string) Result {
	query := strings.Join(args, " ")
	if query == "" {
		return ok("用法: /search <关键词> — 搜索历史会话")
	}
	if b == nil || b.SessStore == nil {
		return ok("无会话存储可用。")
	}
	results, err := b.SessStore.SearchMessages(query, 20)
	if err != nil {
		return errf("搜索失败: %v", err)
	}
	if len(results) == 0 {
		return ok(fmt.Sprintf("未找到 %q 相关结果。", query))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条结果:\n", len(results)))
	for _, r := range results {
		title := r.SessionTitle
		if title == "" {
			title = "Untitled"
		}
		content := strings.ReplaceAll(r.Content, "\n", " ")
		if len(content) > 120 {
			content = content[:120] + "..."
		}
		sb.WriteString(fmt.Sprintf("\n  [%s] %s\n    %s\n", title, r.Role, content))
	}
	sb.WriteString("\n/resume <session_id> 载入某个会话")
	return ok(sb.String())
}

func cmdDiff(args []string) Result {
	extra := ""
	if len(args) > 0 {
		extra = args[0]
	}
	cmdArgs := []string{"diff", extra}
	output, err := executil.Command("git", cmdArgs...).CombinedOutput()
	if err != nil && len(output) == 0 {
		return errf("git diff 失败: %v", err)
	}
	if len(output) == 0 {
		return ok("没有未提交的改动。")
	}
	return ok("```diff\n" + strings.TrimRight(string(output), "\n") + "\n```")
}

// cmdKnowledge searches the local document knowledge base (/kb), 对标
// WorkBuddy 的"资料库"能力（本地 RAG，零 API 零 token）。
func cmdKnowledge(b *Backend, args []string) Result {
	if b == nil || b.Engine == nil {
		return errf("知识库不可用（引擎未就绪）。")
	}
	mgr := b.Engine.KnowledgeManager()
	if mgr == nil {
		return errf("知识库未配置。请在 config.yaml 的 knowledge.dirs 中指定文档目录，或在 /config 中设置。")
	}
	if len(args) == 0 {
		return ok(fmt.Sprintf("知识库状态：已索引 %d 个片段。\n用法: /kb <查询>（如 /kb 保险犹豫期退保规则）", mgr.ChunkCount()))
	}
	query := strings.Join(args, " ")
	if mgr.ChunkCount() == 0 {
		if _, err := mgr.Index(context.Background()); err != nil {
			return errf("索引失败: %v", err)
		}
	}
	results := mgr.Search(query, 5)
	return ok(knowledge.Format(results))
}
func cmdLsp(b *Backend, st *State, args []string) Result {
	if b == nil || b.Engine == nil {
		return errf("LSP 不可用（引擎未就绪）。")
	}
	mgr := b.Engine.LSPManager()
	if mgr == nil {
		return errf("LSP 未启用。请在配置中开启 lsp.enabled（config.toml 或 /config）。")
	}
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}
	out, err := mgr.QueryReport(sub, args)
	if err != nil {
		return errf("%v", err)
	}
	return ok(out)
}

func cmdExport(b *Backend, st *State, args []string) Result {
	filename := "icode-export.md"
	if len(args) > 0 {
		// Bare basename only — a "../" or absolute path in the argument must
		// not write outside the working directory.
		filename = filepath.Base(args[0])
		if filename == "." || filename == string(filepath.Separator) {
			filename = "icode-export.md"
		}
	}
	if b == nil || b.SessStore == nil || st.SessionID == "" {
		return ok("没有可导出的会话（先发一条消息）。")
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return errf("读取会话失败: %v", err)
	}
	var sb strings.Builder
	sb.WriteString("# iCode Conversation Export\n\n")
	sb.WriteString(fmt.Sprintf("**Model:** %s  \n", st.Model))
	sb.WriteString(fmt.Sprintf("**Mode:** %s  \n\n", st.Mode))
	sb.WriteString("---\n\n")
	for _, m := range sess.Messages {
		switch m.Role {
		case "user":
			sb.WriteString("## User\n\n")
		case "assistant":
			sb.WriteString("## Assistant\n\n")
		case "system":
			sb.WriteString("> ")
		case "tool":
			if len(m.ToolCalls) > 0 {
				sb.WriteString("### Tool: " + m.ToolCalls[0].Name + "\n\n")
			} else {
				sb.WriteString("### Tool\n\n")
			}
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	if err := os.WriteFile(filename, []byte(sb.String()), 0o644); err != nil {
		return errf("导出失败: %v", err)
	}
	return ok(fmt.Sprintf("已导出到 %s（%d 条消息）", filename, len(sess.Messages)))
}

func cmdShare(b *Backend, st *State, args []string) Result {
	// /share html — self-contained single-file HTML export (OpenCode /share
	// parity via the local-file route: no server, user hosts it anywhere).
	if len(args) > 0 && strings.EqualFold(args[0], "html") {
		return cmdShareHTML(b, st, args[1:])
	}
	name := fmt.Sprintf("icode-share-%s.md", time.Now().Format("20060102-150405"))
	if len(args) > 0 {
		name = filepath.Base(args[0])
	}
	res := cmdExport(b, st, []string{name})
	if res.IsError {
		return res
	}
	if abs, err := filepath.Abs(name); err == nil {
		return ok(res.Output + "\n分享文件: " + abs + "\n（可直接发送或粘贴到支持 Markdown 的工具；用 /share html 可导出网页版）")
	}
	return res
}

// cmdShareHTML exports the current session as one self-contained .html file:
// inlined styles + dependency-free JS Markdown renderer, chat-bubble layout.
func cmdShareHTML(b *Backend, st *State, args []string) Result {
	if b == nil || b.SessStore == nil || st.SessionID == "" {
		return ok("没有可导出的会话（先发一条消息）。")
	}
	sess, err := b.SessStore.Get(st.SessionID)
	if err != nil {
		return errf("读取会话失败: %v", err)
	}
	name := fmt.Sprintf("icode-share-%s.html", time.Now().Format("20060102-150405"))
	if len(args) > 0 {
		// Same path-escape guard as /export.
		name = filepath.Base(args[0])
		if name == "." || name == string(filepath.Separator) {
			name = fmt.Sprintf("icode-share-%s.html", time.Now().Format("20060102-150405"))
		} else if !strings.HasSuffix(strings.ToLower(name), ".html") {
			name += ".html"
		}
	}
	msgs := make([]shareMsg, 0, len(sess.Messages))
	for _, m := range sess.Messages {
		if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
		role := m.Role
		if role == "tool" && len(m.ToolCalls) > 0 {
			role = "tool"
		}
		msgs = append(msgs, shareMsg{Role: string(role), Content: m.Content})
	}
	title := "iCode 会话"
	page := buildShareHTML(title, st.Model, msgs)
	if err := os.WriteFile(name, []byte(page), 0o644); err != nil {
		return errf("导出失败: %v", err)
	}
	abs, _ := filepath.Abs(name)
	return ok(fmt.Sprintf("✓ 已导出单文件网页（%d 条消息）: %s\n用浏览器打开即可阅读；可直接发给任何人或托管到任意静态空间。", len(msgs), abs))
}

func cmdReview(args []string) Result {
	var target string
	if len(args) > 0 {
		path := args[0]
		if data, err := os.ReadFile(path); err == nil {
			target = fmt.Sprintf("请审查文件 %s：\n\n%s", path, string(data))
		} else {
			return errf("读取文件失败: %v", err)
		}
	} else {
		output, err := executil.Command("git", "diff").CombinedOutput()
		if err != nil && len(output) == 0 {
			return errf("git diff 失败: %v", err)
		}
		if len(output) == 0 {
			return ok("没有未提交的改动可审查。可指定路径：/review <file>")
		}
		target = "请审查以下 git 工作区差异，指出质量问题、安全隐患与改进建议：\n\n```diff\n" + string(output) + "\n```"
	}
	return chat(target)
}

func cmdApply(st *State) Result {
	stage := searchreplace.StageForSessionOrProcess(st.SessionID)
	n := stage.Count()
	if n == 0 {
		return ok("没有可应用的暂存编辑。")
	}
	results := stage.ApplyValid()
	var b strings.Builder
	applied := 0
	for _, r := range results {
		b.WriteString(r + "\n")
		if !strings.HasPrefix(r, "FAILED") {
			applied++
		}
	}
	b.WriteString(fmt.Sprintf("Applied %d/%d staged edits. Remaining: %d", applied, n, stage.Count()))
	return ok(b.String())
}

func cmdReject(st *State) Result {
	stage := searchreplace.StageForSessionOrProcess(st.SessionID)
	n := stage.Count()
	if n == 0 {
		return ok("没有可丢弃的暂存编辑。")
	}
	stage.Clear()
	return ok(fmt.Sprintf("已丢弃 %d 条暂存编辑。", n))
}
