package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Template is a ready-made recipe for creating a scheduled automation with
// minimal editing: pick a template, tweak the prompt, done. Builtin templates
// ship with the binary; user-saved ones persist to ~/.icode/automation_templates.json.
type Template struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	NameEN   string `json:"name_en,omitempty"`
	Desc     string `json:"desc"`
	DescEN   string `json:"desc_en,omitempty"`
	Category string `json:"category"` // dev | info | office | life
	Icon     string `json:"icon"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Custom   bool   `json:"custom,omitempty"`
}

var builtinTemplates = []Template{
	{
		ID: "tpl-code-patrol", Name: "每日代码巡检", NameEN: "Daily code patrol",
		Desc: "git status + 最近提交摘要，改动一目了然", DescEN: "git status + recent commit digest",
		Category: "dev", Icon: "🧭", Schedule: "daily:09:30",
		Prompt: "运行 git status 和 git log --oneline -20，汇总当前工作区未提交的改动（按文件分组列出修改/新增/删除）和最近一天的提交主题；如有疑似遗留的调试代码或 TODO，一并提醒。用简短中文报告。",
	},
	{
		ID: "tpl-code-review", Name: "未提交改动审查", NameEN: "Review uncommitted changes",
		Desc: "对工作区 diff 做一遍轻量 code review", DescEN: "Lightweight review of the working-tree diff",
		Category: "dev", Icon: "🔍", Schedule: "every:24h",
		Prompt: "运行 git diff 查看当前未提交改动，从正确性、边界条件、命名可读性三个角度做轻量 code review；每个问题给出文件:行号与修改建议，按严重程度排序。没有改动时回复「工作区干净」。",
	},
	{
		ID: "tpl-dep-update", Name: "依赖更新检查", NameEN: "Dependency update check",
		Desc: "检查 go.mod / package.json 可升级依赖", DescEN: "Check go.mod / package.json for upgrades",
		Category: "dev", Icon: "📦", Schedule: "every:7d",
		Prompt: "读取本项目的 go.mod 或 package.json，用 web_search 查这些直接依赖是否有新的大版本发布；输出一张「依赖 | 当前版本 | 最新版本 | 升级风险」表格，只标注有破坏性变更风险的行。",
	},
	{
		ID: "tpl-release-digest", Name: "竞品 Release 摘要", NameEN: "Competing-project release digest",
		Desc: "跟踪关注项目的 GitHub Release 动态", DescEN: "Track GitHub releases of projects you follow",
		Category: "info", Icon: "🛰️", Schedule: "every:7d",
		Prompt: "用 fetch 读取这些 GitHub 仓库的 releases 页面：anthropics/claude-code、opencode-ai/opencode。汇总最近一周的版本变化要点（新功能/修复/破坏性变更），中文输出，每项标注来源仓库与版本号。",
	},
	{
		ID: "tpl-morning-news", Name: "行业动态早报", NameEN: "Industry morning brief",
		Desc: "每天早 9 点抓取行业要闻做digest", DescEN: "Daily industry news digest at 9am",
		Category: "info", Icon: "📰", Schedule: "daily:09:00",
		Prompt: "用 web_search 搜索「AI 编程工具 动态」相关的最近 24 小时新闻，挑选 5 条最重要的，每条一句话摘要 + 原文链接，末尾用 2-3 句话总结今天的整体趋势。",
	},
	{
		ID: "tpl-weekly-report", Name: "每周工作周报", NameEN: "Weekly work report",
		Desc: "从 git 历史自动生成周报草稿", DescEN: "Draft a weekly report from git history",
		Category: "office", Icon: "📊", Schedule: "every:7d",
		Prompt: "运行 git log --since=\"7 days ago\" --oneline 并阅读关键提交，按「本周完成 / 进行中 / 下周计划 / 风险与求助」四段生成周报草稿。语气客观简洁，可直接粘贴给上级。",
	},
	{
		ID: "tpl-meeting-minutes", Name: "会议纪要整理", NameEN: "Meeting minutes",
		Desc: "粘贴速记，出结构化纪要（手动运行）", DescEN: "Turn raw notes into structured minutes (run manually)",
		Category: "office", Icon: "📝", Schedule: "every:100d",
		Prompt: "把我接下来粘贴的会议速记整理成结构化纪要：会议主题、时间、参会人、决议事项（标注负责人与截止日期）、遗留问题。速记内容如下：\n\n",
	},
	{
		ID: "tpl-kb-tidy", Name: "知识库日整理", NameEN: "Knowledge-base daily tidy",
		Desc: "归并当日新增笔记、生成目录索引", DescEN: "Merge the day's notes and refresh the index",
		Category: "office", Icon: "🗂️", Schedule: "daily:21:00",
		Prompt: "查看知识库中今天新增/修改的内容，将同主题的碎片条目归并，更新各主题的目录索引页；输出一份「今日新增主题 + 归并动作」清单。",
	},
	{
		ID: "tpl-moments-draft", Name: "朋友圈文案草稿", NameEN: "Social-moment draft",
		Desc: "每天一条专业人设文案草稿", DescEN: "One professional social-post draft a day",
		Category: "life", Icon: "💬", Schedule: "daily:08:00",
		Prompt: "围绕「专业保险顾问的日常洞察」写一条朋友圈文案草稿：一个真实小场景 + 一个专业观点 + 一句柔和的行动号召，控制在 120 字内，不用感叹号堆砌，结尾给 2 个表情建议。只输出文案本身。",
	},
}

// BuiltinTemplates returns the shipped, read-only templates sorted by category.
func BuiltinTemplates() []Template {
	out := make([]Template, len(builtinTemplates))
	copy(out, builtinTemplates)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}

// templatesPath returns ~/.icode/automation_templates.json. Overridable in tests.
var templatesPathFn = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "automation_templates.json"
	}
	return filepath.Join(home, ".icode", "automation_templates.json")
}

func templatesPath() string { return templatesPathFn() }

var tplMu sync.Mutex

// LoadCustomTemplates reads user-saved templates; missing file is not an error.
func LoadCustomTemplates() ([]Template, error) {
	tplMu.Lock()
	defer tplMu.Unlock()
	data, err := os.ReadFile(templatesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Template
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", templatesPath(), err)
	}
	for i := range out {
		out[i].Custom = true
	}
	return out, nil
}

// SaveCustomTemplate persists a user template, assigning it a fresh ID.
func SaveCustomTemplate(t Template) (Template, error) {
	name := strings.TrimSpace(t.Name)
	prompt := strings.TrimSpace(t.Prompt)
	if name == "" || prompt == "" {
		return t, fmt.Errorf("template name and prompt are required")
	}
	schedule := strings.TrimSpace(t.Schedule)
	if schedule == "" {
		schedule = "daily:09:00"
	}
	if _, err := nextRunAfter(schedule, time.Now(), ""); err != nil {
		return t, fmt.Errorf("invalid schedule: %w", err)
	}
	t = Template{
		ID:       fmt.Sprintf("tpl-custom-%d", time.Now().UnixNano()),
		Name:     name,
		NameEN:   name,
		Desc:     strings.TrimSpace(t.Desc),
		DescEN:   strings.TrimSpace(t.Desc),
		Category: "custom", Icon: "⭐", Schedule: schedule, Prompt: prompt, Custom: true,
	}

	tplMu.Lock()
	defer tplMu.Unlock()
	path := templatesPath()
	list := []Template{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &list)
	}
	list = append(list, t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return t, err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return t, err
	}
	return t, os.WriteFile(path, data, 0o600)
}

// DeleteCustomTemplate removes a user-saved template by ID.
func DeleteCustomTemplate(id string) error {
	tplMu.Lock()
	defer tplMu.Unlock()
	path := templatesPath()
	list := []Template{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
	}
	kept := list[:0]
	found := false
	for _, t := range list {
		if t.ID == id {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return fmt.Errorf("template %q not found", id)
	}
	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
