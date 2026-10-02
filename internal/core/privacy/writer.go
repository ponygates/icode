package privacy

import (
	"bytes"
	"io"
	"sync"
)

// maxRedactPending bounds the partial-line buffer. A stream that never sends
// a newline (or a panic-truncated tail) must not stall output forever or grow
// memory without limit, so an oversized buffer is redacted and flushed.
const maxRedactPending = 1 << 20

// redactingWriter masks credentials in everything written through it.
// Complete lines are flushed on every write; a trailing partial line is held
// briefly so a token split across two Write calls is still caught whole.
type redactingWriter struct {
	mu      sync.Mutex
	w       io.Writer
	pending []byte
}

// NewRedactingWriter wraps w so each line has RedactSecrets applied before it
// reaches the underlying writer. Returns w unchanged when it is nil, so
// callers need no guard. The result is safe for concurrent Use.
func NewRedactingWriter(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	return &redactingWriter{w: w}
}

func (r *redactingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy first: p is caller-owned (log.Logger reuses its buffer) and pending
	// must not alias it.
	r.pending = append(r.pending, p...)
	if i := bytes.LastIndexByte(r.pending, '\n'); i >= 0 {
		if _, err := r.redactWrite(r.pending[:i+1]); err != nil {
			r.pending = r.pending[:0]
			return 0, err
		}
		r.pending = append(r.pending[:0], r.pending[i+1:]...)
	}
	if len(r.pending) > maxRedactPending {
		if _, err := r.redactWrite(r.pending); err != nil {
			r.pending = r.pending[:0]
			return 0, err
		}
		r.pending = r.pending[:0]
	}
	// Report the caller's length: bytes are masked, not dropped.
	return len(p), nil
}

// Flush writes out any buffered partial line (redacted). Loggers that always
// terminate lines never accumulate pending, so this is only needed on exit
// paths and before closing the underlying writer.
func (r *redactingWriter) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) == 0 {
		return nil
	}
	_, err := r.redactWrite(r.pending)
	r.pending = r.pending[:0]
	return err
}

func (r *redactingWriter) redactWrite(buf []byte) (int, error) {
	return r.w.Write(RedactSecretsBytes(buf))
}
