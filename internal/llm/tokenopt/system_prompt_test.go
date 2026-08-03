package tokenopt

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestSetSystemPrompt_UpdatesPrefix(t *testing.T) {
	o := New(Config{ModelInfo: types.ModelInfo{ID: "m", Provider: "p"}, SystemPrompt: "base prompt"})

	prefix := o.BuildPrefix()
	if !strings.Contains(prefix, "base prompt") {
		t.Fatalf("prefix should contain initial system prompt")
	}

	o.SetSystemPrompt("base prompt\n\nCURRENT GOAL: ship it")
	prefix = o.BuildPrefix()
	if !strings.Contains(prefix, "CURRENT GOAL: ship it") {
		t.Fatalf("prefix should reflect updated system prompt")
	}
}

func TestSetSystemPrompt_NoOpWhenUnchanged(t *testing.T) {
	o := New(Config{ModelInfo: types.ModelInfo{ID: "m", Provider: "p"}, SystemPrompt: "base"})

	o.SetSystemPrompt("base")
	o.SetSystemPrompt("base")
	o.SetSystemPrompt("base")
	prefix := o.BuildPrefix()
	if strings.Count(prefix, "base") != 1 {
		t.Fatalf("unchanged SetSystemPrompt should not duplicate the prompt")
	}
}

func TestSetSystemPrompt_IgnoresEmpty(t *testing.T) {
	o := New(Config{ModelInfo: types.ModelInfo{ID: "m", Provider: "p"}, SystemPrompt: "keep me"})
	o.SetSystemPrompt("")
	if !strings.Contains(o.BuildPrefix(), "keep me") {
		t.Fatalf("empty SetSystemPrompt must be ignored")
	}
}
