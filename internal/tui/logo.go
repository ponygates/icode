package tui

import "strings"

// asciiLogo is the iCODE wordmark rendered with the figlet "standard" font.
// Kept as a raw string literal so the backslashes need no escaping.
const asciiLogo = ` _  ____ ___  ____  _____
(_)/ ___/ _ \|  _ \| ____|
| | |  | | | | | | |  _|
| | |__| |_| | |_| | |___
|_|\____\___/|____/|_____|`

// logoLines returns the ASCII logo split into lines. When t.color is true the
// wordmark is accentuated with the brand cyan; otherwise it is returned plain
// so it stays readable in non-VT (line) consoles.
func (t *TUI) logoLines() []string {
	raw := strings.Split(asciiLogo, "\n")
	out := make([]string, len(raw))
	for i, l := range raw {
		out[i] = t.paint("cyan", l)
	}
	return out
}

// Logo returns the ASCII wordmark as plain lines (no ANSI), safe for non-VT
// consoles and for the line-mode startup banner.
func Logo() []string {
	return strings.Split(asciiLogo, "\n")
}
