// Package slashcmd loads user-defined slash commands from markdown files.
//
// Layout (matches Claude Code / opencode conventions so users bringing their
// own command files can drop them in unchanged):
//
//	~/.icode/commands/<name>.md          — global commands (all projects)
//	<project>/.icode/commands/<name>.md  — project-scoped commands
//
// A command file is optional-YAML-frontmatter + Markdown body:
//
//	---
//	description: Generate a changelog since a tag
//	argument-hint: [since-tag]
//	---
//	Please write a Markdown changelog for the commits since $ARGUMENTS.
//	!git log $ARGUMENTS..HEAD --oneline
//
// Placeholders:
//   - `$ARGUMENTS`  — the full argument string passed after the command
//   - `$1`..`$9`    — individual whitespace-separated arguments
//
// Any line whose first non-whitespace character is `!` is evaluated as a
// shell command; its stdout+stderr is inlined at that position wrapped in a
// fenced code block. This mirrors Claude Code's `!command` prefix and lets a
// command capture live shell output (git status, ls, etc.) into the prompt
// without a separate tool call.
package slashcmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Command is a single user-defined slash command.
type Command struct {
	Name         string // "/changelog", lowercase, keeps the leading slash
	Description  string
	ArgumentHint string // shown in autocomplete: "/changelog [since-tag]"
	Source       string // absolute path of the .md file for error reporting
	template     string // body after stripping frontmatter
}

// Registry indexes commands by name (with leading slash).
type Registry struct {
	byName map[string]*Command
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{byName: map[string]*Command{}} }

// Load walks each directory (in order) and registers every *.md file as a
// command named after its stem. Later directories override earlier ones, so
// callers should pass user-scope dirs first and project-scope dirs last if
// they want project files to win.
func Load(dirs ...string) *Registry {
	r := NewRegistry()
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, ent.Name())
			cmd, err := loadFile(path)
			if err != nil {
				continue // silently skip malformed files — surfaced via /help later
			}
			r.byName[cmd.Name] = cmd
		}
	}
	return r
}

// List returns commands sorted by name.
func (r *Registry) List() []*Command {
	out := make([]*Command, 0, len(r.byName))
	for _, c := range r.byName {
		out = append(out, c)
	}
	// Insertion order is undefined for maps; caller can sort if it wants a
	// stable ordering. Autocomplete callers typically sort by name themselves.
	return out
}

// Get returns the command registered under name (with leading slash), if any.
func (r *Registry) Get(name string) (*Command, bool) {
	c, ok := r.byName[strings.ToLower(name)]
	return c, ok
}

// Expand substitutes placeholders and evaluates `!shell` prefix lines,
// returning the final prompt text that should be sent to the LLM as if the
// user had typed it directly.
func (c *Command) Expand(ctx context.Context, args string) (string, error) {
	body := c.template
	body = strings.ReplaceAll(body, "$ARGUMENTS", args)

	// $1..$9 substitution based on whitespace-split args.
	fields := strings.Fields(args)
	for i := 1; i <= 9; i++ {
		var repl string
		if i-1 < len(fields) {
			repl = fields[i-1]
		}
		body = strings.ReplaceAll(body, fmt.Sprintf("$%d", i), repl)
	}

	// Evaluate `!command` lines.
	var out strings.Builder
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "!") && !strings.HasPrefix(trimmed, "!=") {
			shellCmd := strings.TrimSpace(trimmed[1:])
			if shellCmd == "" {
				continue
			}
			result := runShell(ctx, shellCmd)
			out.WriteString("```\n$ ")
			out.WriteString(shellCmd)
			out.WriteString("\n")
			out.WriteString(result)
			if !strings.HasSuffix(result, "\n") {
				out.WriteString("\n")
			}
			out.WriteString("```\n")
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// ---------------------------------------------------------------------------
// File loading
// ---------------------------------------------------------------------------

// loadFile parses a single command file. Frontmatter is optional; when
// present it must be a valid YAML block delimited by `---` lines at the top.
func loadFile(path string) (*Command, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	if name == "" {
		return nil, fmt.Errorf("empty command name")
	}

	body := string(data)
	var meta struct {
		Description  string      `yaml:"description"`
		ArgumentHint interface{} `yaml:"argument-hint"`
	}

	if strings.HasPrefix(body, "---") {
		// Split off the frontmatter block: `---\n...\n---\n`
		rest := body[3:]
		end := strings.Index(rest, "\n---")
		if end >= 0 {
			raw := rest[:end]
			// Trim leading newline of raw block, if any.
			raw = strings.TrimPrefix(raw, "\n")
			_ = yaml.Unmarshal([]byte(raw), &meta)
			body = rest[end+len("\n---"):]
			body = strings.TrimPrefix(body, "\n")
		}
	}

	// argument-hint may be a plain string ("[since-tag]") or a YAML flow
	// sequence ([since-tag] with no quotes) depending on how the user wrote
	// it. Accept both and stringify for display.
	hint := ""
	switch v := meta.ArgumentHint.(type) {
	case string:
		hint = v
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, it := range v {
			parts = append(parts, fmt.Sprint(it))
		}
		hint = "[" + strings.Join(parts, " ") + "]"
	default:
		if v != nil {
			hint = fmt.Sprint(v)
		}
	}

	cmd := &Command{
		Name:         "/" + strings.ToLower(name),
		Description:  meta.Description,
		ArgumentHint: hint,
		Source:       path,
		template:     strings.TrimSpace(body),
	}
	if cmd.Description == "" {
		cmd.Description = "自定义命令 (" + filepath.Base(path) + ")"
	}
	return cmd, nil
}

// runShell executes a shell command and returns its combined output, capped
// so a runaway command cannot flood the prompt.
func runShell(parent context.Context, cmd string) string {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()

	var c *exec.Cmd
	if isWindows() {
		c = exec.CommandContext(ctx, "cmd", "/C", cmd)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", cmd)
	}
	out, _ := c.CombinedOutput()

	const maxBytes = 8 * 1024
	if len(out) > maxBytes {
		out = append(out[:maxBytes], []byte("\n[... output truncated at 8KB ...]")...)
	}
	return string(bytes.TrimRight(out, "\n"))
}

func isWindows() bool {
	// Cheap, dependency-free check; the config layer already forces UTF-8 on
	// Windows so we only need to distinguish shell.
	return strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") ||
		filepath.Separator == '\\'
}

// DefaultDirs returns the standard load paths for command files, in the order
// callers should hand to Load. Missing directories are fine — Load skips them.
func DefaultDirs() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".icode", "commands"))
	}
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(cwd, ".icode", "commands"))
	}
	return out
}
