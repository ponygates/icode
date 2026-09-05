package xgo

import (
	"sync"
	"testing"
	"time"
)

// TestGoSafe_RecoversPanic verifies a panicking goroutine cannot take the
// process down — the recover in GoSafe must swallow it (the "flash close" /
// 闪退 defence).
func TestGoSafe_RecoversPanic(t *testing.T) {
	done := make(chan struct{})
	GoSafe("test-panic", func() {
		defer close(done)
		panic("boom")
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("panicking goroutine did not run")
	}
	// Reaching here means the panic was recovered, not fatal.
}

// TestGoSafe_RunsFunction verifies the happy path executes.
func TestGoSafe_RunsFunction(t *testing.T) {
	var mu sync.Mutex
	var ran bool
	GoSafe("test-ok", func() {
		mu.Lock()
		ran = true
		mu.Unlock()
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if ran {
			mu.Unlock()
			return
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("function never ran")
}
