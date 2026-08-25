package tool

import (
	"context"
	"testing"
	"time"
)

// forkRunner implements both SubAgentRunner and ForkingSubAgentRunner.
type forkRunner struct {
	fakeRunner
	forked bool
}

func (f *forkRunner) RunForkedSubAgent(ctx context.Context, name, prompt string) (string, int, error) {
	f.mu.Lock()
	f.forked = true
	f.mu.Unlock()
	return f.RunSubAgent(ctx, name, prompt)
}

func TestTaskToolForkDispatch(t *testing.T) {
	fr := &forkRunner{fakeRunner: fakeRunner{delay: time.Millisecond}}
	tt := NewTaskTool(fr)

	// fork=true routes to RunForkedSubAgent when the host supports it.
	res, err := tt.Execute(context.Background(), `{"name":"explore","prompt":"p","fork":true}`)
	if err != nil || !res.Success {
		t.Fatalf("Execute: %v / %+v", err, res)
	}
	fr.mu.Lock()
	forked := fr.forked
	fr.mu.Unlock()
	if !forked {
		t.Error("fork=true did not call RunForkedSubAgent")
	}

	// Without the flag the plain path is used.
	fr2 := &forkRunner{fakeRunner: fakeRunner{delay: time.Millisecond}}
	if _, err := NewTaskTool(fr2).Execute(context.Background(), `{"name":"explore","prompt":"p"}`); err != nil {
		t.Fatal(err)
	}
	fr2.mu.Lock()
	forked2 := fr2.forked
	fr2.mu.Unlock()
	if forked2 {
		t.Error("plain task must not fork")
	}
}
