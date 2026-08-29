// Package hooks implements lifecycle hooks — external commands that fire at
// key points of the agent loop (Claude Code parity).
//
// Supported events:
//
//	PreToolUse      — before a tool executes. Exit code 2 blocks the tool call
//	                  and feeds stderr back to the model as the error message.
//	PostToolUse     — after a tool executes. Stderr (exit code 2) is appended to
//	                  the tool result so the model sees the feedback.
//	UserPromptSubmit — before a user message is sent to the model. Exit code 2
//	                  blocks the message; stdout JSON {"prompt": "..."} rewrites
//	                  the message the model sees.
//	Stop            — when the agent finishes responding.
//
// Hooks receive a JSON payload on stdin describing the event:
//
//	{"hook_event_name":"PreToolUse","tool_name":"bash","tool_input":{...},
//	 "session_id":"...","cwd":"..."}
//
// Configuration lives in config.yaml:
//
//	hooks:
//	  PreToolUse:
//	    - matcher: "bash"            # regex matched against tool name; empty = all
//	      command: "python check.py" # run via system shell
//	      timeout: 30                # seconds, default 30
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/executil"
)

// Event is a lifecycle hook event name.
type Event string

const (
	PreToolUse       Event = "PreToolUse"
	PostToolUse      Event = "PostToolUse"
	UserPromptSubmit Event = "UserPromptSubmit"
	Stop             Event = "Stop"
	// Notification fires when a turn completes or fails — a lightweight
	// completion/error signal for external scripts (Claude Code parity).
	// Input.ToolOutput carries "completed" / "error: …".
	Notification Event = "Notification"
	// PreCompact fires right before a conversation is compacted, so external
	// scripts can snapshot state or log the handoff.
	PreCompact Event = "PreCompact"
)

// Rule is a single hook definition: a tool-name matcher plus a shell command.
type Rule struct {
	Matcher string `yaml:"matcher" json:"matcher"` // regex on tool name; empty matches all
	Command string `yaml:"command" json:"command"` // executed via system shell
	Timeout int    `yaml:"timeout" json:"timeout"` // seconds; 0 = 30s default
}

// Input is the JSON payload delivered to the hook process on stdin.
type Input struct {
	Event      string          `json:"hook_event_name"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolInput  json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput string          `json:"tool_output,omitempty"`
	Prompt     string          `json:"prompt,omitempty"` // UserPromptSubmit: the user's message
	SessionID  string          `json:"session_id,omitempty"`
	Cwd        string          `json:"cwd"`
}

// Result summarises the outcome of firing an event's hooks.
type Result struct {
	Block   bool   // true when a PreToolUse hook exited with code 2
	Message string // stderr of the deciding hook (feedback for the model)
	// Prompt is the rewritten user message for UserPromptSubmit hooks. Empty
	// means "keep the original" (or, with Block=true, "drop the message").
	Prompt string
}

// Runner executes configured hooks. Safe for concurrent use (immutable config).
type Runner struct {
	rules map[Event][]Rule
	cwd   string
}

// NewRunner builds a Runner from a map of event name → rules.
// Unknown event names are kept as-is so future events don't break configs.
func NewRunner(rules map[string][]Rule, cwd string) *Runner {
	m := make(map[Event][]Rule, len(rules))
	for k, v := range rules {
		m[Event(k)] = v
	}
	return &Runner{rules: m, cwd: cwd}
}

// HasHooks reports whether any rule is registered for the given event.
func (r *Runner) HasHooks(ev Event) bool {
	return r != nil && len(r.rules[ev]) > 0
}

// Fire runs all hooks registered for the event whose matcher matches
// in.ToolName. The first hook that exits with code 2 short-circuits:
// for PreToolUse this blocks the tool call.
func (r *Runner) Fire(ctx context.Context, ev Event, in Input) *Result {
	if r == nil {
		return &Result{}
	}
	rules := r.rules[ev]
	if len(rules) == 0 {
		return &Result{}
	}
	in.Event = string(ev)
	if in.Cwd == "" {
		in.Cwd = r.cwd
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return &Result{}
	}

	agg := &Result{}
	for _, rule := range rules {
		if !matches(rule.Matcher, in.ToolName) {
			continue
		}
		block, msg, stdout := runOne(ctx, rule, payload, r.cwd)
		if block {
			return &Result{Block: true, Message: msg}
		}
		// UserPromptSubmit hooks can rewrite the prompt via stdout JSON.
		if ev == UserPromptSubmit {
			if p := parsePromptRewrite(stdout); p != "" {
				agg.Prompt = p
			}
		}
		if msg != "" {
			if agg.Message != "" {
				agg.Message += "\n"
			}
			agg.Message += msg
		}
	}
	return agg
}

// parsePromptRewrite decodes the optional stdout JSON {"prompt": "..."}
// contract of UserPromptSubmit hooks. Anything else (or unparsable) yields "".
func parsePromptRewrite(stdout string) string {
	s := strings.TrimSpace(stdout)
	if s == "" {
		return ""
	}
	var v struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return ""
	}
	return v.Prompt
}

func matches(pattern, toolName string) bool {
	if strings.TrimSpace(pattern) == "" || pattern == "*" {
		return true
	}
	// Try exact match first (cheap and the common case), then regex.
	if pattern == toolName {
		return true
	}
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		return false
	}
	return re.MatchString(toolName)
}

// runOne executes a single hook command. Returns (block, message, stdout).
// Exit code 2 → block=true with stderr as message. Other non-zero exit
// codes are non-blocking (stderr surfaced as informational message).
// stdout is captured for UserPromptSubmit's JSON rewrite contract.
func runOne(ctx context.Context, rule Rule, payload []byte, cwd string) (bool, string, string) {
	timeout := time.Duration(rule.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = executil.CommandContext(cctx, "cmd", "/C", rule.Command)
	} else {
		cmd = executil.CommandContext(cctx, "sh", "-c", rule.Command)
	}
	cmd.Dir = cwd
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	err := cmd.Run()
	msg := strings.TrimSpace(stderr.String())
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 2 {
			if msg == "" {
				msg = "blocked by hook: " + rule.Command
			}
			return true, msg, out
		}
		// Non-2 failures (including timeout) never block the agent.
		return false, msg, out
	}
	return false, msg, out
}
