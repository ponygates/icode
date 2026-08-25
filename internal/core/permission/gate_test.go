package permission

import (
	"path/filepath"
	"testing"
)

// Claude Code settings.json rules must apply in Agent mode: allow patterns
// auto-approve, deny patterns always reject, and deny wins over allow.
func TestClaudeSettingsInAgentMode(t *testing.T) {
	g := NewGate(ModeAgent)
	g.SetClaudeSettings(&ClaudeSettings{Permissions: ClaudePermissions{
		Allow: []string{"Bash(git status:*)", "Edit(**.md)"},
		Deny:  []string{"Bash(rm *)", "*(*:*)", "Read"},
	}})

	// Bash "git status" allowed by pattern.
	if res := g.Check("s1", Action{Tool: "bash", Command: "git status --short"}); res.Decision != DecisionAllow {
		t.Errorf("Bash(git status:*) should allow, got %s (%s)", res.Decision, res.Reason)
	}
	// Bash "rm -rf x" denied by pattern (even though allow would match "git status" — it doesn't).
	if res := g.Check("s1", Action{Tool: "bash", Command: "rm -rf x"}); res.Decision != DecisionDeny {
		t.Errorf("Bash(rm *) should deny, got %s", res.Decision)
	}
	// Edit on .md path allowed.
	if res := g.Check("s1", Action{Tool: "edit", Path: "docs/readme.md"}); res.Decision != DecisionAllow {
		t.Errorf("Edit(**.md) should allow, got %s", res.Decision)
	}
	// Edit on a .go path: allow pattern doesn't match → normal Agent flow asks.
	if res := g.Check("s1", Action{Tool: "edit", Path: "src/main.go"}); res.Decision != DecisionAsk {
		t.Errorf("unmatched edit should fall through to ask, got %s", res.Decision)
	}
}

// Claude settings must NOT affect other modes (YOLO stays YOLO).
func TestClaudeSettingsIgnoredInYOLO(t *testing.T) {
	g := NewGate(ModeYOLO)
	g.SetClaudeSettings(&ClaudeSettings{Permissions: ClaudePermissions{
		Deny: []string{"Bash(*)"},
	}})
	if res := g.Check("s1", Action{Tool: "bash", Command: "ls"}); res.Decision != DecisionAllow {
		t.Errorf("YOLO mode must ignore Claude settings, got %s", res.Decision)
	}
}

func TestClaudeAllowMatch(t *testing.T) {
	cases := []struct {
		action  Action
		pattern string
		want    bool
	}{
		{Action{Tool: "bash", Command: "git status"}, "Bash(git status:*)", true},
		{Action{Tool: "bash", Command: "git diff"}, "Bash(git status:*)", false},
		{Action{Tool: "edit", Path: "a.md"}, "Edit(**.md)", true},
		{Action{Tool: "edit", Path: "a.go"}, "Edit(**.md)", false},
		{Action{Tool: "read_file", Path: "x.go"}, "*", true},
		{Action{Tool: "bash", Command: "anything"}, "*", true},
		{Action{Tool: "fetch", URL: "https://example.com"}, "Fetch(example.com*)", true},
		{Action{Tool: "bash", Command: "ls"}, "Bash(ls*)", true},
		{Action{Tool: "bash", Command: "pwd"}, "Bash(ls*)", false},
		{Action{Tool: "grep", Path: "/proj", Pattern: "foo"}, "Grep(/proj*foo)", true},
	}
	for _, c := range cases {
		if got := claudeAllowMatch(c.action, c.pattern); got != c.want {
			t.Errorf("claudeAllowMatch(%+v, %q) = %v, want %v", c.action, c.pattern, got, c.want)
		}
	}
}

// The AllowedPaths sandbox must contain EVERY file tool, not just bash, so an
// agent cannot escape the workspace with write_file/edit/read_file in YOLO mode.
func TestAllowedPathsContainmentForFileTools(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "src", "main.go")
	outside := filepath.Join(base, "..", "escape.txt")

	g := NewGate(ModeYOLO)
	g.SetAllowedPaths([]string{base})

	for _, tool := range []string{"read_file", "write_file", "edit", "ls", "grep"} {
		// Inside the sandbox → allowed in YOLO mode.
		insideRes := g.Check("s1", Action{Tool: tool, Path: inside})
		if insideRes.Decision != DecisionAllow {
			t.Errorf("%s inside sandbox → %s, want allow", tool, insideRes.Decision)
		}

		// Outside the sandbox → denied, even in YOLO mode.
		outsideRes := g.Check("s1", Action{Tool: tool, Path: outside})
		if outsideRes.Decision != DecisionDeny {
			t.Errorf("%s outside sandbox → %s, want deny", tool, outsideRes.Decision)
		}
	}
}

// The allowlist prefix must match on a path boundary: /home/u/proj must not
// implicitly allow /home/u/project2.
func TestAllowedPathsBoundary(t *testing.T) {
	parent := t.TempDir()
	proj := filepath.Join(parent, "proj")
	sibling := filepath.Join(parent, "project2", "x.txt")

	g := NewGate(ModeYOLO)
	g.SetAllowedPaths([]string{proj})

	if res := g.Check("s1", Action{Tool: "write_file", Path: sibling}); res.Decision != DecisionDeny {
		t.Errorf("sibling dir under same prefix → %s, want deny", res.Decision)
	}
	if res := g.Check("s1", Action{Tool: "write_file", Path: filepath.Join(proj, "ok.txt")}); res.Decision != DecisionAllow {
		t.Errorf("file directly under allowed dir → %s, want allow", res.Decision)
	}
}

// Empty allowlist = no containment (previous behaviour preserved).
func TestAllowedPathsEmptyMeansNoRestriction(t *testing.T) {
	g := NewGate(ModeYOLO)
	if res := g.Check("s1", Action{Tool: "write_file", Path: filepath.Join(t.TempDir(), "..", "x")}); res.Decision != DecisionAllow {
		t.Errorf("empty allowlist should not restrict, got %s", res.Decision)
	}
}

// Non-path tools (e.g. todo_write) must not be affected by the sandbox.
func TestAllowedPathsIgnoresNonFileTools(t *testing.T) {
	g := NewGate(ModeYOLO)
	g.SetAllowedPaths([]string{t.TempDir()})
	if res := g.Check("s1", Action{Tool: "todo_write"}); res.Decision != DecisionAllow {
		t.Errorf("non-path tool affected by sandbox: %s", res.Decision)
	}
}

// Four-tier permission classification (本书 ch.22).
func TestAccessLevelOf(t *testing.T) {
	cases := map[string]AccessLevel{
		"read_file":      AccessRead,
		"grep":           AccessRead,
		"git_diff":       AccessRead,
		"write_file":     AccessWrite,
		"edit":           AccessWrite,
		"search_replace": AccessWrite,
		"bash":           AccessExecute,
		"git_commit":     AccessExecute,
		"fetch":          AccessConnect,
		"web_search":     AccessConnect,
		"image_gen":      AccessConnect,
		"unknown_tool":   AccessRead,
	}
	for tool, want := range cases {
		if got := AccessLevelOf(tool); got != want {
			t.Errorf("AccessLevelOf(%q) = %v, want %v", tool, got, want)
		}
	}
}

// In Auto mode, Connect-tier tools (fetch/web_search) reach external
// networks, so they must be asked rather than silently auto-approved even
// though they are logically read-only.
func TestAutoModeConnectToolsAsk(t *testing.T) {
	g := NewGate(ModeAuto)
	for _, tool := range []string{"fetch", "web_search"} {
		if res := g.Check("s1", Action{Tool: tool, URL: "https://example.com"}); res.Decision != DecisionAsk {
			t.Errorf("Auto mode %s → %s, want ask (Connect tier)", tool, res.Decision)
		}
	}
	// Read tier still auto-approves.
	if res := g.Check("s1", Action{Tool: "grep", Path: "."}); res.Decision != DecisionAllow {
		t.Errorf("Auto mode grep → %s, want allow", res.Decision)
	}
}

// After a user approves a Connect-tier destination once, Auto mode remembers
// the host and silently auto-approves subsequent fetches to it — the
// "静默白名单" behaviour (本书 ch.22). Other hosts still ask.
func TestAutoModeConnectDomainWhitelist(t *testing.T) {
	g := NewGate(ModeAuto)

	g.TrustDomain("https://example.com/docs/page")
	if !g.IsDomainTrusted("example.com") {
		t.Fatal("example.com should be trusted after approval")
	}

	if res := g.Check("s1", Action{Tool: "fetch", URL: "https://example.com/other"}); res.Decision != DecisionAllow {
		t.Errorf("fetch to trusted host → %s, want allow (silent whitelist)", res.Decision)
	}
	if res := g.Check("s1", Action{Tool: "fetch", URL: "https://example.org/doc"}); res.Decision != DecisionAsk {
		t.Errorf("fetch to untrusted host → %s, want ask", res.Decision)
	}

	// Untrusting restores the prompt.
	g.UntrustDomain("https://example.com/")
	if g.IsDomainTrusted("example.com") {
		t.Fatal("example.com should be untrusted after UntrustDomain")
	}
	if res := g.Check("s1", Action{Tool: "fetch", URL: "https://example.com/x"}); res.Decision != DecisionAsk {
		t.Errorf("fetch after untrust → %s, want ask", res.Decision)
	}
}

// The whitelist matches the host, not the path, and is case-insensitive.
func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://Example.COM/path?a=b":  "example.com",
		"http://api.example.com:8080/x": "api.example.com",
		"example.org":                   "example.org",
		"user:pass@sub.host.io:9090/p":  "sub.host.io",
		"not a url":                     "",
		"":                              "",
	}
	for in, want := range cases {
		if got := HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStrikeEscalation(t *testing.T) {
	g := NewGate(ModeAuto)
	g.SetStrikeThreshold(3)

	// Allow decisions reset the counter.
	for i := 0; i < 5; i++ {
		g.Check("s1", Action{Tool: "read_file", Arguments: `{"path":"a"}`})
	}
	// Three consecutive asks/denies → escalate.
	act := Action{Tool: "bash", Arguments: `{"command":"echo hi"}`}
	var escalated *CheckResult
	for i := 0; i < 3; i++ {
		r := g.Check("s1", act)
		if r.Escalated {
			escalated = &r
		}
	}
	if escalated == nil {
		t.Fatal("expected escalation after 3 consecutive blocks")
	}
	// After escalation, every action (even read-only) asks.
	r := g.Check("s1", Action{Tool: "read_file", Arguments: `{"path":"a"}`})
	if r.Decision != DecisionAsk {
		t.Fatalf("escalated session should force ask, got %s", r.Decision)
	}
	// Other sessions are unaffected.
	r2 := g.Check("s2", Action{Tool: "read_file", Arguments: `{"path":"a"}`})
	if r2.Decision != DecisionAllow {
		t.Fatalf("other session should still allow read, got %s", r2.Decision)
	}
}
