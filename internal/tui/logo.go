package tui

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// buildLogo renders the iCode startup banner: a single bordered box with the
// iCode wordmark + pony-mascot accent on top, followed by live session info
// (model / provider / mode / cwd / context-window usage / cache hit-rate) and
// the quick-command list.
//
// IMPORTANT: every glyph here is either ASCII, a CP437 box-drawing character
// (┌┐└┘│─), or a CP437 block element (█░). These are the ONLY character classes
// that render reliably on legacy Windows conhost (raster font / OEM codepage),
// where decorative Unicode symbols such as ✦ ◆ ✻ ● ⏱ ✓ ⏸ and emoji like 🐴
// have NO glyph and would make the logo "disappear". The pony mascot is drawn
// with the ASCII accent "(>')>" instead of an emoji.
func (t *TUI) logoLines(width int) []string {
	return t.buildLogo(width, t.paint)
}

// Logo returns the iCode wordmark banner as PLAIN (non-ANSI) lines, safe for
// non-VT consoles — e.g. piped output or `icode version`. Live values fall
// back to placeholders since no session is attached.
func Logo() []string {
	t := &TUI{}
	return t.buildLogo(80, func(_, s string) string { return s })
}

// logoPainter applies a colour; buildLogo takes it as a parameter so the same
// layout can be emitted coloured (TUI) or plain (Logo()).
type logoPainter func(color, s string) string

func (t *TUI) buildLogo(width int, paint logoPainter) []string {
	if width < 36 {
		return nil
	}
	cwd, _ := os.Getwd()
	short := shortDir(cwd)
	model := t.model
	if model == "" {
		model = "—"
	}
	provider := t.provider
	if provider == "" {
		provider = "—"
	}
	mode := t.mode
	if mode == "" {
		mode = ModeAuto
	}

	brand := "* iCode " + appVersionStr() + "    多模型 AI 编程助手  (>')>"
	l1 := "Model:    " + model + "          Provider: " + provider
	l2 := "Mode:     " + mode + "               CWD: " + short

	var meter string
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
		bar := strings.Repeat("█", filled) + strings.Repeat("░", cells-filled)
		meter = fmt.Sprintf("Context:  %dK / %dK [%s] %d%%", t.contextTokens/1000, t.contextWindow/1000, bar, pct)
	} else {
		meter = "Context:  —"
	}
	cache := "Cache:    "
	if t.cacheHitRate > 0 {
		cache += fmt.Sprintf("%.0f%%", t.cacheHitRate*100)
	} else {
		cache += "—"
	}
	commands := "/help  /model  /provider  /mode  /clear  /exit"

	contents := []string{brand, l1, l2, meter, cache, commands}
	cw := 0
	for _, c := range contents {
		if w := visibleWidth(c); w > cw {
			cw = w
		}
	}
	if cw < 24 {
		cw = 24
	}
	if cw+4 > width {
		// Too narrow for the full banner — let the caller fall back.
		return nil
	}
	innerDash := cw + 2

	top := paint("dim", "┌"+repeat("─", innerDash)+"┐")
	bot := paint("dim", "└"+repeat("─", innerDash)+"┘")
	sep := paint("dim", "│ "+repeat("─", cw)+" │")
	row := func(inner string) string {
		return paint("dim", "│ ") + inner + paint("dim", " │")
	}
	kv := func(s string) string {
		// Colour labels (words ending with ":") dim and values cyan, but preserve
		// the original spacing exactly. Keeping the original spaces avoids both
		// compressing deliberate padding and introducing subtle width differences
		// on terminals that handle colour-switched runs in unexpected ways.
		var b strings.Builder
		i := 0
		for i < len(s) {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == ' ' {
				b.WriteRune(r)
				i += size
				continue
			}
			j := i
			for j < len(s) {
				r2, sz := utf8.DecodeRuneInString(s[j:])
				if r2 == ' ' {
					break
				}
				j += sz
			}
			word := s[i:j]
			switch {
			case strings.HasSuffix(word, ":"):
				b.WriteString(paint("dim", word))
			case word == "(>')>":
				b.WriteString(paint("yellow", word))
			default:
				b.WriteString(paint("cyan", word))
			}
			i = j
		}
		return b.String()
	}

	out := []string{top}
	out = append(out, row(fitVis(paint("magenta", brand), cw)))
	out = append(out, sep)
	out = append(out, row(fitVis(kv(l1), cw)))
	out = append(out, row(fitVis(kv(l2), cw)))
	out = append(out, row(fitVis(kv(meter), cw)))
	out = append(out, row(fitVis(kv(cache), cw)))
	out = append(out, sep)
	out = append(out, row(fitVis(paint("dim", commands), cw)))
	out = append(out, bot)
	return out
}
