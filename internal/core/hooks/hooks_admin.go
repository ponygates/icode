package hooks

import (
	"fmt"
	"strconv"
	"strings"
)

// AdminEvents lists every lifecycle event in a stable order for display.
// Keep in sync with the Event constants in hooks.go.
var AdminEvents = []Event{
	PreToolUse, PostToolUse, UserPromptSubmit, Stop, Notification,
	PreCompact, PostCompact, SessionStart, SessionEnd, PermissionRequest,
	SubagentStart, SubagentStop, ToolError, AgentStart, AgentStop,
}

// adminEventHelp carries the one-line description shown by /hooks events.
var adminEventHelp = map[Event]string{
	PreToolUse:        "工具调用前 — 可放行/拒绝/转人工审批（permissionDecision）",
	PostToolUse:       "工具调用后 — 可补充提示或抑制输出",
	UserPromptSubmit:  "用户提交输入时 — 可追加上下文或拦截本轮",
	Stop:              "本轮结束时 — 可拒绝结束并强制继续（最多 3 次）",
	Notification:      "本轮完成或失败的通知信号",
	PreCompact:        "会话压缩前 — 可快照现场",
	PostCompact:       "会话压缩后 — 前后消息数在 ToolOutput",
	SessionStart:      "新会话首条消息时",
	SessionEnd:        "会话删除或应用退出时 — 可落盘会话状态",
	PermissionRequest: "工具调用需要人工确认时",
	SubagentStart:     "子 agent 运行开始",
	SubagentStop:      "子 agent 运行结束",
	ToolError:         "工具执行失败 — 错误文本在 ToolOutput",
	AgentStart:        "主 agent 轮次开始",
	AgentStop:         "主 agent 轮次结束",
}

// NormalizeEvent maps any-case spelling to the canonical PascalCase event
// name ("pretooluse" → PreToolUse). Unknown names return "".
func NormalizeEvent(s string) Event {
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	for _, ev := range AdminEvents {
		if strings.ToLower(string(ev)) == lower {
			return ev
		}
	}
	return ""
}

// EventsHelp renders the /hooks events cheat-sheet.
func EventsHelp() string {
	var b strings.Builder
	b.WriteString("可用钩子事件（stdin 收到 JSON 载荷，exit 2 阻断 / stdout JSON 决策）：\n")
	for _, ev := range AdminEvents {
		help := adminEventHelp[ev]
		if help == "" {
			help = "—"
		}
		fmt.Fprintf(&b, "  %-17s %s\n", ev, help)
	}
	b.WriteString("\n示例: /hooks add PreToolUse -m bash -t 10 -- go vet ./...")
	return b.String()
}

// FormatRules renders the /hooks listing. Rules are grouped by event in
// AdminEvents order; numbering restarts at 1 inside each event group so
// "/hooks rm <event> <n>" addresses match what the user sees.
func FormatRules(rules map[string][]Rule) string {
	total := 0
	for _, list := range rules {
		total += len(list)
	}
	if total == 0 {
		return "当前没有配置任何钩子。\n\n" +
			"添加示例: /hooks add PreToolUse -m bash -- go vet ./...\n" +
			"查看事件: /hooks events\n\n" +
			"钩子保存在 ~/.icode/config.yaml 的 hooks: 字段，随会话即时生效。"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "⚙ 生命周期钩子（config.yaml → hooks:）共 %d 条\n\n", total)
	for _, ev := range AdminEvents {
		list := rules[string(ev)]
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s\n", ev)
		for i, r := range list {
			matcher := r.Matcher
			if matcher == "" {
				matcher = "（匹配全部工具）"
			}
			timeout := r.Timeout
			if timeout <= 0 {
				timeout = 30
			}
			fmt.Fprintf(&b, "    %d. %s  timeout=%ds\n       %s\n", i+1, matcher, timeout, r.Command)
		}
	}
	b.WriteString("\n用法: /hooks add <事件> [-m 工具匹配] [-t 超时秒] 命令…  ·  /hooks rm <事件> <序号>  ·  /hooks events  ·  /hooks reload")
	return b.String()
}

// ParseAddArgs parses "/hooks add" arguments:
//
//	ParseAddArgs([]string{"PreToolUse", "-m", "bash", "-t", "10", "go", "vet ./..."})
//
// The first token is the event name (any case). -m takes the next token as
// the tool matcher regex; -t takes the next token as the timeout in seconds.
// The first token that is neither a flag nor a flag value starts the command,
// which is everything joined by single spaces (so flags inside the command
// are fine once the command has started).
func ParseAddArgs(args []string) (Event, Rule, error) {
	var rule Rule
	if len(args) == 0 {
		return "", rule, fmt.Errorf("缺少事件名。用法: /hooks add <事件> [-m 工具匹配] [-t 超时秒] 命令…（/hooks events 查看事件）")
	}
	ev := NormalizeEvent(args[0])
	if ev == "" {
		return "", rule, fmt.Errorf("未知事件 %q（/hooks events 查看全部事件）", args[0])
	}
	i := 1
	for i < len(args) {
		switch args[i] {
		case "--":
			// Explicit end-of-flags marker: everything after is the command.
			i++
			rule.Command = strings.Join(args[i:], " ")
			i = len(args)
		case "-m":
			if i+1 >= len(args) {
				return "", rule, fmt.Errorf("-m 后缺少匹配表达式")
			}
			rule.Matcher = args[i+1]
			i += 2
		case "-t":
			if i+1 >= len(args) {
				return "", rule, fmt.Errorf("-t 后缺少超时秒数")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return "", rule, fmt.Errorf("超时秒数必须是正整数，收到 %q", args[i+1])
			}
			rule.Timeout = n
			i += 2
		default:
			// First non-flag token starts the command; swallow the rest.
			rule.Command = strings.Join(args[i:], " ")
			i = len(args)
		}
	}
	if strings.TrimSpace(rule.Command) == "" {
		return "", rule, fmt.Errorf("缺少命令。用法: /hooks add <事件> [-m 工具匹配] [-t 超时秒] 命令…")
	}
	return ev, rule, nil
}
