package tui

import (
	"bytes"
	"testing"
)

// TestRenderWelcomeOpenPath drives the exact state the CLI is in the instant
// it opens: raw mode, NO messages yet, and the startup welcome banner visible.
// A panic here is what the user experiences as a "flash close" (闪退) on launch.
func TestRenderWelcomeOpenPath(t *testing.T) {
	for _, w := range []int{20, 40, 80, 120, 200} {
		for _, h := range []int{10, 24, 36} {
			tui := &TUI{
				mode:       ModeAgent,
				model:      "openrouter/free",
				provider:   "openrouter",
				lang:       "zh-CN",
				theme:      "dark",
				rawMode:    true,
				color:      true,
				width:      w,
				height:     h,
				streamDone: make(chan struct{}, 1),
				// THE open-path state:
				welcomeVisible: true,
				messages:       []Message{},
			}
			tui.writer = &bytes.Buffer{}
			tui.render()
		}
	}
}

// TestRenderWelcomeWithOverlays covers the other initial overlays that can be
// open on launch-adjacent states (help / autocomplete) so no render path
// panics.
func TestRenderWelcomeWithOverlays(t *testing.T) {
	tui := &TUI{
		mode:           ModeAgent,
		model:          "openrouter/free",
		provider:       "openrouter",
		lang:           "zh-CN",
		theme:          "dark",
		rawMode:        true,
		color:          true,
		width:          120,
		height:         36,
		streamDone:     make(chan struct{}, 1),
		welcomeVisible: true,
		messages:       []Message{},
	}
	tui.writer = &bytes.Buffer{}

	tui.helpVisible = true
	tui.render()
	tui.helpVisible = false

	tui.acOpen = true
	tui.acItems = []acItem{{Name: "commit", Desc: "create commit"}}
	tui.render()
	tui.acOpen = false

	// welcome dismissed but still no messages (empty input prompt)
	tui.welcomeVisible = false
	tui.inputBuf = ""
	tui.render()
}
