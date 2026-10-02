package cmd

// stdout_tee.go — the ultimate "who wrote this" discriminator.
//
// Forensic background: the user's typed characters sometimes appear as stray
// rows in the conversation area (钉字). The renderer's external-write
// detector proved those rows are not painted by render()'s diff loop, and a
// full audit of every Fprint(t.writer)/Fprint(os.Stdout) site found no path
// that could emit the user's keystrokes. Remaining suspects: (a) some code
// path in this process that writes to the terminal outside the audited
// sites, or (b) the console host itself injecting bytes (conhost echo
// revival / IME composition) — which never pass through our file
// descriptors at all.
//
// With ICODE_TEE=1, fd 1/2 are rerouted through pipes: everything the
// process writes to stdout/stderr is mirrored (timestamped per write) into
// ~/.icode/stdout-trace.log and forwarded to the real console. One repro
// then settles it:
//
//   - stray bytes present in the trace  → our process wrote them; the
//     surrounding writes identify the guilty code path.
//   - stray bytes absent from the trace → nothing in this process wrote
//     them; the console host injected them and no code audit can ever find
//     a writer (the periodic full repaint is then the only defense).
//
// The pipe must be transparent to the rest of the process: os.Stdout keeps
// its *os.File type (a new File over the pipe write end), so existing
// assignments like `t.writer = os.Stdout` keep working. Console-specific
// users of os.Stdout.Fd() degrade gracefully: vtProcessingActive() treats a
// non-console handle as VT-capable, and termSize() falls back to stdin.
// The log grows fast while streaming, so the env switch stays opt-in.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/core/privacy"
)

var (
	teeWG     sync.WaitGroup
	teePipes  []*os.File // write ends to close on drain
	teeLog    *os.File
	teeMu     sync.Mutex
	teeMirror io.Writer // redacting mirror writer, flushed on drain
)

// teeStdio reroutes stdout/stderr through the mirroring pipes. Called right
// after setupConsoleIO()/allocConsole() have finalized the real console
// handles, and before any command runs — every later os.Stdout reference
// (including tui's t.writer = os.Stdout) picks up the pipe.
func teeStdio() {
	if os.Getenv("ICODE_TEE") != "1" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	dir := filepath.Join(home, ".icode")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "stdout-trace.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	teeLog = f
	fmt.Fprintf(f, "=== icode stdout-tee %s pid=%d ===\n",
		time.Now().Format("2006-01-02 15:04:05"), os.Getpid())

	teeWG.Add(2)
	teeMirror = privacy.NewRedactingWriter(f)
	teePipe(os.Stdout, teeMirror, "stdout")
	teePipe(os.Stderr, teeMirror, "stderr")
}

// teePipe swaps one standard stream for a pipe, then pumps the pipe into the
// real stream and the trace log.
func teePipe(real *os.File, log io.Writer, name string) {
	r, w, err := os.Pipe()
	if err != nil {
		teeWG.Done()
		return
	}
	teePipes = append(teePipes, w)
	old := *real
	*real = *os.NewFile(w.Fd(), "|"+name+"-tee")
	go func() {
		defer teeWG.Done()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				// Forward to the real terminal first so the UI is never
				// delayed by disk I/O, then mirror to the log.
				if _, werr := old.Write(buf[:n]); werr != nil {
					return
				}
				teeMu.Lock()
				fmt.Fprintf(log, "\n[%s %s %dB] ", name,
					time.Now().Format("15:04:05.000"), n)
				log.Write(buf[:n])
				teeMu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
}

// teeDrain closes the pipe write ends so the pump goroutines hit EOF, waits
// for them to finish, and flushes the log — call it on the way out of
// Execute so the tail of the trace survives process exit. No-op when the
// tee is not active.
func teeDrain() {
	if teeLog == nil {
		return
	}
	for _, w := range teePipes {
		w.Close()
	}
	teeWG.Wait()
	// Release the buffered partial line before the file closes, otherwise the
	// trace tail (possibly holding a redacted fragment) is lost.
	if fl, ok := teeMirror.(interface{ Flush() error }); ok {
		_ = fl.Flush()
	}
	teeLog.Sync()
	teeLog.Close()
	teeLog = nil
	teeMirror = nil
}
