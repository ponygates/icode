package tui

import (
	"fmt"
	"os"
	"strings"
)

// welcomeLines renders the startup screen: an ASCII LOGO (the enlarged "iCode"
// wordmark — yellow-dot i + dim Code, in opencode's minimal style and
// deliberately WITHOUT a surrounding box so it can never be mis-aligned) on
// top, followed by the two startup panels — the LEFT
// panel merges the live session info (model / provider / mode / cwd / context /
// cache / quick commands) with the "Welcome back!" greeting, and the RIGHT panel
// shows tips & what's new. The two panels sit side by side when they fit, and
// stack vertically on narrow terminals.
func (t *TUI) welcomeLines(width, maxH int) []string {
	if maxH < 1 || width < 30 {
		return nil
	}
	logo := t.logoLines(width)
	boxes := t.welcomeBoxes(width)
	if boxes == nil {
		if logo != nil {
			return logo
		}
		return []string{"  " + t.paint("orange", "*") + "  " + t.paint("bold", "欢迎使用 iCode")}
	}
	// Centre the panel block (side-by-side or stacked) within the terminal so
	// it shares the LOGO's centre axis — the whole welcome stays cohesive.
	boxesW := 0
	for _, l := range boxes {
		if vw := visibleWidth(l); vw > boxesW {
			boxesW = vw
		}
	}
	if indent := (width - boxesW) / 2; indent > 0 {
		pad := strings.Repeat(" ", indent)
		for i := range boxes {
			boxes[i] = pad + boxes[i]
		}
	}
	combined := append(append([]string{}, logo...), append([]string{""}, boxes...)...)
	if len(combined) <= maxH {
		return combined
	}
	// Too tall for the full logo + panels: drop the logo, keep the panels.
	if len(boxes) <= maxH {
		return boxes
	}
	// Still too tall: show the left panel alone (it carries the greeting).
	left := t.welcomeInfoBox(width)
	if left != nil && len(left) <= maxH {
		return left
	}
	return []string{"  " + t.paint("orange", "*") + "  " + t.paint("bold", "欢迎使用 iCode")}
}

// welcomeBoxes returns the two startup panels as a single block: side by side
// when they fit horizontally, stacked vertically otherwise. Returns nil if
// neither panel can be built.
func (t *TUI) welcomeBoxes(width int) []string {
	leftLines := t.welcomeInfoLines()
	rightLines := t.welcomeTipsLines()

	// Equalise heights so the two panels can be placed side by side with their
	// top and bottom borders perfectly aligned. Keep content top-aligned so the
	// first real line of each panel (Welcome back! / Tips for getting started)
	// sits on the same row, making the two boxes feel horizontally aligned.
	maxLen := len(leftLines)
	if len(rightLines) > maxLen {
		maxLen = len(rightLines)
	}
	leftLines = padSliceBottom(leftLines, maxLen)
	rightLines = padSliceBottom(rightLines, maxLen)

	// Use the same inner width for both boxes so their outer borders line up
	// vertically and the whole block looks like one aligned composition.
	innerW := maxVisibleWidth(leftLines)
	if w := maxVisibleWidth(rightLines); w > innerW {
		innerW = w
	}
	if innerW < 16 {
		innerW = 16
	}
	if innerW+4 > width {
		innerW = width - 4
	}
	if innerW < 6 {
		return nil
	}

	left := t.buildBoxWithInner("cyan", leftLines, innerW, width)
	right := t.buildBoxWithInner("orange", rightLines, innerW, width)
	if left == nil && right == nil {
		return nil
	}
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	gap := 3
	if boxWidth(left[0])+gap+boxWidth(right[0]) <= width {
		return joinSideBySide(left, right, gap)
	}
	// Not enough horizontal room — stack them instead.
	return append(append([]string{}, left...), append([]string{""}, right...)...)
}

// welcomeInfoBox returns the LEFT panel (greeting + live session info) as a
// bordered box.
func (t *TUI) welcomeInfoBox(width int) []string {
	return t.buildBox("cyan", t.welcomeInfoLines(), width)
}

// welcomeTipsBox returns the RIGHT panel (tips & what's new) as a bordered box.
func (t *TUI) welcomeTipsBox(width int) []string {
	return t.buildBox("orange", t.welcomeTipsLines(), width)
}

// welcomeInfoLines builds the LEFT panel content: greeting + live session info.
// This is the content the user asked to pull out of the old (mis-aligned) top
// banner and merge into the new left panel.
func (t *TUI) welcomeInfoLines() []string {
	cwd, _ := os.Getwd()
	short := shortDir(cwd)
	// Label→value column: pad every label to a fixed visible width so the
	// values start at the same column no matter how long the label is.
	// Hand-padded literals were the source of the drifting value column.
	kv := func(label, val string) string {
		return label + strings.Repeat(" ", 10-visibleWidth(label)) + val
	}
	ctxMeter := "-"
	if t.contextWindow > 0 && t.contextTokens >= 0 {
		pct := t.contextTokens * 100 / t.contextWindow
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		cells := 10
		filled := pct * cells / 100
		bar := repeat("█", filled) + repeat("░", cells-filled)
		ctxMeter = fmt.Sprintf("%dK / %dK [%s] %d%%",
			t.contextTokens/1000, t.contextWindow/1000, bar, pct)
	}
	cacheVal := "-"
	if t.cacheHitRate > 0 {
		cacheVal = fmt.Sprintf("%.0f%%", t.cacheHitRate*100)
	}
	colon := func(key string) string { return t.tstr(key) + ":" }
	return []string{
		t.paint("bold", t.tstr("welcome.greeting")),
		"",
		kv(colon("welcome.model"), t.model),
		kv(colon("welcome.provider"), t.provider),
		kv(colon("welcome.mode"), t.modeLabel(t.mode)),
		kv(colon("welcome.cwd"), short),
		"",
		kv(colon("welcome.ctx"), ctxMeter),
		kv(colon("welcome.cache"), cacheVal),
		"",
		"/help  /model  /provider  /mode  /clear  /exit",
	}
}

// welcomeTipsLines builds the RIGHT panel content: tips & what's new.
func (t *TUI) welcomeTipsLines() []string {
	// Underline = title visible width, so the orange rule always matches the
	// heading above it (a fixed dash count drifted when titles changed).
	rule := func(title string) string {
		return repeat("─", visibleWidth(title))
	}
	tipsTitle := t.tstr("welcome.tipsTitle")
	newTitle := t.tstr("welcome.newTitle")
	return []string{
		t.paint("orange", tipsTitle),
		t.paint("orange", rule(tipsTitle)),
		"  " + t.tstr("welcome.tipsBody1"),
		"  " + t.tstr("welcome.tipsBody2"),
		"",
		t.paint("orange", newTitle),
		t.paint("orange", rule(newTitle)),
		"  " + t.tstr("welcome.newBody"),
	}
}
