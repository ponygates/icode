package xgo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterFilterApplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.log")
	w, err := NewRotatingWriter(path, 0)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	w.SetFilter(func(p []byte) []byte {
		return []byte(strings.ReplaceAll(string(p), "secret", "XXXX"))
	})
	if _, err := w.Write([]byte("a secret line\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), "XXXX") {
		t.Fatalf("filter not applied, file = %q", raw)
	}
}

func TestRotatingWriterWithoutFilterIsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.log")
	w, err := NewRotatingWriter(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("verbatim\n")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	raw, _ := os.ReadFile(path)
	if string(raw) != "verbatim\n" {
		t.Fatalf("file = %q, want verbatim", raw)
	}
}

func TestSetFilterClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clear.log")
	w, _ := NewRotatingWriter(path, 0)
	w.SetFilter(func([]byte) []byte { return []byte("nope") })
	w.SetFilter(nil)
	w.Write([]byte("real\n"))
	w.Close()
	raw, _ := os.ReadFile(path)
	if string(raw) != "real\n" {
		t.Fatalf("file = %q, want real (filter should have been cleared)", raw)
	}
}
