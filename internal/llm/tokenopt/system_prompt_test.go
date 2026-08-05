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
		t.Fatalf("empty SetSystemPrompt should not clear the prefix")
	}
}

func TestBuildPrefix_OmitsToolListText(t *testing.T) {
	// Tool defs are sent natively via the provider tools array, so the
	// immutable text prefix must NOT duplicate them (A1 token saving).
	o := New(Config{ModelInfo: types.ModelInfo{ID: "m", Provider: "p"}, SystemPrompt: "base"})
	o.SetTools([]types.ToolDef{
		{Name: "read_file", Description: "Read a file from disk"},
		{Name: "bash", Description: "Run a shell command and return output"},
	})
	prefix := o.BuildPrefix()
	if strings.Contains(prefix, "Available Tools") {
		t.Fatalf("prefix should not contain an Available Tools listing")
	}
	if strings.Contains(prefix, "Read a file from disk") {
		t.Fatalf("prefix should not inline tool descriptions")
	}
}

func TestMaxCompactTokens_Ceiling(t *testing.T) {
	// A 1M-token window with the default 0.8 fraction yields a ~838k threshold
	// — far too large to ever trigger. The effective ceiling (128k) must fire
	// first so long sessions still compact.
	o := New(Config{
		ModelInfo:    types.ModelInfo{ID: "big", Provider: "deepseek", ContextWindow: 1_048_576},
		SystemPrompt: "sys",
		Strategy:     StrategySummarize,
	})
	if o.maxCompactTokens != MaxCompactTokensDefault {
		t.Fatalf("maxCompactTokens = %d, want default %d", o.maxCompactTokens, MaxCompactTokensDefault)
	}
	// One ~520k ASCII message ≈ 130k tokens: above the 128k ceiling, well
	// below the 838k window-fraction threshold.
	o.AddMessage(types.Message{Role: "user", Content: strings.Repeat("a", 520_000)})
	if !o.ShouldCompact() {
		t.Fatal("ShouldCompact should be true once estimated tokens pass the ceiling")
	}
}

func TestMaxCompactTokens_CustomValue(t *testing.T) {
	// A caller may tune the ceiling; the value must be honored verbatim.
	o := New(Config{
		ModelInfo:        types.ModelInfo{ID: "big", Provider: "deepseek", ContextWindow: 1_048_576},
		SystemPrompt:     "sys",
		MaxCompactTokens: 20_000,
		Strategy:         StrategySummarize,
	})
	if o.maxCompactTokens != 20_000 {
		t.Fatalf("maxCompactTokens = %d, want 20000", o.maxCompactTokens)
	}
	// ~30k tokens: below the 838k window fraction, above the tuned 20k.
	o.AddMessage(types.Message{Role: "user", Content: strings.Repeat("a", 120_000)})
	if !o.ShouldCompact() {
		t.Fatal("ShouldCompact should fire at the custom ceiling")
	}
}
