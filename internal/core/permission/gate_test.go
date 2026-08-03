package permission

import (
	"path/filepath"
	"testing"
)

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
