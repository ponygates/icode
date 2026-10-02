package tui

import (
	"strings"
)

// ── Slash commands ───────────────────────────────────────────────

// setMode switches the TUI mode AND the backend gate's mode so the displayed
// mode always matches what the permission layer enforces (/mode and
// Shift+Tab both funnel through here).
func (t *TUI) setMode(mode string) {
	t.mode = mode
	if t.callback != nil {
		if msg := t.callback.OnSetMode(mode); msg != "" {
			t.notice(msg)
		}
	}
}

// handleSlash dispatches one submitted "/command <args>" line.
//
// The commands are split into six disjoint domain groups (info, session,
// model, config, workspace, ext); each group reports whether it claimed the
// command and the first claim wins, exactly like the single switch this
// replaced — which had no code after it, so every old `break`/`return` meant
// "dispatch finished". A name that no group claims falls through to
// handleSlashFallback (user-defined .icode/commands + the backend callback).
//
// Matching stays on the lowercased first field and the argument list is the
// remaining whitespace-split fields, so prefix/alias resolution is unchanged.
func (t *TUI) handleSlash(text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])
	args := parts[1:]
	t.noteRecentCmd(cmd)

	if t.handleSlashInfo(cmd, args) {
		return
	}
	if t.handleSlashSession(cmd, args) {
		return
	}
	if t.handleSlashModel(cmd, args) {
		return
	}
	if t.handleSlashConfig(cmd, args) {
		return
	}
	if t.handleSlashWorkspace(cmd, args) {
		return
	}
	if t.handleSlashExt(cmd, args) {
		return
	}
	t.handleSlashFallback(cmd, args)
}
