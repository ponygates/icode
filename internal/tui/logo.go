package tui

import (
	"strings"
)

// icodeLogos holds progressively richer visual banners for the startup screen.
// The design uses full-width box-drawing rectangles: left panel for iCode branding,
// right panel for welcome/status info. The horse/pony motif is integrated into
// the right panel as a compact ASCII accent.
//
// Layout (wide variant, ≥90 cols):
//
//	┌──────────────────────────────────────────────────┐
//	│  ✦ iCode  v0.3                🐴 多模型 AI 编程助手 │
//	│  ──────────────────────────────────────────────── │
//	│  Model: deepseek-v4-flash     Mode: agent        │
//	│  Provider: deepseek           CWD: /project/src  │
//	│  Context: 42K/128K ████░░░░   Cache: 65%        │
//	└──────────────────────────────────────────────────┘
var icodeLogos = []struct {
	minWidth int
	lines    []string
}{
	// ── Full layout (≥90 cols) ──
	{90, []string{
		`┌─────────────────────────────────────────────────────────────────┐`,
		`│                                                                 │`,
		`│    ✦ iCode  v0.3                  🐴  多模型 AI 编程助手        │`,
		`│    ─────────────────────────────────────────────────────          │`,
		`│    Model:     deepseek-v4-flash         Provider: deepseek       │`,
		`│    Mode:      agent                     CWD: ~/project/src       │`,
		`│    Context:   42K / 128K ████░░░░░░░    Cache: 65%               │`,
		`│                                                                 │`,
		`│    ─────────────────────────────────────────────────────          │`,
		`│    /help  /model  /provider  /mode  /clear  /multiline  /exit   │`,
		`└─────────────────────────────────────────────────────────────────┘`,
	}},

	// ── Medium layout (≥68 cols) ──
	{68, []string{
		`┌───────────────────────────────────────────────────────┐`,
		`│                                                       │`,
		`│    ✦ iCode v0.3        🐴 多模型 AI 编程助手         │`,
		`│    ─────────────────────────────────────────              │`,
		`│    Model: deepseek-v4-flash  Provider: deepseek        │`,
		`│    Mode:  agent              CWD: ~/project            │`,
		`│    Context: 42K/128K ████░   Cache: 65%               │`,
		`│                                                       │`,
		`└───────────────────────────────────────────────────────┘`,
	}},

	// ── Compact layout (≥48 cols) ──
	{48, []string{
		`┌──────────────────────────────────────┐`,
		`│                                      │`,
		`│    ✦ iCode v0.3   🐴 编程助手        │`,
		`│    ──────────────────────────           │`,
		`│    Model: deepseek-v4-flash           │`,
		`│    Mode:  agent                      │`,
		`│                                      │`,
		`└──────────────────────────────────────┘`,
	}},

	// ── Mini layout (≥32 cols) ──
	{32, []string{
		`┌──────────────────────────┐`,
		`│  ✦ iCode v0.3  🐴       │`,
		`│  deepseek-v4-flash      │`,
		`│  mode: agent            │`,
		`└──────────────────────────┘`,
	}},
}

// logoLines picks the richest banner that fits the terminal width.
// Colors are applied: frame dim, content magenta, horse yellow.
func (t *TUI) logoLines(width int) []string {
	for _, lg := range icodeLogos {
		if width >= lg.minWidth {
			out := make([]string, len(lg.lines))
			for i, ln := range lg.lines {
				switch {
				case i == 0 || i == len(lg.lines)-1:
					// Top/bottom border → dim frame
					out[i] = t.paint("dim", ln)
				case strings.Contains(ln, "✦"):
					// iCode branding line → magenta
					out[i] = t.paint("magenta", ln)
				case strings.Contains(ln, "🐴"):
					// Horse accent line → warm
					out[i] = strings.Replace(ln, "🐴", t.paint("yellow", "🐴"), 1)
					out[i] = colorLogoContent(out[i], t)
				case strings.Contains(ln, "─"):
					// Separator line → dim
					out[i] = t.paint("dim", ln)
				default:
					// Content rows → apply colour per segment
					out[i] = colorLogoContent(ln, t)
				}
			}
			return out
		}
	}
	return nil
}

// colorLogoContent colours segments inside a banner content row.
// Left-aligned labels are dim, values are bright, horse emoji is warm.
func colorLogoContent(ln string, t *TUI) string {
	// Preserve frame borders
	if !strings.HasPrefix(ln, "│") {
		return ln
	}
	lnRunes := []rune(ln)
	if len(lnRunes) < 3 {
		return ln
	}
	leftBorder := string(lnRunes[0])
	content := string(lnRunes[1 : len(lnRunes)-1])
	rightBorder := string(lnRunes[len(lnRunes)-1])

	// Colour segments inside
	var colored strings.Builder
	colored.WriteString(t.paint("dim", leftBorder))

	// Split content by key:value boundaries
	segments := strings.Fields(content)
	for j, seg := range segments {
		if j > 0 {
			colored.WriteString(" ")
		}
		// Labels with colons → dim
		if strings.HasSuffix(seg, ":") {
			colored.WriteString(t.paint("dim", seg))
		} else if seg == "🐴" {
			colored.WriteString(t.paint("yellow", seg))
		} else {
			colored.WriteString(t.paint("cyan", seg))
		}
	}
	colored.WriteString(t.paint("dim", rightBorder))
	return colored.String()
}

// horseDetails returns supplementary horse art lines.
func horseDetails() []string {
	return []string{
		`     ,,,`,
		`   ,'  \,`,
		`  /__ __\`,
		` /  ))\  `,
		`/  /_/   `,
	}
}

// Logo returns the wordmark as plain lines (no ANSI), safe for non-VT consoles.
func Logo() []string {
	for _, lg := range icodeLogos {
		if 80 >= lg.minWidth {
			return lg.lines
		}
	}
	return icodeLogos[len(icodeLogos)-1].lines
}
