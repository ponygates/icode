package conversation

import (
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func mkMsg(role types.Role, content string) types.Message {
	return types.Message{Role: role, Content: content}
}

func TestForkPrefixEmpty(t *testing.T) {
	if got := forkPrefix(nil); got != nil {
		t.Errorf("forkPrefix(nil) = %v, want nil", got)
	}
}

func TestForkPrefixTailCapped(t *testing.T) {
	var msgs []types.Message
	for i := 0; i < forkPrefixMaxMessages+10; i++ {
		msgs = append(msgs, mkMsg(types.RoleUser, "m"))
	}
	got := forkPrefix(msgs)
	if len(got) != forkPrefixMaxMessages {
		t.Errorf("len = %d, want %d", len(got), forkPrefixMaxMessages)
	}
	// Tail must be the LAST N messages verbatim.
	if got[len(got)-1].Content != msgs[len(msgs)-1].Content {
		t.Error("prefix is not the conversation tail")
	}
}

func TestForkPrefixDropsTrailingToolResults(t *testing.T) {
	msgs := []types.Message{
		mkMsg(types.RoleUser, "hi"),
		mkMsg(types.RoleAssistant, "let me check"),
		mkMsg(types.RoleTool, "result payload"),
		mkMsg(types.RoleTool, "another payload"),
	}
	got := forkPrefix(msgs)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (tool tail dropped)", len(got))
	}
	if got[len(got)-1].Role != types.RoleAssistant {
		t.Errorf("last role = %q, want assistant", got[len(got)-1].Role)
	}
	// Tool results in the MIDDLE are kept verbatim.
	msgs2 := append([]types.Message{}, msgs...)
	msgs2 = append(msgs2, mkMsg(types.RoleUser, "thanks"))
	got2 := forkPrefix(msgs2)
	if len(got2) != 5 {
		t.Errorf("len = %d, want 5 (mid tool result kept, tail is user)", len(got2))
	}
}

func TestForkPrefixCopiesSlice(t *testing.T) {
	msgs := []types.Message{mkMsg(types.RoleUser, "a")}
	got := forkPrefix(msgs)
	got[0].Content = "mutated"
	if msgs[0].Content == "mutated" {
		t.Error("forkPrefix must not alias the input slice")
	}
}
