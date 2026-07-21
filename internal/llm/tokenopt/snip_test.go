package tokenopt

import (
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestSnipFilter_RemoveEmptyAssistant(t *testing.T) {
	filter := NewSnipFilter(SnipConfig{
		RemoveEmptyAssistant: true,
		MinContentLength:     3,
	})

	msgs := []types.Message{
		{Role: types.RoleSystem, Content: "system prompt"},
		{Role: types.RoleUser, Content: "hello"},
		{Role: types.RoleAssistant, Content: "Hi there!"},
		{Role: types.RoleAssistant, Content: "", ToolCalls: nil}, // empty → should be removed
		{Role: types.RoleAssistant, Content: "world"},
	}

	result := filter.Filter(msgs)
	if len(result) != 4 {
		t.Errorf("expected 4 messages after snip, got %d", len(result))
	}

	// System and user should be preserved
	if result[0].Role != types.RoleSystem {
		t.Errorf("first message should be system role")
	}
	if result[1].Role != types.RoleUser {
		t.Errorf("second message should be user role")
	}
}

func TestSnipFilter_RemoveRejectedRounds(t *testing.T) {
	filter := NewSnipFilter(SnipConfig{
		RemoveRejectedRounds: true,
	})

	msgs := []types.Message{
		{Role: types.RoleUser, Content: "fix this"},
		{Role: types.RoleAssistant, Content: "I'll edit", ToolCalls: []types.ToolCall{
			{Name: "edit", Result: &types.ToolResult{Success: false, Error: "denied"}},
		}},
		{Role: types.RoleTool, Content: "permission denied"},
		{Role: types.RoleUser, Content: "do something else"},
	}

	result := filter.Filter(msgs)
	// The rejected round should be removed, leaving only 2 messages
	if len(result) != 2 {
		t.Errorf("expected 2 messages after removing rejected round, got %d: %+v", len(result), result)
	}
	if result[0].Content != "fix this" {
		t.Errorf("first message should be 'fix this', got %q", result[0].Content)
	}
	if result[1].Content != "do something else" {
		t.Errorf("second message should be 'do something else', got %q", result[1].Content)
	}
}

func TestSnipFilter_KeepSuccessfulToolRounds(t *testing.T) {
	filter := NewSnipFilter(SnipConfig{
		RemoveRejectedRounds: true,
	})

	msgs := []types.Message{
		{Role: types.RoleUser, Content: "edit file"},
		{Role: types.RoleAssistant, Content: "editing...", ToolCalls: []types.ToolCall{
			{Name: "edit", Result: &types.ToolResult{Success: true}},
		}},
		{Role: types.RoleTool, Content: "file edited"},
	}

	result := filter.Filter(msgs)
	if len(result) != 3 {
		t.Errorf("expected 3 messages (successful round kept), got %d", len(result))
	}
}

func TestSnipFilter_RemoveBlankToolMessages(t *testing.T) {
	filter := NewSnipFilter(SnipConfig{
		RemoveBlankMessages: true,
		MinContentLength:    3,
	})

	msgs := []types.Message{
		{Role: types.RoleUser, Content: "test"},
		{Role: types.RoleTool, Content: "ok result"},
		{Role: types.RoleTool, Content: ""},
		{Role: types.RoleTool, Content: "results here"},
	}

	result := filter.Filter(msgs)
	if len(result) != 3 {
		t.Errorf("expected 3 messages (1 blank removed), got %d", len(result))
	}
}

func TestSnipFilter_AllDisabled(t *testing.T) {
	filter := NewSnipFilter(SnipConfig{})
	msgs := []types.Message{
		{Role: types.RoleAssistant, Content: ""},
		{Role: types.RoleTool, Content: ""},
	}
	result := filter.Filter(msgs)
	if len(result) != 2 {
		t.Errorf("expected all messages kept when snip is disabled, got %d", len(result))
	}
}

func TestSnipFilter_DefaultConfig(t *testing.T) {
	cfg := DefaultSnipConfig()
	if !cfg.RemoveEmptyAssistant {
		t.Error("default should remove empty assistant messages")
	}
	if !cfg.RemoveRejectedRounds {
		t.Error("default should remove rejected rounds")
	}
	if cfg.MinContentLength != 3 {
		t.Errorf("default MinContentLength should be 3, got %d", cfg.MinContentLength)
	}
}
