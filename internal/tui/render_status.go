package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/core/skills"
	"github.com/ponygates/icode/internal/core/tool"
	"github.com/ponygates/icode/internal/xgo"
)

// SetContext records the latest request's prompt-token count and the model's
// context window so the status bar / explorer can show live context usage.
func (t *TUI) SetContext(tokens, window int) {
	t.mu.Lock()
	t.contextTokens = tokens
	t.contextWindow = window
	t.mu.Unlock()
}

// listCwd returns the top-level entries of the current working directory
// (dotfiles and hidden entries skipped), used by the explorer pane.
func listCwd() []string {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 14 {
		names = names[:14]
	}
	return names
}

// statusParts collects the status bar's segments under t.mu (render
// snapshot phase — never lock inside; read fields directly). Segments
// split into two zones (layoutStatusLine fits them side by side):
//
//	left  — identity & work: mode badge, running tool, todo progress,
//	        session title, git branch, PR badge, skill count, security
//	        level, background-task count.
//	right — usage: model, cost, token flow, cache hit rate, elapsed,
//	        flash notice.
//
// Segment ORDER is the drop priority: slice heads (mode, model, cost)
// survive to the bitter end; slice tails (bg, security, skills, PR,
// notice) are dropped first when the terminal is too narrow. The
// context-usage percent lives HERE now (right zone, after the token
// flow) — the standalone gradient contextBar row was removed to keep
// the prompt block one row shorter; the percent is the whole display.
func (t *TUI) statusParts() (left, right []string) {
	d := func(s string) string { return t.paint("dim", s) }

	// ── Left: identity & work ──
	// Mode badge — always visible so the user knows what approvals to
	// expect (Claude Code parity: plan/agent/auto/yolo are first-class).
	// The code is mapped to a localized label (计划/智能体/全自动/自动).
	if t.mode != "" {
		left = append(left, t.paint(modeColor(t.mode), "["+t.modeLabel(string(t.mode))+"]"))
	}
	// opencode-style thinking scanner while the agent is busy: a bright ■
	// head sweeping a dim ⬝ dot track with a cyan fade-trail (Knight Rider),
	// parked briefly at each end. Fixed 8-cell slot → the status-line layout
	// never shifts frame to frame; only the head's position animates.
	// Shown for every non-idle phase (generation + tool execution), matching
	// opencode's status()!=idle rule rather than streaming alone.
	if t.streaming || (t.curTool != "" && t.curTool != "…") {
		left = append(left, t.thinkingSlider())
	}
	// Current work indicator: the tool executing right now, streaming only.
	if t.streaming && t.curTool != "" && t.curTool != "…" {
		left = append(left, t.paint("yellow", "⚙ "+t.curTool))
	}
	// Todo counter — shown when the current session has an active todo list.
	// Zero-list sessions render nothing.
	if t.callback != nil {
		if text := t.callback.TodoActiveText(); text != "" {
			// Claude Code parity: show WHAT is being worked on right now.
			left = append(left, d("◑ "+text))
		}
		if pending, active, done, total := t.callback.TodoCounts(); total > 0 {
			seg := fmt.Sprintf("✓%d", done)
			if pending > 0 || active > 0 {
				seg = fmt.Sprintf("%d…%d→%d", pending, active, done)
			}
			if active > 0 {
				seg = t.paint("yellow", seg)
			} else {
				seg = d(seg)
			}
			left = append(left, seg)
		}
	}
	// Session title — set by autoTitle (first message) or /rename, so the
	// status line always says which conversation is active.
	if st := strings.TrimSpace(t.sessionTitle); st != "" {
		left = append(left, d("📎 "+truncate(st, 18)))
	}
	// Git branch — lazily refreshed, never on the render hot path.
	if b := t.branchSegment(); b != "" {
		left = append(left, d("⎇ ")+b)
	}
	// PR status badge — lazily refreshed (gh pr view), never on the render hot
	// path. Coloured by mergeable state (OPEN/MERGED green; CLOSED/DRAFT red).
	if pr := t.prSegment(); pr != "" {
		color := "green"
		if strings.Contains(pr, "CLOSED") || strings.Contains(pr, "DRAFT") {
			color = "red"
		}
		left = append(left, t.paint(color, "PR "+pr))
	}
	// Installed skill count — cached lazily, never hit the disk per frame.
	if !t.skillCountLoaded {
		t.skillCountLoaded = true
		if reg := skills.Load(skills.DefaultDirs()...); reg != nil {
			t.skillCount = len(reg.List())
		}
	}
	if t.skillCount > 0 {
		left = append(left, d(fmt.Sprintf("🧩%d", t.skillCount)))
	}
	// Security level badge — always visible so the user knows their privacy
	// boundary. Unlike Claude Code, no hidden telemetry or phone-home.
	if t.securityLevel != "" && t.securityLevel != "local" {
		left = append(left, permission.SecurityLabel(config.SecurityLevel(t.securityLevel)))
	}
	// Background tasks (sub-agents + shell) running detached.
	if n := t.runningBgCount(); n > 0 {
		left = append(left, t.paint("cyan", fmt.Sprintf("⚡%s", fmt.Sprintf(t.tstr("status.bg"), n))))
	}

	// ── Right: usage ──
	right = append(right, t.paint("green", "●")+" "+t.model)
	if t.cost != "" {
		right = append(right, d(t.cost))
	}
	if t.promptTokens > 0 || t.completionTokens > 0 {
		right = append(right,
			d("▸"+formatTokens(t.promptTokens)+" ▸"+formatTokens(t.completionTokens)))
	}
	// Context usage percent — the single, whole display of context state
	// (opencode style: a bare percent in the bottom-right corner). Colour
	// grades the pressure: green below half, yellow up to 80%, red beyond.
	if t.contextWindow > 0 && t.contextTokens > 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct > 100 {
			pct = 100
		}
		color := "green"
		switch {
		case pct >= 80:
			color = "red"
		case pct >= 50:
			color = "yellow"
		}
		right = append(right, t.paint(color, fmt.Sprintf("%d%%", pct)))
	}
	if t.cacheHitRate > 0 {
		right = append(right, d(fmt.Sprintf(t.tstr("status.cache"), t.cacheHitRate*100)))
	}
	if t.streaming && !t.turnStart.IsZero() {
		right = append(right, d("⏱ "+formatDuration(time.Since(t.turnStart))))
	}
	// Flash notice (slash command feedback) — lowest priority, dropped first.
	if t.statusNotice != "" {
		right = append(right, t.statusNotice)
	}
	return left, right
}

// layoutStatusLine fits the collected status segments into a W-wide row,
// Claude Code style: identity on the left, usage on the right, padded to
// the edges. When both zones don't fit, the least-important segments
// (slice TAILS) are dropped — the wider tail first — until the row fits.
// The heads are sacred and never drop: left[0] (the mode badge) and
// right[:2] (the model dot and the session cost), so a narrow terminal
// loses PR badges and skill counts, never the model name or the cost.
// Hard clip is the last resort for pathological widths.
func (t *TUI) layoutStatusLine(W int, left, right []string) string {
	sep := t.paint("dim", " · ")
	li, ri := len(left), len(right)
	for {
		lstr := strings.Join(left[:li], sep)
		rstr := strings.Join(right[:ri], sep)
		gap := W - visibleWidth(lstr) - visibleWidth(rstr)
		if gap >= 1 {
			return lstr + strings.Repeat(" ", gap) + rstr
		}
		// Heads (mode, model, cost) survive to the end — only the
		// disposable tails may go.
		if li <= 1 && ri <= 2 {
			break // nothing left to drop — hard-clip below
		}
		// Drop the wider tail segment first: it frees the most space.
		if li > 1 && (ri <= 2 || visibleWidth(left[li-1]) >= visibleWidth(right[ri-1])) {
			li--
		} else {
			ri--
		}
	}
	line := strings.Join(append(append([]string{}, left[:li]...), right[:ri]...), sep)
	if visibleWidth(line) > W {
		line = fitVis(line, W)
	}
	return line
}

// branchSegment returns the cached git branch for the status bar. Runs under
// t.mu (render snapshot phase) — reads fields directly and only spawns the
// background refresh goroutine (which takes the lock itself on write-back).
func (t *TUI) branchSegment() string {
	if time.Since(t.branchCheck) >= 30*time.Second {
		t.branchCheck = time.Now()
		xgo.GoSafe("tui.branch", func() {
			out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
			name := ""
			if err == nil {
				name = strings.TrimSpace(string(out))
				if name == "HEAD" { // detached — show short sha instead
					if sha, e := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); e == nil {
						name = "@" + strings.TrimSpace(string(sha))
					}
				}
			}
			t.mu.Lock()
			t.gitBranch = name
			t.mu.Unlock()
		})
	}
	return t.gitBranch
}

// runningBgCount reports how many background jobs (detached sub-agents plus
// background shell commands) exist, for the ⚡N status segment.
func (t *TUI) runningBgCount() int {
	return tool.RunningAgentTaskCount() + tool.RunningShellTaskCount()
}

// prSegment returns the cached GitHub PR status badge for the current branch,
// e.g. "#123 OPEN" or "#456 MERGED". Mirrors branchSegment: refreshed lazily
// (every 60s) via `gh pr view`, never on the render hot path. Empty when there
// is no PR for the branch, `gh` is missing, or the call fails. The JSON result
// carries the review/merge state so the badge can be coloured meaningfully.
func (t *TUI) prSegment() string {
	if time.Since(t.prCheck) >= 60*time.Second {
		t.prCheck = time.Now()
		branch := t.gitBranch
		xgo.GoSafe("tui.pr", func() {
			if branch == "" {
				t.mu.Lock()
				t.prBadge = ""
				t.mu.Unlock()
				return
			}
			if _, err := exec.LookPath("gh"); err != nil {
				return // gh not installed — leave the (empty) badge as-is
			}
			out, err := exec.Command("gh", "pr", "view", "--json",
				"number,state,title,url").Output()
			if err != nil {
				t.mu.Lock()
				t.prBadge = ""
				t.mu.Unlock()
				return
			}
			var pr struct {
				Number int    `json:"number"`
				State  string `json:"state"`
				Title  string `json:"title"`
				URL    string `json:"url"`
			}
			if e := json.Unmarshal(out, &pr); e != nil || pr.Number == 0 {
				t.mu.Lock()
				t.prBadge = ""
				t.mu.Unlock()
				return
			}
			badge := fmt.Sprintf("#%d %s", pr.Number, strings.ToUpper(pr.State))
			t.mu.Lock()
			t.prBadge = badge
			t.mu.Unlock()
		})
	}
	return t.prBadge
}

// formatDuration renders a duration compactly: "3.2s" or "1m04s".
func formatDuration(d time.Duration) string {
	d = d.Round(time.Millisecond)
	if d < time.Minute {
		return fmt.Sprintf("%.1f秒", d.Seconds())
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%d分%02d秒", m, s)
}

// modeColor returns the ANSI color name for the current mode indicator,
// matching Claude Code's mode coloring (plan=blue, yolo=red, …).
func modeColor(m Mode) string {
	switch m {
	case ModePlan:
		return "blue"
	case ModeAuto:
		return "green"
	case ModeYOLO:
		return "red"
	default:
		// Agent default — Claude Code's signature orange/red
		return "orange"
	}
}
