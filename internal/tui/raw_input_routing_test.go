package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// keyCallback records what the raw-input layer hands to the host, so the routing
// tests can assert "this key never reached the send/submit path".
type keyCallback struct {
	testCallback
	sends       []string
	fwd         []string
	interruptes int
}

func (c *keyCallback) OnSend(text string, _ []types.Attachment) { c.sends = append(c.sends, text) }
func (c *keyCallback) OnSlashCommand(cmd string, args []string) {
	c.fwd = append(c.fwd, strings.TrimSpace(cmd+" "+strings.Join(args, " ")))
}
func (c *keyCallback) OnInterrupt() { c.interruptes++ }

// keyTUI builds a raw-mode TUI wired to a recording callback, with the ANSI/timer
// side effects made harmless: the screen writer is discarded and HOME points at a
// temp dir so nothing can persist into the developer's real ~/.icode.
func keyTUI(t *testing.T) (*TUI, *keyCallback) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	cb := &keyCallback{}
	tu := New(Config{Mode: ModeAuto, Model: "m1", Provider: "openrouter", Version: "test", Callback: cb})
	tu.rawMode = true
	tu.welcomeVisible = false
	tu.writer = io.Discard
	return tu, cb
}

func draft(tu *TUI) string {
	tu.mu.Lock()
	defer tu.mu.Unlock()
	return tu.inputBuf
}

func setDraft(tu *TUI, s string) { tu.setInput(s, len([]rune(s))) }

func feedKeys(tu *TUI, s string) {
	for _, r := range s {
		tu.keyCh <- r
	}
}

// TestHandleKeyOverlayOwnership guards routeKeyToOverlay: while an overlay is open
// the key belongs to it and must never touch the input line or the submit path.
func TestHandleKeyOverlayOwnership(t *testing.T) {
	// resolveAsk clears t.askPending before delivering the answer, so the
	// assertions below must hold the channel, not the (now nil) state pointer.
	askCh := make(chan int, 1)
	strayCh := make(chan int, 1)
	cases := []struct {
		name  string
		setup func(*TUI)
		key   rune
		want  func(*testing.T, *TUI)
	}{
		{
			name: "form wizard takes Tab",
			setup: func(tu *TUI) {
				tu.askForm = &askFormState{
					Questions: []askFormQ{{Question: "one", Options: []string{"a", "b"}}, {Question: "two", Options: []string{"c"}}},
					Picked:    []bool{false, false},
					Ch:        make(chan []askFormAnswer, 1),
				}
			},
			key: 0x09,
			want: func(t *testing.T, tu *TUI) {
				tu.mu.Lock()
				defer tu.mu.Unlock()
				if tu.askForm.Idx != 1 {
					t.Errorf("askForm.Idx = %d, want 1 (Tab advances)", tu.askForm.Idx)
				}
			},
		},
		{
			name: "form wizard Esc cancels",
			setup: func(tu *TUI) {
				tu.askForm = &askFormState{
					Questions: []askFormQ{{Question: "one", Options: []string{"a"}}},
					Picked:    []bool{false},
					Ch:        make(chan []askFormAnswer, 1),
				}
			},
			key: 0x1b,
			want: func(t *testing.T, tu *TUI) {
				if tu.askFormActive() {
					t.Error("Esc must cancel the form wizard")
				}
			},
		},
		{
			name: "ask pending digit picks",
			setup: func(tu *TUI) {
				tu.askPending = &askState{Question: "q", Options: []string{"a", "b"}, Ch: askCh}
			},
			key: '2',
			want: func(t *testing.T, tu *TUI) {
				select {
				case got := <-askCh:
					if got != 1 {
						t.Errorf("ask answer = %d, want 1", got)
					}
				default:
					t.Error("digit 2 did not resolve the pending ask")
				}
			},
		},
		{
			name: "ask pending swallows unknown keys",
			setup: func(tu *TUI) {
				tu.askPending = &askState{Question: "q", Options: []string{"a"}, Ch: strayCh}
			},
			key: 'z',
			want: func(t *testing.T, tu *TUI) {
				select {
				case <-strayCh:
					t.Error("stray key must not resolve the pending ask")
				default:
				}
			},
		},
		{
			name: "login prompt keeps the key out of the input line",
			setup: func(tu *TUI) {
				tu.prompt = &promptState{label: "key: ", secret: true, buf: "", onDone: func(string) {}}
			},
			key: 'x',
			want: func(t *testing.T, tu *TUI) {
				tu.mu.Lock()
				defer tu.mu.Unlock()
				if tu.prompt == nil {
					t.Fatal("prompt vanished")
				}
				if tu.prompt.buf != "x" {
					t.Errorf("prompt.buf = %q, want %q", tu.prompt.buf, "x")
				}
			},
		},
		{
			name: "settings panel owns Enter",
			setup: func(tu *TUI) {
				tu.settingsOpen = true
			},
			key: '\r',
			want: func(t *testing.T, tu *TUI) {
				tu.mu.Lock()
				defer tu.mu.Unlock()
				if tu.settingsOpen {
					t.Error("Enter should have acted on the settings row")
				}
			},
		},
		{
			name: "search overlay owns printable keys",
			setup: func(tu *TUI) {
				tu.pushHistory("earlier note")
				tu.startSearch()
				// startSearch parks the draft by design; re-arm it so the
				// runner's assertion proves the overlay never writes the line.
				setDraft(tu, "draft")
			},
			key: 'e',
			want: func(t *testing.T, tu *TUI) {
				tu.mu.Lock()
				defer tu.mu.Unlock()
				if tu.searchBuf != "e" {
					t.Errorf("searchBuf = %q, want %q", tu.searchBuf, "e")
				}
				if !tu.searchMode {
					t.Error("search overlay must stay open")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tu, cb := keyTUI(t)
			setDraft(tu, "draft")
			tc.setup(tu)
			if !tu.handleKey(tc.key) {
				t.Fatal("handleKey signalled exit")
			}
			if got := draft(tu); got != "draft" {
				t.Errorf("input buffer changed while an overlay owned the key: %q", got)
			}
			if len(cb.sends) != 0 {
				t.Errorf("overlay key leaked into the send path: %v", cb.sends)
			}
			tc.want(t, tu)
		})
	}
}

// TestHandleKeyOverlayPrecedence pins the ownership ORDER of routeKeyToOverlay:
// form wizard > pending ask > /login prompt > settings > Ctrl+R search.
func TestHandleKeyOverlayPrecedence(t *testing.T) {
	t.Run("prompt beats settings", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.settingsOpen = true
		tu.prompt = &promptState{label: "key: ", onDone: func(string) {}}
		if !tu.handleKey(0x03) { // Ctrl+C: the prompt cancels itself
			t.Fatal("handleKey signalled exit")
		}
		if tu.promptActive() {
			t.Error("prompt did not handle Ctrl+C")
		}
		tu.mu.Lock()
		defer tu.mu.Unlock()
		if !tu.settingsOpen {
			t.Error("settings panel must survive — the prompt owns the key")
		}
	})
	t.Run("settings beats search", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.pushHistory("note")
		tu.startSearch()
		tu.searchMode = false
		tu.settingsOpen = true
		if !tu.handleKey(0x12) { // Ctrl+R is bound inside the settings panel? no — it must NOT start a search
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		defer tu.mu.Unlock()
		if tu.searchMode {
			t.Error("settings panel owns the key; Ctrl+R must not open search")
		}
	})
	t.Run("pending ask beats form-less input", func(t *testing.T) {
		tu, _ := keyTUI(t)
		ch := make(chan int, 1)
		tu.askPending = &askState{Question: "q", Options: []string{"a", "b", "c"}, Ch: ch}
		if !tu.handleKey('\r') {
			t.Fatal("handleKey signalled exit")
		}
		if got := <-ch; got != 0 {
			t.Errorf("Enter resolved to %d, want 0 (first option)", got)
		}
	})
}

// TestHandleKeyControlEditing covers handleControlEditKeys: every bound editing
// rune must act on the input line and keep the loop alive.
func TestHandleKeyControlEditing(t *testing.T) {
	cases := []struct {
		name   string
		start  string
		cursor int
		key    rune
		want   string // expected buffer after the key
	}{
		{"Ctrl+J inserts a newline", "ab", 2, 0x0a, "ab\n"},
		{"Ctrl+A leaves the buffer alone", "hello\nworld", 8, 0x01, "hello\nworld"},
		{"Ctrl+E leaves the buffer alone", "hello\nworld", 6, 0x05, "hello\nworld"},
		{"Ctrl+K clears the buffer", "typed text", 3, 0x0b, ""},
		{"Ctrl+W deletes a word", "alpha beta ", 11, 0x17, "alpha "},
		{"Ctrl+U deletes everything before the caret", "alpha\nbeta", 8, 0x15, "ta"},
		{"Backspace deletes before cursor", "abcd", 4, 0x7f, "abc"},
		{"Ctrl+Backspace alias deletes too", "abcd", 4, 0x08, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tu, _ := keyTUI(t)
			tu.setInput(tc.start, tc.cursor)
			if !tu.handleKey(tc.key) {
				t.Fatal("handleKey signalled exit")
			}
			if got := draft(tu); got != tc.want {
				t.Errorf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("Ctrl+_ undoes the last input edit", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.setInput("abc", 3)
		if !tu.handleKey(0x0a) { // Ctrl+J records the pre-edit snapshot
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "abc\n" {
			t.Fatalf("buffer = %q, want %q", got, "abc\n")
		}
		if !tu.handleKey(0x1f) {
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "abc" {
			t.Errorf("buffer = %q, want the pre-edit snapshot", got)
		}
	})
	t.Run("Ctrl+_ without an edit just notices", func(t *testing.T) {
		tu, _ := keyTUI(t)
		setDraft(tu, "nothing to undo")
		if !tu.handleKey(0x1f) {
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "nothing to undo" {
			t.Errorf("buffer = %q, want it untouched", got)
		}
	})
}

// TestHandleKeyControlCursorMoves asserts Ctrl+A/Ctrl+E position the caret on the
// caret's own logical line (readline behaviour, not the whole buffer).
func TestHandleKeyControlCursorMoves(t *testing.T) {
	tu, _ := keyTUI(t)
	tu.setInput("one\ntwo", 5) // 'o' of "two"
	if !tu.handleKey(0x01) {
		t.Fatal("handleKey signalled exit")
	}
	tu.mu.Lock()
	got := tu.cursor
	tu.mu.Unlock()
	if got != 4 {
		t.Errorf("Ctrl+A cursor = %d, want 4 (start of logical line)", got)
	}
	if !tu.handleKey(0x05) {
		t.Fatal("handleKey signalled exit")
	}
	tu.mu.Lock()
	got = tu.cursor
	tu.mu.Unlock()
	if got != 7 {
		t.Errorf("Ctrl+E cursor = %d, want 7 (end of logical line)", got)
	}
}

// TestHandleKeyUnboundControlIsIgnored pins that an unbound control rune is
// swallowed by the printable tail instead of being inserted.
func TestHandleKeyUnboundControlIsIgnored(t *testing.T) {
	tu, _ := keyTUI(t)
	setDraft(tu, "keep")
	for _, r := range []rune{0x00, 0x06, 0x14, 0x18} {
		if !tu.handleKey(r) {
			t.Fatalf("handleKey(%#x) signalled exit", r)
		}
	}
	if got := draft(tu); got != "keep" {
		t.Errorf("buffer = %q, want unchanged", got)
	}
}

// TestHandleKeyAutocompleteVersusHistory guards handleControlNavKeys: the menu
// takes the arrow-equivalent keys only while it is open.
func TestHandleKeyAutocompleteVersusHistory(t *testing.T) {
	t.Run("Ctrl+P walks the menu when open", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.pushHistory("history entry")
		tu.acOpen = true
		tu.acItems = []acItem{{Name: "one"}, {Name: "two"}, {Name: "three"}}
		tu.acIdx = 2
		if !tu.handleKey(0x10) {
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		defer tu.mu.Unlock()
		if tu.acIdx != 1 {
			t.Errorf("acIdx = %d, want 1", tu.acIdx)
		}
		if tu.inputBuf == "history entry" {
			t.Error("history recall must not fire while the menu is open")
		}
	})
	t.Run("Ctrl+N walks down", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.acOpen = true
		tu.acItems = []acItem{{Name: "one"}, {Name: "two"}}
		tu.acIdx = 0
		if !tu.handleKey(0x0e) {
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		defer tu.mu.Unlock()
		if tu.acIdx != 1 {
			t.Errorf("acIdx = %d, want 1", tu.acIdx)
		}
	})
	t.Run("Tab accepts the highlighted row", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.setInput("/com", 4)
		tu.acOpen = true
		tu.acItems = []acItem{{Name: "/compact"}}
		tu.acIdx = 0
		if !tu.handleKey(0x09) {
			t.Fatal("handleKey signalled exit")
		}
		if !strings.HasPrefix(draft(tu), "/com") || draft(tu) == "/com" {
			t.Errorf("Tab did not accept the suggestion: %q", draft(tu))
		}
	})
	t.Run("Ctrl+P recalls history when the menu is closed", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.pushHistory("earlier prompt")
		if !tu.handleKey(0x10) {
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "earlier prompt" {
			t.Errorf("buffer = %q, want the recalled history entry", got)
		}
	})
	t.Run("Tab cycles the permission mode with no menu", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.mu.Lock()
		before := tu.mode
		tu.mu.Unlock()
		if !tu.handleKey(0x09) {
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		after := tu.mode
		tu.mu.Unlock()
		if after == before {
			t.Errorf("Tab did not cycle the mode (still %q)", after)
		}
	})
}

// TestHandleKeySessionControlKeys covers handleControlSessionKeys, including the
// two keys that can end the run loop.
func TestHandleKeySessionControlKeys(t *testing.T) {
	t.Run("Ctrl+C interrupts a running turn", func(t *testing.T) {
		tu, cb := keyTUI(t)
		tu.streaming = true
		if !tu.handleKey(0x03) {
			t.Fatal("Ctrl+C during streaming must keep the loop alive")
		}
		if cb.interruptes != 1 {
			t.Errorf("OnInterrupt calls = %d, want 1", cb.interruptes)
		}
	})
	t.Run("Ctrl+C clears a typed draft", func(t *testing.T) {
		tu, cb := keyTUI(t)
		setDraft(tu, "half written")
		if !tu.handleKey(0x03) {
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "" {
			t.Errorf("buffer = %q, want cleared", got)
		}
		if cb.interruptes != 0 {
			t.Error("must not interrupt with no running turn")
		}
	})
	t.Run("Ctrl+C on an empty line asks for confirmation then exits", func(t *testing.T) {
		tu, cb := keyTUI(t)
		feedKeys(tu, "x") // the "press any key" confirmation
		if tu.handleKey(0x03) {
			t.Fatal("the confirming Ctrl+C must end the loop")
		}
		if tu.running {
			t.Error("running flag must be cleared")
		}
		if len(cb.fwd) != 1 || cb.fwd[0] != "/summarize" {
			t.Errorf("exit-time summary archive forwards = %v, want [/summarize]", cb.fwd)
		}
	})
	t.Run("Ctrl+D exits only on an empty line", func(t *testing.T) {
		tu, _ := keyTUI(t)
		setDraft(tu, "text")
		if !tu.handleKey(0x04) {
			t.Error("Ctrl+D with a draft must not quit")
		}
		tu.setInput("", 0)
		if tu.handleKey(0x04) {
			t.Error("Ctrl+D on an empty line must quit")
		}
		if tu.running {
			t.Error("running flag must be cleared")
		}
	})
	t.Run("Ctrl+O dismisses the banner before toggling the transcript", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.welcomeVisible = true
		if !tu.handleKey(0x0f) {
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		if tu.welcomeVisible {
			t.Error("Ctrl+O must dismiss the welcome banner first")
		}
		verboseBefore := tu.transcriptVerbose
		tu.mu.Unlock()
		if !tu.handleKey(0x0f) {
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		if tu.transcriptVerbose == verboseBefore {
			t.Error("second Ctrl+O must toggle the transcript detail")
		}
		tu.mu.Unlock()
	})
	t.Run("Ctrl+B notes when nothing is running", func(t *testing.T) {
		tu, _ := keyTUI(t)
		if !tu.handleKey(0x02) {
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		got := tu.statusNotice
		tu.mu.Unlock()
		if !strings.Contains(got, "当前没有正在运行的任务") {
			t.Errorf("Ctrl+B idle notice missing: %q", got)
		}
	})
	t.Run("Ctrl+S stashes then restores", func(t *testing.T) {
		tu, _ := keyTUI(t)
		setDraft(tu, "stashed prompt")
		if !tu.handleKey(0x13) {
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "" {
			t.Errorf("buffer = %q, want the stash to clear it", got)
		}
		if !tu.handleKey(0x13) {
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "stashed prompt" {
			t.Errorf("buffer = %q, want the stash restored", got)
		}
	})
	t.Run("Ctrl+G without an editor keeps the draft", func(t *testing.T) {
		tu, _ := keyTUI(t)
		t.Setenv("EDITOR", "")
		t.Setenv("VISUAL", "")
		setDraft(tu, "keep me")
		if !tu.handleKey(0x07) {
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "keep me" {
			t.Errorf("buffer = %q, want it untouched", got)
		}
	})
	t.Run("Ctrl+comma opens settings", func(t *testing.T) {
		tu, _ := keyTUI(t)
		if !tu.handleKey(0x2c) {
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		defer tu.mu.Unlock()
		if !tu.settingsOpen {
			t.Error("Ctrl+, must open the settings panel")
		}
	})
	t.Run("Ctrl+L redraws without touching state", func(t *testing.T) {
		tu, _ := keyTUI(t)
		setDraft(tu, "draft")
		if !tu.handleKey(0x0c) {
			t.Fatal("handleKey signalled exit")
		}
		if got := draft(tu); got != "draft" {
			t.Errorf("buffer = %q, want untouched", got)
		}
	})
	t.Run("Ctrl+Z rejects staged edits", func(t *testing.T) {
		tu, _ := keyTUI(t)
		tu.diffBoxOpen = true
		if !tu.handleKey(0x1a) {
			t.Fatal("handleKey signalled exit")
		}
		tu.mu.Lock()
		defer tu.mu.Unlock()
		if tu.diffBoxOpen {
			t.Error("Ctrl+Z must close the review overlay")
		}
	})
}
