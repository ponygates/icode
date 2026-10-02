package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recCallback records the OnSlashCommand forwards that the default branch falls
// through to, and answers the query-style callbacks with recognizable markers so
// "this branch ran" is unmistakable.
type recCallback struct {
	testCallback
	fwd []string
}

func (r *recCallback) OnSlashCommand(cmd string, args []string) {
	r.fwd = append(r.fwd, strings.TrimSpace(cmd+" "+strings.Join(args, " ")))
}
func (r *recCallback) OnListSessions() string               { return "LIST-SESS" }
func (r *recCallback) OnStatus() string                     { return "STATUS-OUT" }
func (r *recCallback) OnTokenStats() string                 { return "TOKEN-OUT" }
func (r *recCallback) OnUpdateModels() string               { return "UPDATE-OUT" }
func (r *recCallback) OnAddDir(string) string               { return "ADDDIR-OUT" }
func (r *recCallback) OnOutputStyle(string) string          { return "STYLE-OUT" }
func (r *recCallback) OnRenameSession(string) string        { return "RENAME-OUT" }
func (r *recCallback) LSPQuery(string, []string) string     { return "LSP-OUT" }
func (r *recCallback) KnowledgeQuery(string) string         { return "KB-OUT" }
func (r *recCallback) CreateIdleTask(string, string) string { return "IDLE-OUT" }

// ghInstalled reports whether the GitHub CLI is on PATH; /pr_comments only shells
// out when it is, so the test can still exercise the dispatch branch offline.
func ghInstalled() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// TestHandleSlashDispatchCoverage pins every command branch that moved out of the
// old single switch in handleSlash: each probe declares what its branch must
// produce (a reply substring, "any reply", a state change, or an exact forward to
// the host).
//
// A branch that silently fell through to the default path shows up as an
// unexpected "/<name>" forward; a branch that was dropped shows up as a missing
// reply. HOME and CWD are redirected to temp dirs so the commands that persist
// settings, write memory files or export HTML cannot touch the developer's real
// ~/.icode or this repository.
func TestHandleSlashDispatchCoverage(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cases := []struct {
		line    string // the submitted slash-command line
		want    string // substring that must appear in the reply
		wantAny bool   // …or simply: the branch replied
		nilCB   bool   // run without a host callback
		empty   bool   // run against an empty transcript
		raw     bool   // run in raw-mode (overlay/modal paths need it)
		forward string // exact OnSlashCommand forward expected
		state   func(*testing.T, *TUI)
		skip    string // why the branch cannot be driven from a test
	}{
		// ── info group ──
		{line: "/help", want: "Ctrl+C"},
		{line: "/expand", wantAny: true},
		{line: "/status", want: "STATUS-OUT"},
		{line: "/cost", wantAny: true},
		{line: "/usage", want: "TOKEN-OUT"},
		{line: "/stats", want: "TOKEN-OUT"},
		{line: "/token", want: "TOKEN-OUT"},
		{line: "/keys", want: "API 密钥状态"},
		{line: "/history", want: "1. previous prompt"},
		{line: "/tasks", want: "当前没有后台任务"},
		{line: "/todo", wantAny: true},
		{line: "/welcome", state: func(t *testing.T, tu *TUI) {
			if tu.welcomeVisible {
				t.Error("/welcome should hide the banner on a non-empty transcript")
			}
		}},
		{line: "/whoami", wantAny: true},
		{line: "/context", wantAny: true},
		{line: "/feedback", want: "GitHub Issues"},
		{line: "/release-notes", wantAny: true},
		{line: "/doctor", wantAny: true},
		{line: "/bug", skip: "openURL launches the default browser"},

		// ── session group ──
		{line: "/exit", want: "再见", state: func(t *testing.T, tu *TUI) {
			if tu.running {
				t.Error("/exit must clear the running flag")
			}
		}},
		{line: "/quit", want: "再见", state: func(t *testing.T, tu *TUI) {
			if tu.running {
				t.Error("/quit must clear the running flag")
			}
		}},
		{line: "/session", want: "LIST-SESS"},
		{line: "/sessions", want: "LIST-SESS"},
		{line: "/new", want: "新会话已创建", state: func(t *testing.T, tu *TUI) {
			tu.mu.Lock()
			defer tu.mu.Unlock()
			if len(tu.messages) != 1 { // only the notice survives
				t.Errorf("messages = %d, want the transcript reset", len(tu.messages))
			}
		}},
		{line: "/newsession", want: "新会话已创建"},
		{line: "/resume", wantAny: true},
		{line: "/resume s-1", forward: "/resume s-1"},
		{line: "/fork", want: "用法: /fork"},
		{line: "/branch", want: "用法: /branch"},
		{line: "/fork s-1", forward: "/fork s-1"},
		{line: "/restore", nilCB: true, want: "用法: /restore"},
		{line: "/restore s-1", forward: "/restore s-1"},
		{line: "/goal", nilCB: true, want: "用法: /goal"},
		{line: "/goal show", forward: "/goal show"},
		{line: "/budget", nilCB: true, want: "用法: /budget"},
		{line: "/budget show", forward: "/budget show"},
		{line: "/clear", want: "对话已清空", forward: "/clear"},
		{line: "/wipe", wantAny: true},
		{line: "/search", want: "用法: /search"},
		{line: "/search needle", forward: "/search needle"},
		// /summarize renders the summary AND archives a zero-token copy by
		// forwarding itself — original behaviour, so the forward is expected.
		{line: "/summarize", want: "### 用户提问", forward: "/summarize"},
		{line: "/summarize", nilCB: true, empty: true, want: "没有对话内容可总结"},
		{line: "/cd", wantAny: true},
		{line: "/rename 新标题", want: "RENAME-OUT"},
		{line: "/export", wantAny: true},
		{line: "/copy bogus", want: "用法: /copy"},
		{line: "/share", want: "可分享副本"},
		{line: "/compact", wantAny: true},
		{line: "/recap", wantAny: true},

		// ── model group ──
		{line: "/model", wantAny: true},
		{line: "/model gpt-4o", wantAny: true, state: func(t *testing.T, tu *TUI) {
			tu.mu.Lock()
			defer tu.mu.Unlock()
			if tu.model != "gpt-4o" {
				t.Errorf("model = %q, want gpt-4o", tu.model)
			}
		}},
		{line: "/models", wantAny: true},
		{line: "/mode", want: "当前模式"},
		{line: "/mode plan", want: "模式", state: modeIs("plan")},
		{line: "/mode bogus", want: "无效模式"},
		{line: "/plan", want: "模式", state: modeIs("plan")},
		{line: "/ask", want: "模式", state: modeIs("ask")},
		{line: "/debug", want: "模式", state: modeIs("agent")},
		{line: "/provider", want: "当前服务商"},
		{line: "/provider deepseek", want: "deepseek", state: func(t *testing.T, tu *TUI) {
			tu.mu.Lock()
			defer tu.mu.Unlock()
			if tu.provider != "deepseek" {
				t.Errorf("provider = %q", tu.provider)
			}
		}},
		{line: "/update", want: "UPDATE-OUT"},
		{line: "/add-dir", wantAny: true},
		{line: "/add-dir /tmp/x", want: "ADDDIR-OUT"},
		{line: "/output-style", want: "当前输出风格"},
		{line: "/output-style concise", want: "STYLE-OUT"},
		{line: "/output-style bogus", want: "无效风格"},
		{line: "/admin", want: "管理员模式", state: modeIs("yolo")},
		{line: "/admin off", want: "管理员模式", state: modeIs("ask")},
		{line: "/login", want: "终端行模式无法安全隐藏输入"},
		{line: "/login", raw: true, state: func(t *testing.T, tu *TUI) {
			if !tu.promptActive() {
				t.Error("/login in raw mode must open the modal key prompt")
			}
			tu.mu.Lock()
			tu.prompt = nil
			tu.mu.Unlock()
		}},
		{line: "/logout", want: "没有已保存的 API Key"},

		// ── config group ──
		{line: "/config", state: func(t *testing.T, tu *TUI) {
			tu.mu.Lock()
			defer tu.mu.Unlock()
			if !tu.settingsOpen {
				t.Error("bare /config must open the settings panel")
			}
		}},
		{line: "/theme", wantAny: true},
		{line: "/lang", wantAny: true},
		{line: "/mouse", want: "鼠标"},
		{line: "/multiline", want: "多行输入", state: func(t *testing.T, tu *TUI) {
			tu.mu.Lock()
			defer tu.mu.Unlock()
			if !tu.multiline {
				t.Error("/multiline must flip the flag")
			}
		}},
		{line: "/verbose", want: "详细输出"},
		{line: "/zen", want: "Zen"},
		{line: "/vim", want: "Vim"},
		{line: "/statusline", want: "状态栏"},
		{line: "/bell", want: "铃声"},
		{line: "/security", wantAny: true},
		{line: "/permissions", wantAny: true},
		{line: "/init", wantAny: true, state: func(t *testing.T, _ *TUI) {
			now, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			if _, err := os.Stat(filepath.Join(now, "ICODE.md")); err != nil {
				t.Errorf("/init did not write ICODE.md: %v", err)
			}
		}},
		{line: "/memory", wantAny: true},
		{line: "/hooks", wantAny: true},
		{line: "/hooks events", want: "PreToolUse"},

		// ── workspace group ──
		{line: "/diff", wantAny: true},
		{line: "/review", wantAny: true},
		{line: "/lsp", want: "LSP-OUT"},
		{line: "/kb", want: "KB-OUT"},
		{line: "/knowledge q", want: "KB-OUT"},
		{line: "/idle", want: "用法: /idle"},
		{line: "/idle name task", want: "IDLE-OUT"},
		{line: "/rewind", wantAny: true},
		{line: "/checkpoint", wantAny: true},
		{line: "/rewind 0", want: "用法: /rewind"},
		{line: "/undo", want: "没有活跃会话"},
		{line: "/redo", want: "没有活跃会话"},
		{line: "/replay", want: "没有活跃会话"},
		{line: "/apply", nilCB: true, want: "没有可应用的暂存编辑"},
		{line: "/reject", nilCB: true, want: "没有可丢弃的暂存编辑"},

		// ── ext group ──
		{line: "/agents", wantAny: true},
		{line: "/skills", wantAny: true},
		{line: "/skill-eval", wantAny: true},
		{line: "/plugin", wantAny: true},
		{line: "/mesh", wantAny: true},
		{line: "/teams", wantAny: true},
		{line: "/mcp", wantAny: true},
		{line: "/voice", skip: "toggleVoiceRecording opens the live microphone (rec.Start) on Windows"},
		{line: "/pr_comments", skip: "shells out to the GitHub CLI", want: "未检测到 GitHub CLI"},

		// ── default branch ──
		{line: "/thinking on", forward: "/thinking on"},
		{line: "/totally-unknown", forward: "/totally-unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			if tc.skip != "" {
				if tc.line == "/pr_comments" && ghInstalled() {
					t.Skip(tc.skip + " (gh is installed here, running it would hit the network)")
				}
				if tc.line != "/pr_comments" {
					t.Skip(tc.skip)
				}
			}
			cb := &recCallback{}
			var host Callback
			if !tc.nilCB {
				host = cb
			}
			tu := New(Config{Mode: ModeAuto, Model: "m1", Provider: "openrouter", Version: "test", Callback: host})
			tu.rawMode = tc.raw
			tu.pushHistory("previous prompt")
			if !tc.empty {
				tu.add(RoleUser, "hello")
				tu.add(RoleAssistant, "world")
			}
			tu.mu.Lock()
			before := len(tu.messages)
			tu.mu.Unlock()

			tu.handleSlash(tc.line)

			tu.mu.Lock()
			// Some commands reset the transcript (/new, /clear), so the tail
			// window can legitimately start over — clamp instead of slicing past 0.
			start := before
			if len(tu.messages) < start {
				start = 0
			}
			slice := append([]Message(nil), tu.messages[start:]...)
			tu.mu.Unlock()
			var b strings.Builder
			for _, m := range slice {
				b.WriteString(string(m.Role) + "|" + m.Content + "\n")
			}
			got := b.String()
			switch {
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Errorf("%s reply missing %q:\n%s", tc.line, tc.want, got)
			case tc.wantAny && len(slice) == 0:
				t.Errorf("%s produced no reply — the branch looks unreachable", tc.line)
			}
			if tc.forward != "" {
				if len(cb.fwd) != 1 || cb.fwd[0] != tc.forward {
					t.Errorf("%s forwards = %v, want [%s]", tc.line, cb.fwd, tc.forward)
				}
			} else {
				// A locally handled command must never reach the host under its
				// own name — that is precisely what the default branch does.
				name := strings.Fields(tc.line)[0]
				for _, f := range cb.fwd {
					if strings.Fields(f)[0] == name {
						t.Errorf("%s fell through to the host (forwarded %q)", tc.line, f)
					}
				}
			}
			if tc.state != nil {
				tc.state(t, tu)
			}
		})
	}
}

func modeIs(want string) func(*testing.T, *TUI) {
	return func(t *testing.T, tu *TUI) {
		t.Helper()
		tu.mu.Lock()
		defer tu.mu.Unlock()
		if tu.mode != want {
			t.Errorf("mode = %q, want %q", tu.mode, want)
		}
	}
}
