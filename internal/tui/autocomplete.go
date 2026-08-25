package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/slashcmd"
)

// 鈹€鈹€ Helpers 鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€

// persistSetting loads config, applies fn, and writes it back to disk.
// Errors are reported via the UI rather than silently ignored. When the host
// installed an OnConfigChanged hook (wired in cmd/commands.go), it is invoked
// afterwards so engine-side settings derived from config —?most importantly
// the system prompt carrying the language directive —?refresh immediately.
func (t *TUI) persistSetting(fn func(*config.Config)) {
	cfg, err := config.Load()
	if err != nil {
		t.add(RoleError, "閰嶇疆鍔犺浇澶辫触: "+err.Error())
		return
	}
	fn(cfg)
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.add(RoleError, "閰嶇疆淇濆瓨澶辫触: "+err.Error())
	}
	if t.onConfigChanged != nil {
		t.onConfigChanged(cfg)
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
			if prefix == "/" {
				items = append(items, acItem{Name: d.Name, Desc: t.tstr(d.Key)})
				continue
			}
			if score := fuzzyScore(prefix, d.Name); score >= 0 {
				items = append(items, acItem{Name: d.Name, Desc: t.tstr(d.Key) + usageHint(d.Name)})
			}
		}
		// Custom commands (.icode/commands/*.md) participate in completion
		// too —?Claude Code lists them alongside built-ins with their
		// frontmatter argument hints.
		customSeen := map[string]bool{}
		for _, it := range items {
			customSeen[it.Name] = true
		}
		for _, c := range slashcmd.CachedLoad(slashcmd.DefaultDirs()...).List() {
			if customSeen[c.Name] {
				continue
			}
			if prefix == "/" || fuzzyScore(prefix, c.Name) >= 0 {
				desc := c.Description
				if desc == "" {
					desc = t.tstr("ac.custom")
				}
				items = append(items, acItem{Name: c.Name, Desc: desc + " " + c.ArgumentHint})
			}
		}
		t.rankSuggestions(items)
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
	// @file completions —?matches Claude Code's file-attachment autocomplete.
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
		// User typed a sub-path: "src/foo" 鈫?search cwd/src/
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
	items := make([]acItem, 0, len(slashDefs)+8)
	seen := map[string]bool{}
	for _, d := range slashDefs {
		items = append(items, acItem{Name: d.Name, Desc: t.tstr(d.Key) + usageHint(d.Name)})
		seen[d.Name] = true
	}
	for _, c := range slashcmd.CachedLoad(slashcmd.DefaultDirs()...).List() {
		if seen[c.Name] {
			continue
		}
		desc := c.Description
		if desc == "" {
			desc = t.tstr("ac.custom")
		}
		items = append(items, acItem{Name: c.Name, Desc: desc + " " + c.ArgumentHint})
	}
	t.rankSuggestions(items)
	return items
}

// usageHint returns a dim argument placeholder for commands that take one,
// mirroring Claude Code's inline hints. Empty for zero-arg commands.
func usageHint(name string) string {
	hints := map[string]string{
		"/model":        " <model-id>",
		"/lang":         " <zh-CN|zh-TW|en>",
		"/theme":        " <auto|dark|light>",
		"/add-dir":      " <dir>",
		"/cd":           " <dir>",
		"/goal":         " set <goal> --verify <cmd> | show | clear",
		"/idle":         " <name> <desc>",
		"/security":     " <level>",
		"/config":       " key=value",
		"/output-style": " [style]",
		"/export":       " [file]",
		"/copy":         " [file]",
		"/compact":      " [instructions]",
		"/summarize":    " [focus]",
		"/resume":       " [session-id]",
		"/fork":         " <session-id>",
		"/lsp":          " diag <file>",
	}
	return hints[name]
}

// rankSuggestions orders the completion list: recently used commands first
// (recency), then built-in definition order. Custom commands keep their
// relative order after matching built-ins.
func (t *TUI) rankSuggestions(items []acItem) {
	t.mu.Lock()
	recent := append([]string(nil), t.recentCmds...)
	t.mu.Unlock()
	if len(recent) == 0 {
		return
	}
	pos := map[string]int{}
	for i, name := range recent {
		pos[name] = len(recent) - i // higher = more recent
	}
	stableSortedByRecency(items, pos)
}

// stableSortedByRecency is an insertion sort keyed on recency rank (0 =
// never used). Stable, and the lists are tiny (<90 entries).
func stableSortedByRecency(items []acItem, pos map[string]int) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && pos[items[j].Name] > pos[items[j-1].Name]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// fuzzyScore scores name against a typed prefix using Claude Code-style
// subsequence matching: contiguous prefix match ranks best, then in-order
// subsequences penalised by the gap since the previous matched rune.
// Returns -1 when the query cannot be embedded in order.
func fuzzyScore(query, name string) int {
	if query == "" {
		return 0
	}
	if strings.HasPrefix(strings.ToLower(name), strings.ToLower(query)) {
		return 0
	}
	q := strings.ToLower(query)
	n := strings.ToLower(name)
	score, qi, last := 0, 0, 0
	for ni := 0; ni < len(n) && qi < len(q); ni++ {
		if n[ni] != q[qi] {
			continue
		}
		if qi > 0 {
			score -= ni - last - 1 // relative gap penalty
		}
		last = ni
		qi++
	}
	if qi < len(q) {
		return -1
	}
	return score
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
	// All completable commands: built-ins first (definition order), then
	// user-defined ones from .icode/commands.
	names := make([]string, 0, len(slashDefs)+8)
	for _, d := range slashDefs {
		names = append(names, d.Name)
	}
	for _, c := range slashcmd.CachedLoad(slashcmd.DefaultDirs()...).List() {
		dup := false
		for _, n := range names {
			if n == c.Name {
				dup = true
				break
			}
		}
		if !dup {
			names = append(names, c.Name)
		}
	}

	// Bare "/" — can't dispatch an empty command; fall back to the first one.
	if token == "/" {
		if len(names) == 0 {
			return text, false
		}
		return names[0] + rest, true
	}
	var first string
	for _, name := range names {
		if name == token {
			// Exact command — already complete, never rewrite it.
			return text, false
		}
		if first == "" && strings.HasPrefix(name, token) {
			first = name
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
	out = append(out, t.paint("dim", "  鈻?"+t.tstr("ac.title")+"   ("+t.tstr("ac.hint")+")"))

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
