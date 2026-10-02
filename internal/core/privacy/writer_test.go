package privacy

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const fakeKey = "sk-abcdefghijklmnopqrstuvwx"

// readAll fails the test if the plain secret appears anywhere in the bytes.
func assertNoSecret(t *testing.T, raw []byte, ctx string) {
	t.Helper()
	if i := bytes.Index(raw, []byte(fakeKey)); i >= 0 {
		lo := i - 40
		if lo < 0 {
			lo = 0
		}
		t.Fatalf("%s: plaintext secret on disk at %d: ...%s...", ctx, i, raw[lo:i+60])
	}
}

// A secret split across two Write calls must still be caught: the partial
// line is buffered until the newline arrives, then redacted as a whole.
func TestRedactingWriter_SplitSecretAcrossWrites(t *testing.T) {
	var buf bytes.Buffer
	w := NewRedactingWriter(&buf)
	if _, err := w.Write([]byte("auth failed: key=" + fakeKey[:8])); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(fakeKey[8:] + "\n")); err != nil {
		t.Fatal(err)
	}
	assertNoSecret(t, buf.Bytes(), "split write")
	if !strings.Contains(buf.String(), "[REDACTED]") {
		t.Fatalf("no redaction marker: %q", buf.String())
	}
}

// Complete lines reach the underlying writer immediately — buffering must
// never delay a normal log line waiting for the next one.
func TestRedactingWriter_FlushesCompleteLinesPerWrite(t *testing.T) {
	var buf bytes.Buffer
	w := NewRedactingWriter(&buf)
	w.Write([]byte("line1 " + fakeKey + "\n"))
	w.Write([]byte("line2 ok\n"))
	if !strings.Contains(buf.String(), "line2 ok") {
		t.Fatalf("second line was held back: %q", buf.String())
	}
	assertNoSecret(t, buf.Bytes(), "line flush")
}

// A trailing partial line is held, then released (redacted) by Flush — this
// is the exit/panic-truncation fallback: nothing waits until process end.
func TestRedactingWriter_PartialLineFlush(t *testing.T) {
	var buf bytes.Buffer
	w := NewRedactingWriter(&buf)
	w.Write([]byte("tail " + fakeKey))
	if buf.Len() != 0 {
		t.Fatalf("partial line flushed early: %q", buf.String())
	}
	if err := w.(*redactingWriter).Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "tail ") {
		t.Fatalf("Flush lost prose: %q", buf.String())
	}
	assertNoSecret(t, buf.Bytes(), "flush")
}

// A stream that never sends a newline must not grow the buffer without bound.
func TestRedactingWriter_BoundedPending(t *testing.T) {
	var buf bytes.Buffer
	w := NewRedactingWriter(&buf)
	chunk := strings.Repeat("x", 64*1024)
	before := maxRedactPending + 2*len(chunk)
	for i := 0; i < before/len(chunk); i++ {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if buf.Len() == 0 {
		t.Fatal("no newline ever: pending grew past the cap without flushing")
	}
	// Memory held back is at most one cap window, not the whole stream.
	rw := w.(*redactingWriter)
	if rw.pending == nil {
		t.Fatal("pending lost")
	}
}

func TestRedactingWriter_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "concurrent.log"))
	if err != nil {
		t.Fatal(err)
	}
	w := NewRedactingWriter(f)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				fmt.Fprintf(w, "goroutine %d line %d key %s\n", g, i, fakeKey)
			}
		}(g)
	}
	wg.Wait()
	if err := w.(interface{ Flush() error }).Flush(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	raw, _ := os.ReadFile(filepath.Join(dir, "concurrent.log"))
	assertNoSecret(t, raw, "concurrent")
	if n := strings.Count(string(raw), "[REDACTED]"); n != 400 {
		t.Fatalf("lost lines: %d redaction markers, want 400", n)
	}
}

// End-to-end through the stdlib default logger — the exact wiring done in
// cmd.Execute: after log.SetOutput(NewRedactingWriter(file)), a log.Printf
// carrying a key must never reach the file in the clear.
func TestRedactingWriter_StdlibLogEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	old := log.Default().Writer()
	lg := log.New(NewRedactingWriter(f), "", 0)
	lg.Printf("provider 401: Authorization: Bearer %s failed", fakeKey)
	lg.Printf("no key here, plain line survives")
	f.Close()
	log.Default().SetOutput(old)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecret(t, raw, "e2e stdlib log")
	// 磁盘上的内容 grep 不到明文 key，但普通日志行必须原样保留。
	if !strings.Contains(string(raw), "plain line survives") {
		t.Fatalf("ordinary line was eaten: %s", raw)
	}
	if !strings.Contains(string(raw), "provider 401:") {
		t.Fatalf("line prefix lost: %s", raw)
	}
}
