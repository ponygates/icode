// Package agent defines the sub-agent system: independent conversational
// agents that run in their own Optimizer context, separate from the main
// session. This is the most powerful token-saving mechanism in the project.
//
// Key insight: when the main agent needs to "research something" or "search a
// file", instead of doing it in-line (which pollutes the main context with
// thousands of tokens of tool output), it dispatches a sub-agent that runs
// with its own Optimizer, its own tool set, and a restricted token budget.
// The sub-agent's final answer is returned as a single tool_result (~hundreds
// of tokens) rather than the raw output (~thousands of tokens).
//
// Sub-agent definitions live in `.icode/agents/*.md` files, inheriting the
// same pattern as custom slash commands. Three agents ship with iCode:
//
//   - explore: read-only agent for codebase searching and file inspection
//   - plan: high-level architecture/design reasoning (more tokens, deeper)
//   - general: catch-all for any subtask the main agent wants to offload
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentDef describes a sub-agent that the main LLM can delegate work to.
type AgentDef struct {
	Name         string   // "explore", "plan", "general"
	Description  string   // Shown in /agents listing so the model knows when to delegate
	Model        string   // Model override (empty = inherit main session model)
	SystemPrompt string   // Independent system prompt for this agent
	Tools        []string // Permitted tool names. Empty = all tools allowed.
	MaxRounds    int      // Max tool rounds before forced stop. 0 = default (8).
	MaxTokens    int      // Max completion tokens. 0 = default (4096).
}

// Registry indexes sub-agent definitions by name.
type Registry struct {
	byName map[string]*AgentDef
}

// NewRegistry returns an empty sub-agent registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]*AgentDef{}}
}

// Load walks directories (user ~/.icode/agents first, then project .icode/
// agents) and registers every *.md file as a sub-agent definition. Later
// directories override earlier ones.
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
			def, err := loadFile(path)
			if err != nil {
				continue
			}
			r.byName[def.Name] = def
		}
	}
	return r
}

// Get returns a sub-agent by name. Case-insensitive.
func (r *Registry) Get(name string) (*AgentDef, bool) {
	def, ok := r.byName[strings.ToLower(name)]
	return def, ok
}

// List returns all registered sub-agents (for /agents slash).
func (r *Registry) List() []*AgentDef {
	out := make([]*AgentDef, 0, len(r.byName))
	for _, def := range r.byName {
		out = append(out, def)
	}
	return out
}

// loadFile reads a single agent.md file.
//
// Format:
//
//	---
//	description: Read-only codebase searching and file inspection
//	model: deepseek-v3
//	tools: [read_file, grep, glob, ls, git_status, git_diff]
//	max_rounds: 8
//	max_tokens: 4096
//	---
//	You are the explore agent. Your job is to search the codebase...
func loadFile(path string) (*AgentDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSuffix(strings.ToLower(filepath.Base(path)), ".md")
	if name == "" {
		return nil, fmt.Errorf("empty agent name from %s", path)
	}

	type agentMeta struct {
		Description string   `yaml:"description"`
		Model       string   `yaml:"model"`
		Tools       []string `yaml:"tools"`
		MaxRounds   int      `yaml:"max_rounds"`
		MaxTokens   int      `yaml:"max_tokens"`
	}

	body := string(data)
	var meta agentMeta

	if strings.HasPrefix(body, "---") {
		rest := body[3:]
		end := strings.Index(rest, "\n---")
		if end >= 0 {
			raw := strings.TrimPrefix(rest[:end], "\n")
			_ = yaml.Unmarshal([]byte(raw), &meta)
			body = rest[end+len("\n---"):]
			body = strings.TrimPrefix(body, "\n")
		}
	}

	def := &AgentDef{
		Name:         name,
		Description:  meta.Description,
		Model:        meta.Model,
		SystemPrompt: strings.TrimSpace(body),
		Tools:        meta.Tools,
		MaxRounds:    meta.MaxRounds,
		MaxTokens:    meta.MaxTokens,
	}
	if def.MaxRounds <= 0 {
		def.MaxRounds = 8
	}
	if def.MaxTokens <= 0 {
		def.MaxTokens = 4096
	}
	if def.Description == "" {
		def.Description = "Sub-agent: " + name
	}
	return def, nil
}

// DefaultAgentDefs returns the three built-in agent definitions that ship with
// the binary when no .icode/agents/*.md files exist yet. They are embedded
// directly in Go so there is zero setup friction.
func DefaultAgentDefs() []*AgentDef {
	return []*AgentDef{
		{
			Name:        "explore",
			Description: "Read-only codebase searching, file inspection, and symbol lookup. Use when you need to find something without modifying files.",
			SystemPrompt: `You are the explore agent — a read-only codebase search and file inspection specialist.

Your job:
1. Find relevant files, functions, and patterns using grep/glob/ls
2. Read files to understand their content
3. Report back a concise summary of what you found

RULES:
- NEVER write or edit files. If asked, refuse politely.
- NEVER execute arbitrary shell commands. Only safe git status/diff queries.
- Be thorough: search multiple patterns if the first yields few results.
- When reporting back, include file paths and line numbers.`,
			Tools:     []string{"read_file", "grep", "glob", "ls", "git_status", "git_diff"},
			MaxRounds: 8,
			MaxTokens: 4096,
		},
		{
			Name:        "plan",
			Description: "High-level architecture analysis, design reasoning, and implementation strategy.",
			SystemPrompt: `You are the plan agent — an architect focused on design and strategy.

Your job:
1. Understand the problem by reading relevant files
2. Think about architecture, trade-offs, and edge cases
3. Produce a concrete implementation plan with file paths and function names
4. Return the plan as a clear markdown document

RULES:
- Stay high-level where possible. Defer implementation details to the main agent.
- Always cite specific files and functions.
- Consider: error handling, backwards compatibility, test strategy, security.`,
			Tools:     []string{"read_file", "grep", "glob", "ls", "git_status", "git_diff"},
			MaxRounds: 12,
			MaxTokens: 8192,
		},
		{
			Name:        "general",
			Description: "Catch-all sub-agent for any subtask. Use when a task is self-contained and would pollute the main context with intermediate output.",
			SystemPrompt: `You are a general-purpose sub-agent for iCode.

Your job: complete the task assigned to you by the main agent and report back
a concise summary. Use tools as needed, but avoid unnecessary exploration.

RULES:
- Do NOT edit files unless the task explicitly requires it.
- If the task requires file modifications, list the exact changes needed and
  the main agent will apply them — do not apply them yourself.
- Be concise in your final report. State what you found/did and any follow-up
  recommendations.`,
			MaxRounds: 8,
			MaxTokens: 4096,
		},
	}
}

// RegisterDefaults adds the built-in agents to the registry. It only inserts
// agents that are not already registered, so file-based definitions override.
func (r *Registry) RegisterDefaults() {
	for _, def := range DefaultAgentDefs() {
		if _, exists := r.byName[def.Name]; !exists {
			r.byName[def.Name] = def
		}
	}
}

// DefaultDirs returns the standard paths for agent definition files, in the
// order that should be passed to Load (user first, then project).
func AgentDefaultDirs() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".icode", "agents"))
	}
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(cwd, ".icode", "agents"))
	}
	return out
}
