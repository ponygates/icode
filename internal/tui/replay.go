package tui

// replay.go — /replay: the checkpoint timeline overlay (Claude Code /replay
// parity). Lists the session's shadow-git checkpoints newest-first so the
// user can step through what the agent did:
//
//	↑/↓   move the highlight
//	Enter show the diff THAT step introduced (one-step replay)
//	r     rewind the workspace back to the highlighted checkpoint —
//	      first press previews the diff that will be reverted, second
//	      press confirms (防误触, same pattern as double-Esc rewind)
//	Esc   close

import (
	"context"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/core/checkpoint"
)

// openReplay loads the checkpoint timeline and opens the overlay.
func (t *TUI) openReplay() {
	if t.streaming {
		t.add(RoleSystem, "生成进行中，请等本轮结束后再回放。")
		return
	}
	sessionID := ""
	if t.callback != nil {
		sessionID = t.callback.SessionID()
	}
	if sessionID == "" {
		t.add(RoleSystem, "没有活跃会话。")
		return
	}
	store, err := checkpoint.GetOrOpen(sessionID)
	if err != nil {
		t.add(RoleError, "打开检查点失败: "+err.Error())
		return
	}
	entries, err := store.List(context.Background())
	if err != nil || len(entries) == 0 {
		t.add(RoleSystem, "本会话还没有检查点。模型修改文件后会自动创建；也可用 /checkpoint 手动创建。")
		return
	}
	t.replayList = entries
	t.replayIdx = 0
	t.replayTop = 0
	t.replayArmed = false
	t.replayOpen = true
	t.render()
}

// closeReplay exits the timeline overlay.
func (t *TUI) closeReplay() {
	if !t.replayOpen {
		return
	}
	t.replayOpen = false
	t.replayArmed = false
	t.render()
}

// moveReplay shifts the timeline highlight by delta rows.
func (t *TUI) moveReplay(delta int) {
	n := len(t.replayList)
	if n == 0 {
		return
	}
	t.replayIdx += delta
	if t.replayIdx < 0 {
		t.replayIdx = 0
	}
	if t.replayIdx >= n {
		t.replayIdx = n - 1
	}
	t.replayArmed = false // moving disarms a pending rewind confirm
	t.render()
}

// replayStore resolves the checkpoint store for the active session.
func (t *TUI) replayStore() (*checkpoint.Store, string) {
	sessionID := ""
	if t.callback != nil {
		sessionID = t.callback.SessionID()
	}
	if sessionID == "" {
		return nil, ""
	}
	store, err := checkpoint.GetOrOpen(sessionID)
	if err != nil {
		return nil, sessionID
	}
	return store, sessionID
}

// replayViewStep shows the diff introduced by the highlighted checkpoint —
// "what happened in THIS step" (entries are newest-first, so step i's changes
// are the diff from its older neighbour entries[i+1] to entries[i]).
func (t *TUI) replayViewStep(i int) {
	if i < 0 || i >= len(t.replayList) {
		t.closeReplay()
		return
	}
	store, _ := t.replayStore()
	if store == nil {
		t.add(RoleError, "打开检查点失败。")
		t.closeReplay()
		return
	}
	e := t.replayList[i]
	t.closeReplay()

	// Oldest checkpoint has no older neighbour — it is the session's starting
	// snapshot, nothing to diff against.
	if i == len(t.replayList)-1 {
		t.add(RoleSystem, fmt.Sprintf("🎬 起点（%s）：%s\n这是本会话最早的检查点（起始快照），没有更早的对照。",
			e.When.Format("01-02 15:04:05"), truncRunes(e.Message, 60)))
		return
	}
	prev := t.replayList[i+1]
	diff, err := store.DiffBetween(context.Background(), prev.Hash, e.Hash)
	if err != nil {
		t.add(RoleError, "读取检查点 diff 失败: "+err.Error())
		return
	}
	if strings.TrimSpace(diff) == "" {
		t.add(RoleSystem, fmt.Sprintf("🎬 第 %d 步（%s）：%s\n这一步没有文件改动。",
			len(t.replayList)-1-i, e.When.Format("01-02 15:04:05"), truncRunes(e.Message, 60)))
		return
	}
	t.add(RoleSystem, fmt.Sprintf("🎬 第 %d 步（%s）：%s\n```diff\n%s\n```",
		len(t.replayList)-1-i, e.When.Format("01-02 15:04:05"), truncRunes(e.Message, 60),
		strings.TrimRight(diff, "\n")))
}

// replayRewindTo rolls the workspace back to the highlighted checkpoint.
// First press arms: it prints the diff that WOULD be reverted and asks the
// user to press r again; the second press executes (rewindSteps handles the
// actual revert + preview).
func (t *TUI) replayRewindTo(i int) {
	if i < 0 || i >= len(t.replayList) {
		return
	}
	if i == 0 {
		t.add(RoleSystem, "已在此检查点（最新状态），无需回滚。")
		t.replayArmed = false
		t.render()
		return
	}
	if !t.replayArmed {
		// Arm: preview what would be reverted (HEAD relative to the target).
		store, _ := t.replayStore()
		if store != nil {
			if diff, err := store.DiffBetween(context.Background(), t.replayList[i].Hash, t.replayList[0].Hash); err == nil && strings.TrimSpace(diff) != "" {
				t.add(RoleSystem, fmt.Sprintf("⏪ 将回滚到「%s」（撤销 %d 步），以下改动将被撤销:\n```diff\n%s\n```\n再按 r 确认回滚，移动光标取消。",
					truncRunes(t.replayList[i].Message, 40), i, strings.TrimRight(diff, "\n")))
			} else {
				t.add(RoleSystem, fmt.Sprintf("⏪ 将回滚到「%s」（撤销 %d 步，无文件改动）。再按 r 确认。",
					truncRunes(t.replayList[i].Message, 40), i))
			}
		}
		t.replayArmed = true
		t.render()
		return
	}
	// Confirmed: execute the rewind (steps = i rolls HEAD back i entries).
	t.replayArmed = false
	t.closeReplay()
	t.rewindSteps(i)
}

// replayOverlay renders the timeline panel: title, entries (with an internal
// scroll window keeping the highlight visible), and the shortcut footer.
func (t *TUI) replayOverlay(W, bodyH int) []string {
	title := t.paint("bold", "⏪ 检查点时间轴（↑/↓ 选择 · Enter 查看该步 diff · r 回滚到此 · Esc 退出）：")
	const titleRows = 2 // title + blank
	const footRows = 1
	n := len(t.replayList)
	// Reserve one row for the "↑N 更早 · ↓N 更新" indicator so the overlay
	// never exceeds bodyH (overflow would push the footer off-screen).
	maxRows := bodyH - titleRows - footRows - 1
	if maxRows < 1 {
		maxRows = 1
	}
	if t.replayIdx < t.replayTop {
		t.replayTop = t.replayIdx
	}
	if t.replayIdx >= t.replayTop+maxRows {
		t.replayTop = t.replayIdx - maxRows + 1
	}
	if t.replayTop > n-maxRows {
		t.replayTop = n - maxRows
	}
	if t.replayTop < 0 {
		t.replayTop = 0
	}

	lines := []string{title, ""}
	for i := t.replayTop; i < t.replayTop+maxRows && i < n; i++ {
		e := t.replayList[i]
		when := e.When.Format("01-02 15:04:05")
		msg := truncRunes(e.Message, 44)
		row := fmt.Sprintf("  #%-4d %s  %s", len(t.replayList)-1-i, when, msg)
		if i == t.replayIdx {
			row = t.paint("green", "▶ ") + fmt.Sprintf("#%-4d %s  %s", len(t.replayList)-1-i, when, msg)
		}
		lines = append(lines, row)
	}
	if n > maxRows {
		above := t.replayTop
		below := n - (t.replayTop + maxRows)
		lines = append(lines, t.paint("dim", fmt.Sprintf("  ↑ %d 更早  ·  ↓ %d 更新  (共 %d 个检查点)", above, below, n)))
	} else {
		lines = append(lines, "")
	}
	foot := t.paint("dim", "  Enter: 单步 diff · r: 回滚到此"+map[bool]string{true: "（已装填，再按 r 确认）", false: ""}[t.replayArmed])
	lines = append(lines, foot)
	return lines
}

// truncRunes shortens s to at most n runes with an ellipsis.
func truncRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
