package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/slashcmd"
	"github.com/ponygates/icode/internal/types"
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
		// Argument-level completion: "/model " (space typed) offers values
		// for commands that take enumerable arguments — Claude Code parity.
		if i := strings.IndexByte(buf, ' '); i >= 0 && !strings.Contains(buf[i+1:], " ") {
			if items := t.argSuggestions(buf[:i], buf[i+1:]); len(items) > 0 {
				t.acOpen = true
				t.acItems = items
				t.clampAcIdx()
				return
			}
			t.acOpen = false
			t.acItems = nil
			return
		}
		if strings.Contains(buf, " ") {
			t.acOpen = false
			t.acItems = nil
			return
		}
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

// argSuggestions returns completion entries for the argument of a slash
// command (e.g. /model → model list; /lang → locales). partial is the text
// typed after the command's space.
func (t *TUI) argSuggestions(cmd, partial string) []acItem {
	var values []string
	switch strings.ToLower(cmd) {
	case "/model":
		t.mu.Lock()
		values = append(values, t.models...)
		t.mu.Unlock()
	case "/lang":
		values = []string{"zh-CN", "zh-TW", "en"}
	case "/theme":
		values = []string{"auto", "dark", "light"}
	case "/security":
		values = []string{"local", "desensitize", "local-llm", "foreign-llm", "unrestricted"}
	case "/mode":
		values = []string{"plan", "agent", "auto", "yolo"}
	default:
		return nil
	}
	var items []acItem
	for _, v := range values {
		if partial != "" && fuzzyScore(partial, v) < 0 {
			continue
		}
		items = append(items, acItem{Name: v, Desc: t.tstr("ac.arg"), ArgPrefix: cmd + " "})
	}
	return items
}

// clampAcIdx keeps the autocomplete cursor inside the item bounds.
func (t *TUI) clampAcIdx() {
	if t.acIdx >= len(t.acItems) {
		t.acIdx = len(t.acItems) - 1
	}
	if t.acIdx < 0 {
		t.acIdx = 0
	}
}

// filesAutocomplete returns files and directories that match the given
// prefix (text after the @ sign). Matching is fuzzy (subsequence) with
// prefix matches ranked first, mirroring slash-command behaviour. Results
// are gitignore-aware: directories named .git, node_modules, vendor, dist,
// target, release are excluded.
func (t *TUI) filesAutocomplete(prefix string) []acItem {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	// Determine the directory to search.
	searchDir := cwd
	if strings.Contains(prefix, "/") {
		// User typed a sub-path: "src/foo" searches cwd/src/
		rel := filepath.Dir(prefix)
		searchDir = filepath.Join(cwd, rel)
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	basePattern := strings.TrimPrefix(prefix, filepath.Dir(prefix)+"/")
	if basePattern == filepath.Dir(prefix) {
		basePattern = prefix
	}
	type scored struct {
		item  acItem
		score int
	}
	var items []scored
	for _, e := range entries {
		name := e.Name()
		if isIgnoredDir(name) {
			continue
		}
		s := fuzzyScore(basePattern, name)
		if basePattern != "" && s < 0 {
			continue
		}
		relPath := strings.TrimPrefix(filepath.Join(filepath.Dir(prefix), name), ".")
		relPath = strings.TrimPrefix(relPath, "/")
		if e.IsDir() {
			// Directories rank just below equally-scoring files so exact
			// file matches surface first.
			items = append(items, scored{acItem{Name: relPath + "/", Desc: "directory"}, s - 1})
		} else {
			info, _ := e.Info()
			size := ""
			if info != nil {
				size = formatFileSize(info.Size())
			}
			items = append(items, scored{acItem{Name: relPath, Desc: size}, s})
		}
		if len(items) >= 200 {
			break
		}
	}
	// Best score first (stable insertion sort — tiny lists).
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].score < items[j-1].score; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	out := make([]acItem, 0, len(items))
	for _, it := range items {
		out = append(out, it.item)
		if len(out) >= 50 {
			break
		}
	}
	return out
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
func (t *TUI) expandFileRefs(text string) (string, []types.Attachment) {
	var result strings.Builder
	var atts []types.Attachment
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
		// Images are inlined as true multimodal attachments (Claude Code
		// parity) so the model can actually see them — both for Ctrl+V pastes
		// and manual `@photo.png` references. readImageFile validates magic
		// bytes, size (≤25MB) and MIME, so a JPEG lacking NUL bytes is still
		// recognised (the old 0x00-only guard would have inlined it as junk).
		if b64, mime, ok := readImageFile(fullPath); ok {
			atts = append(atts, types.Attachment{Type: "image", MIMEType: mime, Data: b64})
			result.WriteString(fmt.Sprintf("[📎 图片: %s]\n", filepath.Base(fullPath)))
			remaining = rest
			continue
		}
		// Other binary files (e.g. .exe, .zip): never inline as text.
		if bytes.Contains(data, []byte{0}) {
			result.WriteString(fmt.Sprintf("[file: %s] (二进制文件，未内联内容，路径: %s)\n", path, fullPath))
			remaining = rest
			continue
		}
		result.WriteString(fmt.Sprintf("[file: %s]\n%s\n", path, strings.TrimSpace(string(data))))
		remaining = rest
	}
	return result.String(), atts
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

// rankSuggestions orders the completion list: frequently-used commands first
// (persisted usage history, C8), then recently used (session recency), then
// built-in definition order. Custom commands keep their relative order after
// matching built-ins.
func (t *TUI) rankSuggestions(items []acItem) {
	t.mu.Lock()
	recent := append([]string(nil), t.recentCmds...)
	usage := make(map[string]int, len(t.cmdUsage))
	for k, v := range t.cmdUsage {
		usage[k] = v
	}
	t.mu.Unlock()
	if len(recent) == 0 && len(usage) == 0 {
		return
	}
	pos := map[string]int{}
	for i, name := range recent {
		pos[name] = len(recent) - i // higher = more recent
	}
	stableSortByUsage(items, usage, pos)
}

// stableSortByUsage is an insertion sort keyed on (usage count, recency rank)
// — most-used first, ties broken by recency, definition order as the stable
// fallback. Lists are tiny (<90 entries) so insertion sort is fine.
func stableSortByUsage(items []acItem, usage map[string]int, pos map[string]int) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && usageRank(items[j].Name, usage, pos) > usageRank(items[j-1].Name, usage, pos); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// usageRank combines a command's persisted usage count (primary) with its
// session recency (secondary) into a single sort key. Commands never used
// score 0 and keep definition order among themselves.
func usageRank(name string, usage map[string]int, pos map[string]int) int {
	u := usage[name]
	if u > 0 {
		// usage dominates; add recency as a tiny secondary component.
		return u*1000 + pos[name]
	}
	return pos[name] // never-used but recently typed still floats up a bit
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

	// Argument completion for enumerable slash-command values: replace only
	// the argument part after the command token ("/model " + value).
	if it.ArgPrefix != "" {
		t.inputBuf = it.ArgPrefix + it.Name + " "
		t.cursor = len([]rune(t.inputBuf))
		t.acOpen = false
		return
	}

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
	out = append(out, t.paint("dim", "  ▾"+t.tstr("ac.title")+"   ("+t.tstr("ac.hint")+")"))

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
			// Claude Code parity: the highlighted row carries an explicit
			// "tab" affordance so the accept key is always discoverable.
			out = append(out, "  "+t.c("cyan")+"> "+name+" "+it.Desc+t.paint("dim", "  (tab)")+"\x1b[0m")
		} else {
			out = append(out, "    "+t.paint("dim", name+" "+it.Desc))
		}
	}
	// "N more" pager (Claude Code parity): tell the user the window is a
	// filtered view and typing narrows it.
	if hidden := len(t.acItems) - len(show); hidden > 0 {
		out = append(out, "    "+t.paint("dim", fmt.Sprintf("(还有 %d 项 — 继续输入以过滤)", hidden)))
	}
	return out
}
