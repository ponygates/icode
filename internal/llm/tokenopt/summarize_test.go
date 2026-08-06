package tokenopt

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// TestSummarizeStructuredSegments verifies that summarizeLocked fills the
// 9-segment template deterministically from concrete message text rather than
// delegating to free-form model summarization (book ch.25 principle).
func TestSummarizeStructuredSegments(t *testing.T) {
	// goal + progress (index 0-1) must be compacted; tool signals (file &
	// blocker) and tool counts must be extracted deterministically. 11 total
	// messages => keepFrom = len-6 = 5, so index 0-4 fold into the summary.
	o := New(Config{
		ModelInfo:        types.ModelInfo{ID: "m", Provider: "deepseek", ContextWindow: 1_048_576},
		SystemPrompt:     "sys",
		Strategy:         StrategySummarize,
		MaxCompactTokens: 4_000,
	})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "build a chat summary feature"})
	o.AddMessage(types.Message{Role: types.RoleAssistant, Content: "I will start by editing the optimizer"})
	o.AddMessage(types.Message{Role: types.RoleTool, Content: "Edited optimizer.go"})
	o.AddMessage(types.Message{Role: types.RoleTool, Content: "Edited summarize_test.go"})
	o.AddMessage(types.Message{Role: types.RoleTool, Content: "FAIL: TestSummarizeStructuredSegments failed"})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "keep going"})
	o.AddMessage(types.Message{Role: types.RoleAssistant, Content: "more progress text"})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "and another correction"})
	o.AddMessage(types.Message{Role: types.RoleTool, Content: "PASS: TestSummarizeStructuredSegments ok"})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: strings.Repeat("x", 500)})
	// Push over the compact threshold using a large final message.
	o.AddMessage(types.Message{Role: types.RoleUser, Content: strings.Repeat("b", 60_000)})

	if !o.ShouldCompact() {
		t.Fatal("ShouldCompact should be true")
	}
	o.CompactRequest("")

	s := o.CompactionSummary()
	if s == "" {
		t.Fatal("expected non-empty compaction summary")
	}

	// Goal segment: the first user turn.
	if !strings.Contains(s, "目标:") || !strings.Contains(s, "build a chat summary feature") {
		t.Errorf("missing goal segment, got:\n%s", s)
	}
	// Files segment: derived from Edited tool results.
	if !strings.Contains(s, "已改动文件:") || !strings.Contains(s, "optimizer.go") {
		t.Errorf("missing/garbled files segment, got:\n%s", s)
	}
	// Blockers segment: derived from FAIL tool result.
	if !strings.Contains(s, "已知坑:") || !strings.Contains(s, "TestSummarizeStructuredSegments failed") {
		t.Errorf("missing blockers segment, got:\n%s", s)
	}
	// Progress segment: from assistant/user text.
	if !strings.Contains(s, "进展:") {
		t.Errorf("missing progress segment, got:\n%s", s)
	}
	// Tool count segment.
	if !strings.Contains(s, "已执行工具") {
		t.Errorf("missing tool-count segment, got:\n%s", s)
	}
}

// TestSummarizeKeepsRecentMessages verifies compaction drops old messages but
// preserves the most recent turns for continuity.
func TestSummarizeKeepsRecentMessages(t *testing.T) {
	o := New(Config{
		ModelInfo:        types.ModelInfo{ID: "m", Provider: "deepseek", ContextWindow: 1_048_576},
		SystemPrompt:     "sys",
		Strategy:         StrategySummarize,
		MaxCompactTokens: 4_000,
	})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "old goal"})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "latest turn"})

	// Push over the compact threshold.
	o.AddMessage(types.Message{Role: types.RoleUser, Content: strings.Repeat("c", 60_000)})

	got := o.CompactRequest("")
	foundRecent := false
	for _, m := range got {
		if strings.Contains(m.Content, "latest turn") {
			foundRecent = true
			break
		}
	}
	if !foundRecent {
		t.Fatal("expected recent turn to survive compaction in request messages")
	}
}
