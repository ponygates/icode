package tool

// Structured background-task snapshots for the desktop Agents view
// (GET /api/bgtasks). Unlike the plain-text List*TaskLines used by the TUI,
// BgTaskInfo carries typed fields the React UI can render directly.

import (
	"sort"
	"time"
)

// BgTaskInfo is a point-in-time snapshot of one background task — a shell
// command (bg-N) or a background sub-agent (agt-N).
type BgTaskInfo struct {
	ID      string `json:"id"`      // "bg-1" / "agt-1"
	Kind    string `json:"kind"`    // "shell" | "agent"
	Status  string `json:"status"`  // "running" | "finished" | "failed" | "cancelled"
	Elapsed int64  `json:"elapsed"` // seconds since start
	Label   string `json:"label"`   // shell command or agent name
	Brief   string `json:"brief"`   // agent prompt excerpt ("" for shell)
	Tokens  int    `json:"tokens"`  // agent token usage (0 for shell)
	Tail    string `json:"tail"`    // last ~2 KiB of output, rune-safe
}

// maxTailBytes bounds the output preview sent to the desktop per task.
const maxTailBytes = 2 << 10

// tailStr returns at most n bytes from the end of s, cut on a rune boundary
// so Chinese text is never sliced mid-character.
func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !isRuneStart(s[cut]) {
		cut++
	}
	return s[cut:]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// ListBgTaskInfos returns a structured snapshot of every background task
// (shell + agent). Running tasks come first, then the rest, each group
// newest-start first.
func ListBgTaskInfos() []BgTaskInfo {
	infos := make([]BgTaskInfo, 0, len(bgTasks.tasks)+len(agentBGTasks.tasks))

	agentBGTasks.mu.Lock()
	for _, t := range agentBGTasks.tasks {
		output, tokens, done, errMsg, elapsed := t.snapshot()
		info := BgTaskInfo{
			ID:      t.id,
			Kind:    "agent",
			Status:  bgStatus(done, errMsg, t.isCancelled()),
			Elapsed: int64(elapsed.Round(time.Second) / time.Second),
			Label:   t.name,
			Brief:   t.brief,
			Tokens:  tokens,
			Tail:    tailStr(output, maxTailBytes),
		}
		infos = append(infos, info)
	}
	agentBGTasks.mu.Unlock()

	bgTasks.mu.Lock()
	for _, t := range bgTasks.tasks {
		output, done, errMsg, elapsed := t.snapshot()
		t.mu.Lock()
		cancelled := t.cancelled
		t.mu.Unlock()
		info := BgTaskInfo{
			ID:      t.id,
			Kind:    "shell",
			Status:  bgStatus(done, errMsg, cancelled),
			Elapsed: int64(elapsed.Round(time.Second) / time.Second),
			Label:   t.command,
			Tail:    tailStr(output, maxTailBytes),
		}
		infos = append(infos, info)
	}
	bgTasks.mu.Unlock()

	// Stable, useful ordering: running first, then newest start first.
	sort.SliceStable(infos, func(i, j int) bool {
		ri, rj := infos[i].Status == "running", infos[j].Status == "running"
		if ri != rj {
			return ri
		}
		return infos[i].ID > infos[j].ID
	})
	return infos
}

// bgStatus maps the raw task state to the four UI statuses. Cancelled wins
// over failed so a user kill never shows up as an error.
func bgStatus(done bool, errMsg string, cancelled bool) string {
	if !done {
		return "running"
	}
	if cancelled {
		return "cancelled"
	}
	if errMsg != "" {
		return "failed"
	}
	return "finished"
}

// CancelShellTask cancels a running background shell task by id. Returns
// false when the id is unknown or the task already finished.
func CancelShellTask(id string) bool {
	task := bgTasks.Get(id)
	if task == nil {
		return false
	}
	task.mu.Lock()
	done := task.done
	task.mu.Unlock()
	if done {
		return false
	}
	task.cancelTask()
	return true
}
