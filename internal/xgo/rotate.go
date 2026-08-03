package xgo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// RotatingWriter is a minimal size-based log rotator. It writes to a file and,
// when the file exceeds MaxBytes, renames it to <path>.1 and starts a fresh
// file. This keeps logs bounded without pulling in an external dependency
// (lumberjack) — important here because the sandbox blocks `go get` for new
// modules.
//
// It implements io.Writer. Concurrency-safe (one mutex). Backups kept: 1
// (current + previous). MaxBytes <= 0 disables rotation (writes grow
// unbounded, matching the pre-existing behaviour).
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	f        *os.File
	written  int64 // bytes written to current file since open
}

// NewRotatingWriter opens (or creates) the log file at path. The file is
// opened append-only. If MaxBytes <= 0 rotation is disabled.
func NewRotatingWriter(path string, maxBytes int64) (*RotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("rotating writer: mkdir: %w", err)
	}
	w := &RotatingWriter{path: path, maxBytes: maxBytes}
	if err := w.openExisting(); err != nil {
		return nil, err
	}
	return w, nil
}

// openExisting opens the current file (append). If the file already exceeds
// the cap, rotate immediately so we don't keep growing an already-oversized
// log.
func (w *RotatingWriter) openExisting() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("rotating writer: open: %w", err)
	}
	w.f = f
	if w.maxBytes > 0 {
		if info, err := f.Stat(); err == nil {
			w.written = info.Size()
			if w.written >= w.maxBytes {
				w.rotateLocked()
			}
		}
	}
	return nil
}

// Write implements io.Writer. After each write it checks the size cap and
// rotates when exceeded.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.openExisting(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	if err != nil {
		return n, err
	}
	w.written += int64(n)
	if w.maxBytes > 0 && w.written >= w.maxBytes {
		w.rotateLocked()
	}
	return n, nil
}

// rotateLocked renames the current file to <path>.1 (overwriting any previous
// backup) and opens a fresh file. Caller must hold w.mu.
func (w *RotatingWriter) rotateLocked() {
	if w.f != nil {
		w.f.Close()
	}
	backup := w.path + ".1"
	// Best-effort rename; if it fails we just truncate by opening fresh.
	_ = os.Rename(w.path, backup)
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0644)
	if err != nil {
		// Keep the writer usable: nil file means next Write retries open.
		w.f = nil
		w.written = 0
		return
	}
	w.f = f
	w.written = 0
}

// Close closes the underlying file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	return w.f.Close()
}

// Compile-time check.
var _ io.Writer = (*RotatingWriter)(nil)
