package conversation

import (
	"log"
	"sync"
)

// Engine-side diagnostics.
//
// This package used to have no logging at all, and the engine is where the
// state that must survive lives — preference memory, session summaries,
// checkpoints. Every one of those writes was discarded with `_ =`, so a
// read-only home directory or a full disk produced a session that silently
// forgot its own history, with nothing in cli.log to explain why.
//
// Errors stay non-fatal: the caller's control flow is unchanged, the failure
// is only made visible. Repeats are suppressed per operation key because the
// preference save runs on a debounce timer — a permanently broken path must
// say so once, not every tick.

var (
	diagMu   sync.Mutex
	diagSeen = map[string]bool{}
)

// diagf logs one diagnostic line at most once per key.
func diagf(key, format string, args ...any) {
	diagMu.Lock()
	if diagSeen[key] {
		diagMu.Unlock()
		return
	}
	diagSeen[key] = true
	diagMu.Unlock()
	log.Printf("[icode engine] "+format, args...)
}

// discard records an error the caller deliberately does not act on.
func discard(op string, err error) {
	if err == nil {
		return
	}
	diagf(op, "%s failed: %v", op, err)
}

// discardOnce logs every failure, for operations rare enough that a repeat is
// itself information.
func discardOnce(op string, err error) {
	if err == nil {
		return
	}
	log.Printf("[icode engine] %s failed: %v", op, err)
}
