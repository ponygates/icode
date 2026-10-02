package hooks

import (
	"strings"
	"testing"
)

func TestNormalizeEvent(t *testing.T) {
	cases := []struct {
		in   string
		want Event
	}{
		{"PreToolUse", PreToolUse},
		{"pretooluse", PreToolUse},
		{"PreToolUse", PreToolUse},
		{"STOP", Stop},
		{"stop", Stop},
		{"SessionEnd", SessionEnd},
		{"sessionend", SessionEnd},
		{"", ""},
		{"NotAnEvent", ""},
		{"pretuse", ""},
	}
	for _, c := range cases {
		if got := NormalizeEvent(c.in); got != c.want {
			t.Errorf("NormalizeEvent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseAddArgsFull(t *testing.T) {
	ev, rule, err := ParseAddArgs([]string{"PreToolUse", "-m", "bash", "-t", "10", "go", "vet", "./..."})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ev != PreToolUse {
		t.Errorf("event = %q, want PreToolUse", ev)
	}
	if rule.Matcher != "bash" {
		t.Errorf("matcher = %q, want bash", rule.Matcher)
	}
	if rule.Timeout != 10 {
		t.Errorf("timeout = %d, want 10", rule.Timeout)
	}
	if rule.Command != "go vet ./..." {
		t.Errorf("command = %q", rule.Command)
	}
}

func TestParseAddArgsDefaultsAndErrors(t *testing.T) {
	// Bare command: no matcher, no timeout → defaults.
	ev, rule, err := ParseAddArgs([]string{"stop", "echo", "done"})
	if err != nil || ev != Stop || rule.Matcher != "" || rule.Timeout != 0 || rule.Command != "echo done" {
		t.Fatalf("bare: ev=%q rule=%+v err=%v", ev, rule, err)
	}
	// Explicit end-of-flags marker: "--" separates flags from the command,
	// so leading-dash words after it stay part of the command.
	_, rule, err = ParseAddArgs([]string{"Stop", "-t", "5", "--", "git", "log", "-m", "x"})
	if err != nil || rule.Command != "git log -m x" || rule.Timeout != 5 {
		t.Fatalf("end-of-flags: %+v err=%v", rule, err)
	}
	// Flags after the command starts must be swallowed into the command.
	_, rule, err = ParseAddArgs([]string{"Stop", "pytest", "-m", "verbose"})
	if err != nil || rule.Command != "pytest -m verbose" {
		t.Fatalf("flags-in-command: %+v err=%v", rule, err)
	}
	// Missing event.
	if _, _, err = ParseAddArgs(nil); err == nil {
		t.Error("nil args should error")
	}
	// Unknown event.
	if _, _, err = ParseAddArgs([]string{"Nope", "x"}); err == nil || !strings.Contains(err.Error(), "未知事件") {
		t.Errorf("unknown event err = %v", err)
	}
	// Missing command.
	if _, _, err = ParseAddArgs([]string{"Stop"}); err == nil {
		t.Error("no command should error")
	}
	// -m without value.
	if _, _, err = ParseAddArgs([]string{"Stop", "-m"}); err == nil {
		t.Error("-m without value should error")
	}
	// Bad timeout.
	if _, _, err = ParseAddArgs([]string{"Stop", "-t", "0", "x"}); err == nil {
		t.Error("timeout 0 should error")
	}
	if _, _, err = ParseAddArgs([]string{"Stop", "-t", "abc", "x"}); err == nil {
		t.Error("timeout abc should error")
	}
}

func TestFormatRules(t *testing.T) {
	// Empty → onboarding hint.
	out := FormatRules(map[string][]Rule{})
	if !strings.Contains(out, "没有配置任何钩子") {
		t.Errorf("empty listing missing hint: %q", out)
	}
	// Populated: per-event numbering + defaults shown for timeout 0.
	out = FormatRules(map[string][]Rule{
		string(PreToolUse): {
			{Matcher: "bash", Command: "go vet ./...", Timeout: 10},
			{Command: "echo all-tools"},
		},
		string(Stop): {
			{Command: "echo stop", Timeout: 0},
		},
	})
	for _, want := range []string{"共 3 条", "PreToolUse", "bash", "go vet ./...", "（匹配全部工具）", "timeout=30s", "Stop", "echo stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing missing %q in:\n%s", want, out)
		}
	}
	// Per-event numbering restarts at 1: two entries under PreToolUse → "2.".
	if !strings.Contains(out, "    2. （匹配全部工具）") {
		t.Errorf("per-event numbering not restarted: %s", out)
	}
}

func TestEventsHelp(t *testing.T) {
	out := EventsHelp()
	for _, ev := range AdminEvents {
		if !strings.Contains(out, string(ev)) {
			t.Errorf("EventsHelp missing %q", ev)
		}
	}
	if !strings.Contains(out, "permissionDecision") {
		t.Error("EventsHelp should mention the JSON decision protocol")
	}
}
