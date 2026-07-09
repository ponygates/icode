// Package context provides project context loading for iCode.
//
// It reads ICODE.md files from the current working directory and parent
// directories (up to 3 levels up), similar to Claude Code's CLAUDE.md support.
// The combined content is prepended to the conversation system prompt so the
// agent is aware of project-specific conventions, build commands, and architecture.
package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxParentLevels is how many directories above the CWD to search for ICODE.md.
const maxParentLevels = 3

// icodeFileName is the project context file name.
const icodeFileName = "ICODE.md"

// LoadProjectContext reads ICODE.md from the current working directory and
// parent directories (up to maxParentLevels). The contents are combined from
// the outermost parent down to the current directory, so more specific files
// (closer to CWD) appear last and can override broader guidance.
//
// If no ICODE.md files are found, an empty string is returned.
func LoadProjectContext() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Collect candidate directories: CWD first, then up to maxParentLevels parents.
	dirs := []string{cwd}
	parent := cwd
	for i := 0; i < maxParentLevels; i++ {
		next := filepath.Dir(parent)
		if next == parent {
			break // reached filesystem root
		}
		dirs = append(dirs, next)
		parent = next
	}

	// Walk from the outermost directory inward so the CWD content comes last.
	var parts []string
	for i := len(dirs) - 1; i >= 0; i-- {
		content, ok := readICodeFile(dirs[i])
		if !ok {
			continue
		}
		// Annotate each section with its source directory so the agent can
		// tell which project level the guidance came from.
		parts = append(parts, formatSection(dirs[i], content))
	}

	return strings.Join(parts, "\n\n")
}

// readICodeFile reads ICODE.md from dir. Returns (content, true) on success.
func readICodeFile(dir string) (string, bool) {
	path := filepath.Join(dir, icodeFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", false
	}
	return content, true
}

// formatSection wraps a file's content with a header noting its origin path.
func formatSection(dir, content string) string {
	return fmt.Sprintf("# Project Context (%s)\n\n%s", dir, content)
}
