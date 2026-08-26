package permission

import "testing"

// Refusals must read like explanations, not error codes (源码解析 ch.5:
// "拒绝至少要告诉用户原因，且是人能理解的自然语言").
func TestHumanizeDeny(t *testing.T) {
	cases := []struct {
		name    string
		reason  string
		contain []string // all must appear
	}{
		{"param rule", "参数级规则命中 Bash(git push:*)", []string{"硬性规则", "Bash(git push:*)", "[permission.rules]"}},
		{"claude settings", "Denied by .claude/settings.json: Bash(rm:*)", []string{".claude/settings.json", "deny"}},
		{"hooks", "Denied by hooks rule: sudo", []string{"hooks.yaml"}},
		{"deny list", "Agent mode deny list: 命令命中危险模式 \"rm -rf /\"", []string{"危险操作清单", "拆解"}},
		{"user tool rule", "Tool denied by user rule", []string{"总是拒绝", "/permissions"}},
		{"escalated", "手动模式：连续拦截后已退回人工确认", []string{"手动模式", "保护行为"}},
	}
	for _, tc := range cases {
		got := HumanizeDeny(Action{Tool: "bash"}, tc.reason)
		for _, want := range tc.contain {
			if !contains(got, want) {
				t.Errorf("%s: output missing %q:\n%s", tc.name, want, got)
			}
		}
		if !contains(got, "⛔") {
			t.Errorf("%s: missing interception marker", tc.name)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestExplainDenySandboxVsDenylist(t *testing.T) {
	g := NewGate(ModeAuto)
	g.SetAllowedPaths([]string{t.TempDir()})

	// Path outside sandbox → sandbox cause, not the generic deny list.
	outside := Action{Tool: "read_file", Path: "C:\\Windows\\system32\\config"}
	if !g.isDenied(outside) {
		t.Fatal("outside-sandbox action should be denied")
	}
	if got := g.explainDeny(outside); !contains(got, "沙箱") {
		t.Errorf("sandbox cause = %q", got)
	}

	// Dangerous bash inside the workspace → deny-list cause.
	danger := Action{Tool: "bash", Command: "rm -rf /"}
	if !g.isDenied(danger) {
		t.Fatal("dangerous command should be denied")
	}
	if got := g.explainDeny(danger); contains(got, "沙箱") {
		t.Errorf("deny-list cause wrongly mentions sandbox: %q", got)
	}
}
