package tui

// icodeLogos holds progressively richer ASCII wordmarks for the startup
// banner, chosen by available terminal width. Each entry pairs the art with a
// per-row colour gradient (magenta → purple → blue → cyan) for a polished,
// Claude-Code-like look that stays crisp on any UTF-8 terminal.
var icodeLogos = []struct {
	minWidth int
	lines    []string
	gradient []string
}{
	{40, []string{
		"██╗ ██████╗ ██████╗ ██████╗ ███████╗",
		"██║██╔════╝██╔═══██╗██╔══██╗██╔════╝",
		"██║██║     ██║   ██║██║  ██║█████╗  ",
		"██║██║     ██║   ██║██║  ██║██╔══╝  ",
		"██║╚██████╗╚██████╔╝██████╔╝███████╗",
		"╚═╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝",
	}, []string{"magenta", "magenta", "purple", "blue", "cyan", "cyan"}},
	{30, []string{
		"    _ ______          __   ",
		"   (_) ____/___  ____/ /__ ",
		"  / / /   / __ \\/ __  / _ \\",
		" / / /___/ /_/ / /_/ /  __/",
		"/_/\\____/\\____/\\__,_/\\___/ ",
	}, []string{"magenta", "purple", "blue", "cyan", "cyan"}},
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
	// Primary wordmark (the ansi_shadow art) without colour escapes.
	primary := icodeLogos[0].lines
	out := make([]string, len(primary))
	for i, l := range primary {
		out[i] = l
	}
	return out
}
