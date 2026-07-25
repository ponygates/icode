// Package context provides project + user context loading for iCode.
//
// Loading precedence (from lowest to highest priority — later entries override
// / extend earlier ones):
//
//  1. User memory: ~/.icode/CLAUDE.md, ~/.icode/AGENTS.md, or ~/.icode/ICODE.md
//     (first one found). These carry global user preferences that apply to
//     every project.
//
//  2. Project memory: ICODE.md / CLAUDE.md / AGENTS.md discovered by walking
//     from the current working directory upward (up to maxParentLevels
//     parents). Ancestors are loaded first so files closer to the CWD carry
//     more specific / more authoritative guidance.
//
//  3. @import syntax: within any of the above files, a line of the form
//     `@relative/or/absolute/path.md` is inlined at the location of the
//     directive (up to importMaxDepth levels deep, with cycle detection).
//     This lets teams factor out shared conventions into a common file that
//     every project's ICODE.md includes.
//
// This matches Claude Code's multi-layer CLAUDE.md model and opencode's
// AGENTS.md model, so users coming from either tool get a familiar experience.
package context

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// maxParentLevels is how many directories above the CWD to search for
// project-level memory files.
const maxParentLevels = 5

// importMaxDepth caps recursive @import expansion so a cycle or a runaway
// chain cannot blow the stack or the context window.
const importMaxDepth = 3

// candidateFileNames lists the memory file names we recognise, in priority
// order (the first that exists at a given directory wins).
var candidateFileNames = []string{"ICODE.md", "CLAUDE.md", "AGENTS.md"}

// UserMemoryPath returns the absolute path of the user-level memory file,
// creating parent directories if needed. The file itself is NOT created — the
// caller must handle the "not exists" case (typically by returning an empty
// string on read).
func UserMemoryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".icode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "CLAUDE.md"), nil
}

// LoadProjectContext returns the combined user + project + imported context
// as a single string ready to be prepended to the system prompt.
//
// Sections are separated by a header identifying their source so the model
// can tell which layer a directive came from ("user memory" vs a specific
// project directory).
func LoadProjectContext() string {
	var parts []string

	// 1. User memory (global preferences)
	if user := loadUserMemory(); user != "" {
		parts = append(parts, formatSection("user memory (~/.icode)", user))
	}

	// 2. Project memory — walk from outermost parent inward so the CWD file
	//    appears last (highest priority in the concatenated prompt).
	cwd, err := os.Getwd()
	if err == nil {
		dirs := []string{cwd}
		parent := cwd
		for i := 0; i < maxParentLevels; i++ {
			next := filepath.Dir(parent)
			if next == parent {
				break
			}
			dirs = append(dirs, next)
			parent = next
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			content, ok := readFirstExisting(dirs[i], candidateFileNames)
			if !ok {
				continue
			}
			expanded := expandImports(content, dirs[i], 0, map[string]bool{})
			parts = append(parts, formatSection(dirs[i], expanded))
		}
	}

	return strings.Join(parts, "\n\n")
}

// AppendUserMemory appends a line of text to the user memory file. Used by
// the TUI's `#` shortcut so users can quickly record a preference without
// leaving the chat.
func AppendUserMemory(text string) error {
	path, err := UserMemoryPath()
	if err != nil {
		return err
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty memory text")
	}

	// Read existing (may not exist)
	existing, _ := os.ReadFile(path)

	// Ensure the file ends with a section header the first time we touch it,
	// so users can find their notes easily. Subsequent writes just append a
	// bullet.
	var buf strings.Builder
	if len(existing) == 0 {
		buf.WriteString("# iCode User Memory\n\n")
		buf.WriteString("Quick notes captured via the `#` shortcut in the chat.\n\n")
		buf.WriteString("## Notes\n\n")
	} else {
		buf.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			buf.WriteString("\n")
		}
	}
	buf.WriteString("- ")
	buf.WriteString(text)
	buf.WriteString("\n")

	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

// AppendProjectMemory appends a bullet note to the project memory file in
// the current working directory (ICODE.md, or the first recognised memory
// file that exists). Returns the path written to.
func AppendProjectMemory(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("empty memory text")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cwd, "ICODE.md")
	for _, name := range candidateFileNames {
		p := filepath.Join(cwd, name)
		if _, statErr := os.Stat(p); statErr == nil {
			path = p
			break
		}
	}
	existing, _ := os.ReadFile(path)
	var buf strings.Builder
	if len(existing) == 0 {
		buf.WriteString("# ICODE.md — Project Memory\n\n## Notes\n\n")
	} else {
		buf.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			buf.WriteString("\n")
		}
	}
	buf.WriteString("- " + text + "\n")
	return path, os.WriteFile(path, []byte(buf.String()), 0o644)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// loadUserMemory returns the first user-level memory file that exists under
// ~/.icode/, or "" if none.
func loadUserMemory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".icode")
	content, ok := readFirstExisting(dir, candidateFileNames)
	if !ok {
		return ""
	}
	return expandImports(content, dir, 0, map[string]bool{})
}

// readFirstExisting tries each candidate file name in dir and returns the
// content of the first one whose file is present and non-empty.
func readFirstExisting(dir string, names []string) (string, bool) {
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		return content, true
	}
	return "", false
}

// importPattern matches an `@path/to/file.md` directive that occupies its
// own line (leading whitespace allowed). We deliberately only recognise the
// directive at the start of a line so a `@` inside prose or code samples is
// never mistaken for an import.
var importPattern = regexp.MustCompile(`(?m)^[ \t]*@([^\s@]+)[ \t]*$`)

// expandImports inlines any `@path.md` directives inside content. Relative
// paths are resolved against baseDir. Cycles are detected by tracking
// canonical absolute paths in `visited`. Depth is capped by importMaxDepth to
// bound worst-case token usage.
func expandImports(content, baseDir string, depth int, visited map[string]bool) string {
	if depth >= importMaxDepth {
		return content
	}

	return importPattern.ReplaceAllStringFunc(content, func(match string) string {
		sub := importPattern.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		target := sub[1]

		// Resolve against baseDir
		if !filepath.IsAbs(target) {
			target = filepath.Join(baseDir, target)
		}
		abs, err := filepath.Abs(target)
		if err != nil {
			return match
		}
		if visited[abs] {
			// Cycle — leave the directive untouched so the operator can
			// spot the problem in the resulting prompt.
			return fmt.Sprintf("[iCode: skipped cyclic import %s]", target)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return fmt.Sprintf("[iCode: import failed %s: %v]", target, err)
		}
		visited[abs] = true
		defer delete(visited, abs)

		inner := strings.TrimSpace(string(data))
		return expandImports(inner, filepath.Dir(abs), depth+1, visited)
	})
}

// formatSection wraps a memory chunk with a header noting its origin so the
// model can attribute directives to the right layer.
func formatSection(origin, content string) string {
	return fmt.Sprintf("# Project Context (%s)\n\n%s", origin, content)
}

// LoadProjectAnalysis scans the project root for structural metadata
// (go.mod, README, CI config, etc.) and returns a formatted summary
// string, or "" if no project is detected.
func LoadProjectAnalysis() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	var parts []string

	// 1. Read go.mod (Go project)
	if data, err := os.ReadFile(filepath.Join(cwd, "go.mod")); err == nil {
		moduleName := extractGoModule(string(data))
		goVersion := extractGoVersion(string(data))
		deps := extractGoDeps(string(data))
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Language: Go\n"))
		if moduleName != "" {
			b.WriteString(fmt.Sprintf("Module: %s\n", moduleName))
		}
		if goVersion != "" {
			b.WriteString(fmt.Sprintf("Go version: %s\n", goVersion))
		}
		if len(deps) > 0 {
			b.WriteString(fmt.Sprintf("Dependencies: %s\n", strings.Join(deps, ", ")))
		}
		parts = append(parts, b.String())
	}

	// 2. Read package.json (Node/JS/TS project)
	if data, err := os.ReadFile(filepath.Join(cwd, "package.json")); err == nil {
		name := extractJSONField(string(data), "name")
		scripts := extractJSONScripts(string(data))
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Language: JavaScript/TypeScript\n"))
		if name != "" {
			b.WriteString(fmt.Sprintf("Project: %s\n", name))
		}
		if len(scripts) > 0 {
			b.WriteString(fmt.Sprintf("Scripts: %s\n", strings.Join(scripts, ", ")))
		}
		parts = append(parts, b.String())
	}

	// 3. Read README for project description
	for _, name := range []string{"README.md", "README_zh.md", "README_en.md"} {
		if data, err := os.ReadFile(filepath.Join(cwd, name)); err == nil {
			summary := extractReadmeSummary(string(data))
			if summary != "" {
				parts = append(parts, fmt.Sprintf("Description: %s", summary))
			}
			break
		}
	}

	// 4. Check CI configuration
	ciDir := filepath.Join(cwd, ".github", "workflows")
	if entries, err := os.ReadDir(ciDir); err == nil && len(entries) > 0 {
		var workflows []string
		for _, e := range entries {
			if !e.IsDir() {
				workflows = append(workflows, e.Name())
			}
		}
		if len(workflows) > 0 {
			parts = append(parts, fmt.Sprintf("CI: %s", strings.Join(workflows, ", ")))
		}
	}

	// 5. Check for Makefile
	if _, err := os.Stat(filepath.Join(cwd, "Makefile")); err == nil {
		parts = append(parts, "Build: Makefile")
	}

	if len(parts) == 0 {
		return ""
	}
	return "## Project Analysis\n" + strings.Join(parts, "\n") + "\n"
}

func extractGoModule(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(line[7:])
		}
	}
	return ""
}

func extractGoVersion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(line[3:])
		}
	}
	return ""
}

func extractGoDeps(content string) []string {
	var deps []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "v") && strings.Contains(line, ".") {
			if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "  ") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					dep := strings.TrimSuffix(parts[0], "/")
					short := filepath.Base(dep)
					if short != "" && short != dep {
						deps = append(deps, short)
					}
				}
			}
		}
	}
	if len(deps) > 5 {
		deps = deps[:5]
		deps = append(deps, "...")
	}
	return deps
}

func extractJSONField(content, field string) string {
	prefix := fmt.Sprintf(`"%s": "`, field)
	idx := strings.Index(content, prefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(prefix)
	end := strings.Index(content[start:], `"`)
	if end < 0 {
		return ""
	}
	return content[start : start+end]
}

func extractJSONScripts(content string) []string {
	prefix := `"scripts": {`
	idx := strings.Index(content, prefix)
	if idx < 0 {
		return nil
	}
	start := idx + len(prefix)
	depth := 1
	var scripts []string
	for i := start; i < len(content) && depth > 0; i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
		case '"':
			if depth == 1 {
				closeIdx := strings.Index(content[i+1:], `"`)
				if closeIdx >= 0 {
					key := content[i+1 : i+1+closeIdx]
					if !strings.HasPrefix(key, "//") {
						scripts = append(scripts, key)
					}
					i += closeIdx + 1
				}
			}
		}
	}
	if len(scripts) > 5 {
		scripts = scripts[:5]
		scripts = append(scripts, "...")
	}
	return scripts
}

func extractReadmeSummary(content string) string {
	// Remove YAML frontmatter
	if strings.HasPrefix(content, "---") {
		if end := strings.Index(content[3:], "\n---"); end >= 0 {
			content = content[end+5:]
		}
	}
	// Remove markdown headers, take the first meaningful paragraph
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ">") {
			continue
		}
		// Remove badges and links
		trimmed = stripMarkdownURLs(trimmed)
		trimmed = strings.TrimSpace(trimmed)
		if len(trimmed) > 30 {
			if len(trimmed) > 200 {
				trimmed = trimmed[:200] + "..."
			}
			return trimmed
		}
	}
	return ""
}

func stripMarkdownURLs(s string) string {
	// Remove [text](url) and [text](url "title")
	re := regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	s = re.ReplaceAllString(s, "$1")
	// Remove image references ![alt](url)
	re = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	s = re.ReplaceAllString(s, "")
	return s
}
