package permission

import "testing"

// Param-level rules must override every other decision path: hard deny beats
// YOLO auto-approval, hard ask beats session allow-all, and first match wins.
func TestParamRulesHardDenyBeatsYOLO(t *testing.T) {
	g := NewGate(ModeYOLO)
	g.SetParamRules([]ParamRule{
		{Pattern: "Bash(git push:*)", Decision: DecisionDeny},
	})
	res := g.Check("s1", Action{Tool: "bash", Command: "git push origin main"})
	if res.Decision != DecisionDeny {
		t.Fatalf("decision = %q, want deny (rule overrides YOLO)", res.Decision)
	}
}

func TestParamRulesHardAskOverridesSessionAllow(t *testing.T) {
	g := NewGate(ModeYOLO)
	g.SetSessionAllow("s1", true)
	g.SetParamRules([]ParamRule{
		{Pattern: "Bash(rm -rf:*)", Decision: DecisionAsk},
	})
	res := g.Check("s1", Action{Tool: "bash", Command: "rm -rf ./build"})
	if res.Decision != DecisionAsk {
		t.Fatalf("decision = %q, want ask even with session allow-all", res.Decision)
	}
}

func TestParamRulesAllowPath(t *testing.T) {
	g := NewGate(ModeAgent)
	g.SetParamRules([]ParamRule{
		{Pattern: "Edit(**.md)", Decision: DecisionAllow},
	})
	res := g.Check("s1", Action{Tool: "edit", Path: "docs/readme.md"})
	if res.Decision != DecisionAllow {
		t.Fatalf("decision = %q, want allow for .md edit", res.Decision)
	}
	// Non-matching tool calls still fall through to the normal flow (ask).
	res2 := g.Check("s1", Action{Tool: "edit", Path: "main.go"})
	if res2.Decision != DecisionAsk {
		t.Fatalf("decision = %q, want ask fall-through for non-match", res2.Decision)
	}
}

func TestParamRulesFirstMatchWins(t *testing.T) {
	g := NewGate(ModeYOLO)
	g.SetParamRules([]ParamRule{
		{Pattern: "Bash(git push --force:*)", Decision: DecisionDeny},
		{Pattern: "Bash(git push:*)", Decision: DecisionAsk},
	})
	deny := g.Check("s1", Action{Tool: "bash", Command: "git push --force origin"})
	if deny.Decision != DecisionDeny {
		t.Fatalf("force-push = %q, want deny (first match)", deny.Decision)
	}
	ask := g.Check("s1", Action{Tool: "bash", Command: "git push origin main"})
	if ask.Decision != DecisionAsk {
		t.Fatalf("normal push = %q, want ask (second rule)", ask.Decision)
	}
}

func TestParamRulesNonBashTools(t *testing.T) {
	g := NewGate(ModeAuto)
	g.SetParamRules([]ParamRule{
		{Pattern: "mcp_call(*)", Decision: DecisionAsk},
	})
	res := g.Check("s1", Action{Tool: "mcp_call", Arguments: `{"server":"db","tool":"query"}`})
	if res.Decision != DecisionAsk {
		t.Fatalf("mcp_call = %q, want ask via mcp_call(*) rule", res.Decision)
	}
}
