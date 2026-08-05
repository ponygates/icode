package tokenopt

import (
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// TestReplaceMessages_BindsBudget verifies the hard /budget fix: after a
// TrimToBudget drops older turns, ReplaceMessages must bring the cached
// optimizer's log in line so CompactRequest no longer replays the full
// transcript. Regression for A2 where the trim result was ignored once the
// optimizer was cached.
func TestReplaceMessages_BindsBudget(t *testing.T) {
	o := New(Config{ModelInfo: types.ModelInfo{ID: "m", Provider: "p", ContextWindow: 128000}, SystemPrompt: "sys"})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "old turn one"})
	o.AddMessage(types.Message{Role: types.RoleAssistant, Content: "old reply one"})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "old turn two"})
	o.AddMessage(types.Message{Role: types.RoleAssistant, Content: "old reply two"})

	// The budget trim keeps only the newest messages.
	trimmed := []types.Message{
		{Role: types.RoleUser, Content: "recent turn"},
		{Role: types.RoleAssistant, Content: "recent reply"},
	}
	o.ReplaceMessages(trimmed)

	out := o.CompactRequest("new input")
	foundOld := false
	for _, m := range out {
		if m.Content == "old turn one" || m.Content == "old reply two" {
			foundOld = true
		}
	}
	if foundOld {
		t.Fatalf("ReplaceMessages did not drop pre-trim messages: %+v", out)
	}

	foundNew := false
	for _, m := range out {
		if m.Content == "recent turn" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatalf("ReplaceMessages did not keep trimmed messages: %+v", out)
	}
}
