package tui

// icodeLogos holds progressively richer ASCII wordmarks for the startup
// banner. They use only plain ASCII glyphs (no box-drawing or block-element
// Unicode) so they stay crisp and aligned on every terminal, including
// Windows conhost and CJK fonts where ambiguous-width Unicode blocks often
// shift or break apart.
var icodeLogos = []struct {
	minWidth int
	lines    []string
	gradient []string
}{
	{45, []string{
		`   _    ___           __  `,
		`  (_)  / _ \         / _| `,
		`   _  | | | | ___    | |_ `,
		`  | | | | | |/ _ \   |  _|`,
		`  | | | |_| |  __/   | |  `,
		`  |_|  \___/ \___/   |_|  `,
	}, []string{"magenta", "magenta", "purple", "blue", "cyan", "cyan"}},
	{30, []string{
		`  _   ___        __ `,
		` (_) / _ \      / _|`,
		`  _ | | | | ___| |_ `,
		` | || | | |/ _ \  _|`,
		` | || |_| |  __/ |  `,
		` |_| \___/ \___|_|  `,
	}, []string{"magenta", "magenta", "purple", "blue", "cyan", "cyan"}},
}

// logoLines picks the richest ASCII wordmark that fits the terminal width and
// returns it with its per-row colour gradient applied (nil when none fits, so
// very narrow terminals simply skip the logo).
func (t *TUI) logoLines(width int) []string {
	for _, lg := range icodeLogos {
		if width >= lg.minWidth {
			var out []string
			for i, ln := range lg.lines {
				color := "magenta"
				if i < len(lg.gradient) {
					color = lg.gradient[i]
				}
				out = append(out, "   "+t.paint(color, ln))
			}
			return out
		}
	}
	return nil
}

// Logo returns the ASCII wordmark as plain lines (no ANSI), safe for non-VT
// consoles and for the CLI startup banner (e.g. `icode` / `icode about`).
func Logo() []string {
	// Primary wordmark without colour escapes.
	primary := icodeLogos[0].lines
	out := make([]string, len(primary))
	for i, l := range primary {
		out[i] = l
	}
	return out
}
