package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ponygates/icode/internal/config"
)

// ── Helpers ──────────────────────────────────────────────────────

// persistSetting loads config, applies fn, and writes it back to disk.
// Errors are reported via the UI rather than silently ignored.
func (t *TUI) persistSetting(fn func(*config.Config)) {
	cfg, err := config.Load()
	if err != nil {
		t.add(RoleError, "配置加载失败: "+err.Error())
		return
	}
	fn(cfg)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.add(RoleError, "配置保存失败: "+err.Error())
	}
}

// updateSuggestions recomputes the autocomplete panel based on the current
// input. It opens when the input is empty (showing all commands) or starts
// with "/" (showing commands) or contains "@" (showing file completions).
func (t *TUI) updateSuggestions() {
	if !t.rawMode || t.streaming {
		t.acOpen = false
		t.acItems = nil
		return
	}
	buf := t.inputBuf
	if buf == "" {
		t.acOpen = true
		t.acItems = t.allSuggestions()
		t.acIdx = 0
		return
	}
	if strings.HasPrefix(buf, "/") {
		prefix := strings.TrimSpace(buf)
		var items []acItem
		for _, d := range slashDefs {
			if prefix == "/" || strings.HasPrefix(d.Name, prefix) {
				items = append(items, acItem{Name: d.Name, Desc: t.tstr(d.Key)})
			}
		}
		if len(items) == 0 {
			t.acOpen = false
			t.acItems = nil
			return
		}
		t.acOpen = true
		t.acItems = items
		if t.acIdx >= len(items) {
			t.acIdx = len(items) - 1
		}
		if t.acIdx < 0 {
			t.acIdx = 0
		}
		return
	}
	// @file completions — matches Claude Code's file-attachment autocomplete.
	if idx := strings.LastIndex(buf, "@"); idx >= 0 {
		prefix := buf[idx+1:] // text after the @
		items := t.filesAutocomplete(prefix)
		if len(items) > 0 {
			t.acOpen = true
			t.acItems = items
			if t.acIdx >= len(items) {
				t.acIdx = len(items) - 1
			}
			if t.acIdx < 0 {
				t.acIdx = 0
			}
			return
		}
	}
	t.acOpen = false
	t.acItems = nil
}

// filesAutocomplete returns files and directories that match the given
// prefix (text after the @ sign). Results are gitignore-aware: directories
// named .git, node_modules, vendor, dist, target, release are excluded.
func (t *TUI) filesAutocomplete(prefix string) []acItem {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	// Determine the directory to search.
	searchDir := cwd
	if strings.Contains(prefix, "/") {
		// User typed a sub-path: "src/foo" → search cwd/src/
		rel := filepath.Dir(prefix)
		searchDir = filepath.Join(cwd, rel)
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	var items []acItem
	basePattern := strings.TrimPrefix(prefix, filepath.Dir(prefix)+"/")
	if basePattern == filepath.Dir(prefix) {
		basePattern = prefix
	}
	for _, e := range entries {
		name := e.Name()
		if isIgnoredDir(name) {
			continue
		}
		relPath := strings.TrimPrefix(filepath.Join(filepath.Dir(prefix), name), ".")
		relPath = strings.TrimPrefix(relPath, "/")
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(basePattern)) {
			continue
		}
		if e.IsDir() {
			items = append(items, acItem{Name: relPath + "/", Desc: "directory"})
		} else {
			info, _ := e.Info()
			size := ""
			if info != nil {
				size = formatFileSize(info.Size())
			}
			items = append(items, acItem{Name: relPath, Desc: size})
		}
		if len(items) >= 50 {
			break
		}
	}
	return items
}

// isIgnoredDir returns true for directories that should be hidden from
// @file autocomplete (mirrors common .gitignore patterns).
func isIgnoredDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "target", "release",
		"__pycache__", ".venv", ".next", "build", "out", ".icloud":
		return true
	}
	return false
}

// formatFileSize renders a byte count into a human-readable string.
func formatFileSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// expandFileRefs scans text for `@path/to/file` references (not preceded by
// a word character) and replaces them with [file: path]\n<content>\n. This
// lets users quickly attach file context in the TUI. For desktop/clients
// that handle file attachment natively, this is a no-op fallback.
func (t *TUI) expandFileRefs(text string) string {
	var result strings.Builder
	remaining := text
	for {
		idx := strings.Index(remaining, "@")
		if idx < 0 || (idx > 0 && isWordChar(remaining[idx-1])) {
			result.WriteString(remaining)
			break
		}
		result.WriteString(remaining[:idx])
		rest := remaining[idx+1:]

		// Extract the path: everything until whitespace or end.
		end := strings.IndexAny(rest, " \t\n")
		path := rest
		if end >= 0 {
			path = rest[:end]
			rest = rest[end:]
		} else {
			rest = ""
		}
		path = strings.TrimSpace(path)
		if path == "" {
			result.WriteString("@")
			remaining = rest
			continue
		}
		// Resolve relative to CWD
		fullPath := path
		if !filepath.IsAbs(path) {
			if cwd, err := os.Getwd(); err == nil {
				fullPath = filepath.Join(cwd, path)
			}
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			result.WriteString("@")
			result.WriteString(path)
			remaining = rest
			continue
		}
		result.WriteString(fmt.Sprintf("[file: %s]\n%s\n", path, strings.TrimSpace(string(data))))
		remaining = rest
	}
	return result.String()
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '.'
}

// allSuggestions returns every slash command as an autocomplete entry.
func (t *TUI) allSuggestions() []acItem {
	items := make([]acItem, 0, len(slashDefs))
	for _, d := range slashDefs {
		items = append(items, acItem{Name: d.Name, Desc: t.tstr(d.Key)})
	}
	return items
}

// completeSlashCommand expands an incomplete slash-command prefix when the
// user presses Enter, so a dangling "/x" or a bare "/" can never submit as an
// empty or unknown command. It completes to the FIRST command that matches the
// typed prefix (same order as Tab completion / the /help list), keeps any
// trailing arguments, and leaves exact commands untouched.
func (t *TUI) completeSlashCommand(text string) (string, bool) {
	if !strings.HasPrefix(text, "/") {
		return text, false
	}
	token, rest := text, ""
	if i := strings.IndexByte(text, ' '); i >= 0 {
		token, rest = text[:i], text[i:]
	}
	// Bare "/" — can't dispatch an empty command; fall back to the first one.
	if token == "/" {
		if len(slashDefs) == 0 {
			return text, false
		}
		return slashDefs[0].Name + rest, true
	}
	var first string
	for _, d := range slashDefs {
		if d.Name == token {
			// Exact command — already complete, never rewrite it.
			return text, false
		}
		if first == "" && strings.HasPrefix(d.Name, token) {
			first = d.Name
		}
	}
	if first != "" {
		return first + rest, true
	}
	return text, false
}

// acceptSuggestion replaces the input with the highlighted autocomplete
// entry. For slash commands this inserts the command name. For @file refs
// it replaces the "@prefix" with the file path and the file content.
func (t *TUI) acceptSuggestion() {
	if len(t.acItems) == 0 {
		return
	}
	it := t.acItems[t.acIdx]

	// If this is an @file autocomplete item (name starts with a path
	// separator or "./"), replace the "@prefix" with the file path.
	if strings.Contains(t.inputBuf, "@") && !strings.HasPrefix(it.Name, "/") {
		idx := strings.LastIndex(t.inputBuf, "@")
		path := strings.TrimPrefix(it.Name, "./")
		// Replace the @prefix with the file path
		t.inputBuf = t.inputBuf[:idx] + "@" + path + " "
		t.cursor = len([]rune(t.inputBuf))
		t.acOpen = false
		t.updateSuggestions()
		// When the user sends the message, the frontend will automatically
		// read the @file reference and prepend its content.
		return
	}

	t.inputBuf = it.Name + " "
	t.cursor = len([]rune(t.inputBuf))
	t.acOpen = false
	t.updateSuggestions()
}

// autocompleteLines renders the suggestion panel shown above the input line.
// Returns nil when there is nothing to show.
func (t *TUI) autocompleteLines() []string {
	if !t.rawMode || t.streaming || !t.acOpen || len(t.acItems) == 0 {
		return nil
	}
	var out []string
	out = append(out, t.paint("dim", "  ▾ "+t.tstr("ac.title")+"   ("+t.tstr("ac.hint")+")"))

	const maxShow = 9
	from := 0
	if t.acIdx >= maxShow {
		from = t.acIdx - maxShow + 1
	}
	show := t.acItems
	if from+maxShow < len(show) {
		show = show[from : from+maxShow]
	} else {
		show = show[from:]
	}

	for i, it := range show {
		globalIdx := from + i
		sel := globalIdx == t.acIdx
		name := padEnd(it.Name, 16)
		if sel {
			out = append(out, "  "+t.c("cyan")+"> "+name+" "+it.Desc+"\x1b[0m")
		} else {
			out = append(out, "    "+t.paint("dim", name+" "+it.Desc))
		}
	}
	return out
}
