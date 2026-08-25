package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryDirUserScope(t *testing.T) {
	// User scope lives under the real home; just verify path shape without
	// touching disk beyond MkdirAll of a temp-named agent.
	name := "memtest-" + t.Name()
	dir, err := MemoryDir(MemoryUser, name, "")
	if err != nil {
		t.Fatalf("MemoryDir: %v", err)
	}
	defer os.RemoveAll(dir)
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".icode", "agent-memory", name)
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(dir, memoryFileName)); err != nil {
		t.Errorf("MEMORY.md not created: %v", err)
	}
}

func TestMemoryDirProjectAndLocal(t *testing.T) {
	proj := t.TempDir()
	pdir, err := MemoryDir(MemoryProject, "explore", proj)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pdir, filepath.Join(proj, ".icode", "agent-memory")) {
		t.Errorf("project dir = %q", pdir)
	}
	ldir, err := MemoryDir(MemoryLocal, "explore", proj)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ldir, "agent-memory-local") {
		t.Errorf("local dir = %q", ldir)
	}
	if _, err := os.Stat(filepath.Join(pdir, memoryFileName)); err != nil {
		t.Error("project MEMORY.md missing")
	}
}

func TestMemoryDirRejectsEmpty(t *testing.T) {
	if _, err := MemoryDir("", "x", ""); err == nil {
		t.Error("empty scope should error")
	}
	if _, err := MemoryDir(MemoryProject, "", ""); err == nil {
		t.Error("empty agent name should error")
	}
}

func TestNormalizeMemoryScope(t *testing.T) {
	cases := map[string]MemoryScope{
		"User":     MemoryUser,
		" project": MemoryProject,
		"LOCAL":    MemoryLocal,
		"":         "",
		"bogus":    "",
	}
	for in, want := range cases {
		if got := NormalizeMemoryScope(in); got != want {
			t.Errorf("NormalizeMemoryScope(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadMemoryHeadTruncatesLinesAndBytes(t *testing.T) {
	proj := t.TempDir()
	dir, err := MemoryDir(MemoryProject, "heady", proj)
	if err != nil {
		t.Fatal(err)
	}
	// 300 short lines — exceeds the 200-line cap.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(filepath.Join(dir, memoryFileName), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	head := LoadMemoryHead(dir)
	if got := strings.Count(head, "\n"); got >= 200 {
		t.Errorf("head has %d lines, want <200", got)
	}
	// Huge single line — exceeds the 25KB byte cap.
	big := strings.Repeat("x", 100*1024)
	if err := os.WriteFile(filepath.Join(dir, memoryFileName), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := len(LoadMemoryHead(dir)); got > 25*1024+2 { // +split/join slack
		t.Errorf("head is %d bytes, want ≤ ~25KB", got)
	}
	// Missing directory → empty, no error.
	if got := LoadMemoryHead(filepath.Join(proj, "nope")); got != "" {
		t.Errorf("missing dir head = %q", got)
	}
}

func TestAgentDefMemoryYAMLParsing(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "remember.md", `---
description: memory tester
memory: local
---
Remember things.
`)
	def, err := loadFile(filepath.Join(dir, "remember.md"))
	if err != nil {
		t.Fatal(err)
	}
	if def.Memory != "local" {
		t.Errorf("Memory = %q, want local", def.Memory)
	}
	// Invalid scopes normalize to disabled (""), never break loading.
	writeAgent(t, dir, "badmem.md", "---\nmemory: bogus\n---\nx")
	def2, err := loadFile(filepath.Join(dir, "badmem.md"))
	if err != nil {
		t.Fatal(err)
	}
	if def2.Memory != "" {
		t.Errorf("bogus scope = %q, want empty", def2.Memory)
	}
}
