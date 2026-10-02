package permission

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// HooksConfig represents the hooks.yaml configuration.
type HooksConfig struct {
	Tools []ToolRule `yaml:"tools"`
}

type ToolRule struct {
	Name  string   `yaml:"name"`
	Allow []string `yaml:"allow"` // patterns that are always allowed
	Deny  []string `yaml:"deny"`  // patterns that are always denied
	Ask   []string `yaml:"ask"`   // patterns that require user approval
}

func (g *Gate) checkHooks(action Action) *CheckResult {
	if g.hooks == nil {
		return nil
	}

	for _, rule := range g.hooks.Tools {
		if rule.Name != action.Tool {
			continue
		}

		// Check deny patterns first (highest priority)
		for _, pattern := range rule.Deny {
			if matchPattern(action, pattern) {
				return &CheckResult{
					Decision: DecisionDeny,
					Reason:   fmt.Sprintf("Denied by hooks rule: %s", pattern),
				}
			}
		}

		// Check allow patterns
		for _, pattern := range rule.Allow {
			if matchPattern(action, pattern) {
				return &CheckResult{
					Decision: DecisionAllow,
					Reason:   fmt.Sprintf("Allowed by hooks rule: %s", pattern),
				}
			}
		}
	}

	return nil
}

func matchPattern(action Action, pattern string) bool {
	switch action.Tool {
	case "bash":
		return strings.Contains(action.Command, pattern)
	case "read_file", "write_file":
		return strings.Contains(action.Path, pattern)
	default:
		return strings.Contains(action.Arguments, pattern)
	}
}

// argField extracts a string field from the tool-call arguments JSON. Empty
// when absent/unparseable — the permission summary degrades gracefully.
func argField(arguments, key string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(arguments), &m); err != nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// buildPrompt renders a user-facing summary of what the tool call will do.
// It is shown verbatim in the TUI / desktop approval dialogs, so
// it carries a compact argument preview (paths, commands, content excerpts)
// to let the user judge the request before approving — not just the tool name.
func (g *Gate) buildPrompt(action Action) string {
	switch action.Tool {
	case "bash":
		return fmt.Sprintf("执行命令: %s", action.Command)
	case "write_file":
		// Show the write target plus a short content excerpt so the user can
		// verify what is about to be written (Claude Code parity). The excerpt
		// preserves line structure and indentation — the old strings.Fields
		// join collapsed ALL whitespace, which mangled code layout and made
		// approving a write pure guesswork.
		excerpt := excerptLines(argField(action.Arguments, "content"), 6, 120)
		if excerpt != "" {
			return fmt.Sprintf("写入文件: %s (%d 字节)\n内容:\n%s", action.Path, len(action.Arguments), excerpt)
		}
		return fmt.Sprintf("写入文件: %s (%d 字节)", action.Path, len(action.Arguments))
	case "edit":
		// Unified-diff style preview: removed lines prefixed "- ", added
		// lines "+ ". The TUI renders "-" red and "+" green, so the approval
		// box reads like a git diff instead of two opaque quoted strings —
		// the user approves what they can actually see.
		oldS := argField(action.Arguments, "old_string")
		newS := argField(action.Arguments, "new_string")
		if oldS != "" || newS != "" {
			var b strings.Builder
			fmt.Fprintf(&b, "编辑文件: %s", action.Path)
			for _, ln := range strings.Split(excerptLines(oldS, 6, 72), "\n") {
				b.WriteString("\n- " + ln)
			}
			for _, ln := range strings.Split(excerptLines(newS, 6, 72), "\n") {
				b.WriteString("\n+ " + ln)
			}
			return b.String()
		}
		return fmt.Sprintf("编辑文件: %s", action.Path)
	case "read_file":
		return fmt.Sprintf("读取文件: %s", action.Path)
	case "grep":
		return fmt.Sprintf("搜索: \"%s\" 在 %s", action.Pattern, action.Path)
	case "glob":
		return fmt.Sprintf("查找文件: %s", action.Pattern)
	case "git_diff":
		return fmt.Sprintf("查看 git 差异 (staged=%t)", action.Path != "")
	case "git_status":
		return "查看 git 状态"
	case "git_commit":
		return fmt.Sprintf("提交 git: %s", truncate(action.Command, 50))
	case "fetch":
		return fmt.Sprintf("访问网络: %s", truncate(action.URL, 120))
	default:
		return fmt.Sprintf("%s: %s", action.Tool, truncate(action.Arguments, 100))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Truncate by RUNES, not bytes: CJK characters are 3 bytes each, so the
	// old s[:n] byte cut landed mid-rune and produced mojibake (乱码) in the
	// approval preview. The limit is now n characters, which matches the
	// visible width the dialog intended anyway.
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// excerptLines renders up to maxLines lines of s, each truncated to maxRunes
// characters (with an omission note when more lines follow). Unlike a
// strings.Fields join it preserves the original line structure and
// indentation, so code/config previews in the approval dialog stay readable —
// the user approves what they can actually see.
func excerptLines(s string, maxLines, maxRunes int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	omitted := len(lines) > maxLines
	if omitted {
		lines = lines[:maxLines]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, truncate(l, maxRunes))
	}
	joined := strings.Join(out, "\n")
	if omitted {
		joined += fmt.Sprintf("\n…（篇幅较长，已省略后续行）")
	}
	return joined
}

// ============================================================================
// Hooks file loading
// ============================================================================

func loadHooks() *HooksConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	paths := []string{
		filepath.Join(home, ".icode", "hooks.yaml"),
		".icode/hooks.yaml",
		"hooks.yaml",
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		var cfg HooksConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			continue
		}
		return &cfg
	}

	return &HooksConfig{
		Tools: []ToolRule{
			{
				Name:  "read_file",
				Allow: []string{"*"},
			},
			{
				Name:  "grep",
				Allow: []string{"*"},
			},
			{
				Name:  "glob",
				Allow: []string{"*"},
			},
			{
				Name:  "ls",
				Allow: []string{"*"},
			},
		},
	}
}

// ============================================================================
// Hooks file generator — creates a sample hooks.yaml
// ============================================================================

func GenerateHooks(path string) error {
	sample := `# iCode Hooks Configuration
# Control which tool calls are allowed, denied, or require approval.
#
# Patterns support glob-style matching (*, **, ?).

tools:
  # File reading — always allowed
  - name: read_file
    allow:
      - "*"

  # File searching — always allowed
  - name: grep
    allow:
      - "*"
  - name: glob
    allow:
      - "*"
  - name: ls
    allow:
      - "*"

  # File writing — require approval
  - name: write_file
    ask:
      - "*"

  # Shell commands — require approval, block dangerous ones
  - name: bash
    deny:
      - "rm -rf /"
      - "sudo"
      - "chmod 777"
      - "> /dev/sda"
    ask:
      - "*"
`

	return os.WriteFile(path, []byte(sample), 0644)
}
