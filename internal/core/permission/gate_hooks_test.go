package permission

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// mustArgs builds a tool-call arguments JSON payload from a string map, so
// tests never hand-roll escaping.
func mustArgs(t *testing.T, m map[string]string) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return string(b)
}

// TestTruncateRuneSafe locks the fix for the byte-truncation bug: CJK text is
// 3 bytes per rune, so the old s[:n] cut landed mid-rune and produced mojibake
// (invalid UTF-8 / replacement chars) in the approval preview.
func TestTruncateRuneSafe(t *testing.T) {
	cjk := strings.Repeat("中", 100) // 300 bytes, 100 runes
	for _, n := range []int{1, 5, 40, 80, 120} {
		got := truncate(cjk, n)
		if !utf8.ValidString(got) {
			t.Fatalf("truncate(cjk, %d) produced invalid UTF-8: %q", n, got)
		}
		if strings.ContainsRune(got, '�') {
			t.Fatalf("truncate(cjk, %d) contains a replacement char: %q", n, got)
		}
		wantRunes := n
		if wantRunes > 100 {
			wantRunes = 100
		}
		// n runes + the "..." suffix
		if got != cjk[:wantRunes*3]+"..." && wantRunes < 100 {
			t.Fatalf("truncate(cjk, %d) = %q, want %d runes + ellipsis", n, got, wantRunes)
		}
	}
	// Short input passes through untouched.
	if got := truncate("短文本", 80); got != "短文本" {
		t.Fatalf("short input should pass through, got %q", got)
	}
	// Mixed content stays valid too.
	mixed := "配置 config 值 = 42；" + strings.Repeat("内容", 60)
	if got := truncate(mixed, 50); !utf8.ValidString(got) {
		t.Fatalf("truncate(mixed) produced invalid UTF-8: %q", got)
	}
}

// TestBuildPromptWriteFilePreservesLayout locks the fix for the
// strings.Fields whitespace-collapse bug: the write_file approval excerpt must
// keep newlines and indentation so the user can judge a code change from the
// preview instead of guessing at a whitespace-flattened blob.
func TestBuildPromptWriteFilePreservesLayout(t *testing.T) {
	g := &Gate{}
	content := "func main() {\n\tfmt.Println(\"hello\")\n}\n"
	args := mustArgs(t, map[string]string{"content": content})
	prompt := g.buildPrompt(Action{Tool: "write_file", Path: "main.go", Arguments: args})

	if !strings.Contains(prompt, "写入文件: main.go") {
		t.Fatalf("prompt missing file header: %q", prompt)
	}
	if !strings.Contains(prompt, "func main() {") {
		t.Fatalf("prompt lost the first line of the excerpt: %q", prompt)
	}
	// The indented line must keep its indentation — the old Fields join
	// flattened it to "fmt.Println(\"hello\")" with no leading tab.
	if !strings.Contains(prompt, "\n\tfmt.Println") {
		t.Fatalf("prompt lost the excerpt indentation: %q", prompt)
	}
}

// TestBuildPromptWriteFileOmitsLongTail verifies the multi-line excerpt caps
// at a few lines with an explicit omission note, so a 500-line write still
// renders a reviewable preview.
func TestBuildPromptWriteFileOmitsLongTail(t *testing.T) {
	g := &Gate{}
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "\tline "+strings.Repeat("x", 20))
	}
	content := strings.Join(lines, "\n")
	args := mustArgs(t, map[string]string{"content": content})
	prompt := g.buildPrompt(Action{Tool: "write_file", Path: "gen.go", Arguments: args})

	if !strings.Contains(prompt, "已省略后续行") {
		t.Fatalf("long excerpt missing the omission note: %q", prompt)
	}
	if strings.Contains(prompt, "line 49") {
		// The body lines are "line xxxx...", not numbered — this check just
		// guards against the full body leaking in; a real leak is caught by
		// counting rendered lines below.
	}
	// Count rendered excerpt lines: 6 kept + 1 omission note.
	gotLines := strings.Count(prompt, "line ")
	if gotLines > 6 {
		t.Fatalf("excerpt kept %d body lines, want ≤ 6: %q", gotLines, prompt)
	}
}

// TestBuildPromptEditCJK verifies the edit preview truncates Chinese old/new
// excerpts without producing mojibake (the old byte cut could split a rune).
func TestBuildPromptEditCJK(t *testing.T) {
	g := &Gate{}
	oldS := strings.Repeat("旧", 60) // 180 bytes
	newS := strings.Repeat("新", 60) // 180 bytes
	args := mustArgs(t, map[string]string{"old_string": oldS, "new_string": newS})
	prompt := g.buildPrompt(Action{Tool: "edit", Path: "notes.md", Arguments: args})
	if !utf8.ValidString(prompt) {
		t.Fatalf("edit prompt contains invalid UTF-8: %q", prompt)
	}
	if strings.ContainsRune(prompt, '�') {
		t.Fatalf("edit prompt contains a replacement char: %q", prompt)
	}
	if !strings.Contains(prompt, "编辑文件: notes.md") {
		t.Fatalf("edit prompt missing header: %q", prompt)
	}
}

// TestBuildPromptEditDiffStyle locks the unified-diff preview format: removed
// lines carry the "- " prefix and added lines "+ ", which the TUI colours red
// and green respectively.
func TestBuildPromptEditDiffStyle(t *testing.T) {
	g := &Gate{}
	args := mustArgs(t, map[string]string{
		"old_string": "fmt.Println(\"hi\")",
		"new_string": "log.Printf(\"hi\")\nlog.Printf(\"bye\")",
	})
	prompt := g.buildPrompt(Action{Tool: "edit", Path: "main.go", Arguments: args})
	if !strings.Contains(prompt, "\n- fmt.Println(\"hi\")") {
		t.Fatalf("missing '- ' removed-line prefix: %q", prompt)
	}
	if !strings.Contains(prompt, "\n+ log.Printf(\"hi\")") {
		t.Fatalf("missing '+ ' added-line prefix: %q", prompt)
	}
	if !strings.Contains(prompt, "\n+ log.Printf(\"bye\")") {
		t.Fatalf("second added line lost: %q", prompt)
	}
	// The opaque old format must be gone.
	if strings.Contains(prompt, "改为") {
		t.Fatalf("edit prompt still uses the old quoted-string format: %q", prompt)
	}
}
