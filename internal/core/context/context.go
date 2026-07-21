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
