package tui

import (
	"fmt"
	"time"
)

// askState is an in-flight interactive multiple-choice question (Claude Code
// AskUserQuestion parity). Set while the engine's ask_user_question tool is
// waiting; the main loop routes digit/Enter/Esc keys into Ch and the render
// pass draws an option overlay.
type askState struct {
	Question string
	Options  []string
	Ch       chan int
}

// askUserInteractive is the Callback.OnSetAskUser implementation. It blocks
// the tool's goroutine until the user picks an option (digits 1-9, Enter =
// first option, Esc = cancel) via the main loop, with a safety timeout so a
// stuck question can never hang a turn forever.
func (t *TUI) askUserInteractive(question string, options []string) (int, error) {
	ch := make(chan int, 1)
	t.mu.Lock()
	t.askPending = &askState{Question: question, Options: options, Ch: ch}
	t.mu.Unlock()
	t.scheduleRender()

	select {
	case idx := <-ch:
		return idx, nil
	case <-time.After(5 * time.Minute):
		t.mu.Lock()
		t.askPending = nil
		t.mu.Unlock()
		t.scheduleRender()
		return 0, fmt.Errorf("提问超时")
	}
}

// resolveAsk finalizes the in-flight question with the chosen index
// (negative = cancelled) and clears the pending state.
func (t *TUI) resolveAsk(idx int) {
	t.mu.Lock()
	ask := t.askPending
	t.askPending = nil
	t.mu.Unlock()
	if ask != nil {
		ask.Ch <- idx
	}
	t.scheduleRender()
}

// askPendingVisible reports whether an interactive question is waiting for
// input (guards the main-loop key routing).
func (t *TUI) askPendingVisible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.askPending != nil
}
