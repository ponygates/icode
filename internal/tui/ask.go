package tui

import (
	"fmt"
	"time"

	"github.com/ponygates/icode/internal/core/tool"
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

// ── Multi-question wizard (opencode AskQuestion parity) ─────────────────

// askFormState is an in-flight multi-question form. The main loop routes
// digits (pick), Enter (confirm multi-select / advance), Tab (next
// question), Esc (cancel) into it; render draws the wizard overlay.
type askFormState struct {
	Questions []askFormQ
	Idx       int    // current question index
	Picked    []bool // current question's multi-select picks
	Answers   []askFormAnswer
	Ch        chan []askFormAnswer
}

type askFormQ struct {
	Question string
	Options  []string
	Multi    bool
	Text     bool
}

type askFormAnswer struct {
	Index   int
	Indices []int
	Text    string
}

// askUserFormInteractive is the Callback.OnSetAskUserForm implementation:
// blocks the tool's goroutine until the whole form is answered or cancelled.
func (t *TUI) askUserFormInteractive(questions []tool.FormQuestion) ([]tool.FormAnswer, error) {
	fs := &askFormState{
		Questions: make([]askFormQ, 0, len(questions)),
		Answers:   make([]askFormAnswer, len(questions)),
		Ch:        make(chan []askFormAnswer, 1),
	}
	for _, q := range questions {
		fs.Questions = append(fs.Questions, askFormQ{
			Question: q.Question,
			Options:  q.Options,
			Multi:    q.MultiSelect,
			Text:     q.Text || len(q.Options) == 0,
		})
	}
	fs.Picked = make([]bool, len(fs.Questions[0].Options))
	t.mu.Lock()
	t.askForm = fs
	t.mu.Unlock()
	t.scheduleRender()

	select {
	case answers := <-fs.Ch:
		out := make([]tool.FormAnswer, 0, len(answers))
		for _, a := range answers {
			out = append(out, tool.FormAnswer{Index: a.Index, Indices: a.Indices, Text: a.Text})
		}
		return out, nil
	case <-time.After(10 * time.Minute):
		t.mu.Lock()
		t.askForm = nil
		t.mu.Unlock()
		t.scheduleRender()
		return nil, fmt.Errorf("提问超时")
	}
}

// formAdvance moves to the next question (or submits when done).
func (t *TUI) formAdvance() {
	t.mu.Lock()
	fs := t.askForm
	t.mu.Unlock()
	if fs == nil {
		return
	}
	fs.Idx++
	if fs.Idx >= len(fs.Questions) {
		t.resolveAskForm(fs.Answers)
		return
	}
	fs.Picked = make([]bool, len(fs.Questions[fs.Idx].Options))
	t.scheduleRender()
}

// formPick selects option n of the current question (single-select advances
// immediately; multi-select toggles until Enter).
func (t *TUI) formPick(n int) {
	t.mu.Lock()
	fs := t.askForm
	t.mu.Unlock()
	if fs == nil || fs.Idx >= len(fs.Questions) {
		return
	}
	q := fs.Questions[fs.Idx]
	if n < 0 || n >= len(q.Options) || q.Text {
		return
	}
	if q.Multi {
		fs.Picked[n] = !fs.Picked[n]
		t.scheduleRender()
		return
	}
	fs.Answers[fs.Idx] = askFormAnswer{Index: n}
	t.formAdvance()
}

// formConfirm commits the current multi-select (or advances a text question).
func (t *TUI) formConfirm() {
	t.mu.Lock()
	fs := t.askForm
	t.mu.Unlock()
	if fs == nil || fs.Idx >= len(fs.Questions) {
		return
	}
	q := fs.Questions[fs.Idx]
	if q.Multi {
		var idx []int
		for i, on := range fs.Picked {
			if on {
				idx = append(idx, i)
			}
		}
		fs.Answers[fs.Idx] = askFormAnswer{Indices: idx}
		t.formAdvance()
	} else if q.Text {
		fs.Answers[fs.Idx] = askFormAnswer{Text: "（用户跳过）"}
		t.formAdvance()
	}
}

// resolveAskForm finalizes the form with the collected answers.
func (t *TUI) resolveAskForm(answers []askFormAnswer) {
	t.mu.Lock()
	fs := t.askForm
	t.askForm = nil
	t.mu.Unlock()
	if fs != nil {
		fs.Ch <- answers
	}
	t.scheduleRender()
}

// askFormActive reports whether the wizard form is waiting for input.
func (t *TUI) askFormActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.askForm != nil
}
