// agent memory — persistent per-agent knowledge directories (Claude Code
// parity). Each sub-agent can own a MEMORY.md that survives across sessions,
// letting it accumulate project-specific knowledge (codepaths, patterns,
// debugging insights) over time.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MemoryScope controls where an agent's persistent memory lives.
//
//	user    ~/.icode/agent-memory/<agent>/      — remembered across ALL projects
//	project <proj>/.icode/agent-memory/<agent>/  — project-specific, shareable via VCS
//	local   <proj>/.icode/agent-memory-local/<agent>/ — project-specific, private
type MemoryScope string

const (
	MemoryUser    MemoryScope = "user"
	MemoryProject MemoryScope = "project"
	MemoryLocal   MemoryScope = "local"
)

const (
	memoryFileName   = "MEMORY.md"
	memoryMaxHead    = 25 * 1024 // inject at most 25KB of MEMORY.md into the prompt
	memoryMaxLines   = 200       // or 200 lines, whichever comes first
	memoryPromptNote = "\n\n# Persistent memory\n" +
		"You have a persistent memory directory. Its MEMORY.md (head below) is reloaded " +
		"every time you run. Consult it before starting work and update it after completing " +
		"a task: write concise notes about what you found, codepaths, library locations, and " +
		"architectural decisions, so future runs benefit.\n\n"
)

// NormalizeMemoryScope validates a raw scope string ("", user/project/local).
func NormalizeMemoryScope(raw string) MemoryScope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user":
		return MemoryUser
	case "project":
		return MemoryProject
	case "local":
		return MemoryLocal
	default:
		return ""
	}
}

// String returns the canonical lowercase scope name ("" when disabled).
func (s MemoryScope) String() string { return string(s) }

// MemoryDir returns the memory directory for the given agent+scope, creating
// it (with an empty MEMORY.md) on first use. projectDir is only consulted for
// project/local scopes; empty means cwd.
func MemoryDir(scope MemoryScope, agentName, projectDir string) (string, error) {
	if scope == "" {
		return "", fmt.Errorf("empty memory scope")
	}
	if agentName == "" {
		return "", fmt.Errorf("empty agent name")
	}
	var dir string
	switch scope {
	case MemoryUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".icode", "agent-memory", agentName)
	case MemoryProject, MemoryLocal:
		base := "agent-memory"
		if scope == MemoryLocal {
			base = "agent-memory-local"
		}
		if projectDir == "" {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			projectDir = wd
		}
		dir = filepath.Join(projectDir, ".icode", base, agentName)
	default:
		return "", fmt.Errorf("unknown memory scope %q", scope)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	mem := filepath.Join(dir, memoryFileName)
	if _, err := os.Stat(mem); os.IsNotExist(err) {
		header := fmt.Sprintf("# %s agent memory\n\nPersistent notes accumulate here across sessions.\n", agentName)
		if err := os.WriteFile(mem, []byte(header), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// LoadMemoryHead returns the first maxLines lines / maxBytes bytes of the
// agent's MEMORY.md ("" when absent). Used to inject institutional knowledge
// into the sub-agent's system prompt.
func LoadMemoryHead(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, memoryFileName))
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > memoryMaxHead {
		data = data[:memoryMaxHead]
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > memoryMaxLines {
		lines = lines[:memoryMaxLines]
	}
	return strings.Join(lines, "\n")
}

// memoryBlock builds the system-prompt section for an agent with memory
// enabled. Returns "" when the scope is disabled.
func memoryBlock(scope, agentName, projectDir string) (string, error) {
	dir, err := MemoryDir(MemoryScope(scope), agentName, projectDir)
	if err != nil {
		return "", err
	}
	head := LoadMemoryHead(dir)
	block := memoryPromptNote
	if strings.TrimSpace(head) != "" {
		block += "<MEMORY.md head>\n" + head + "\n</MEMORY.md>\n"
	} else {
		block += "(MEMORY.md is currently empty.)\n"
	}
	return block, nil
}
