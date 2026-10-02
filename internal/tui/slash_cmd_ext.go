package tui

import (
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/core/agent"
	"github.com/ponygates/icode/internal/core/skills"
)

// handleSlashExt dispatches the extension-surface commands: sub-agents,
// skills, plugins, mesh, teams, MCP, voice and PR comments. It reports
// whether the command was claimed.
func (t *TUI) handleSlashExt(cmd string, args []string) bool {
	switch cmd {
	case "/agents":
		t.agentsCommand()
	case "/skills":
		t.slashSkills()
	case "/skill-eval":
		t.skillEvalCommand(args)
	case "/plugin":
		t.pluginCommand(args)
	case "/mesh":
		t.meshCommand(args)
	case "/teams":
		t.slashTeams()
	case "/mcp":
		t.mcpCommand(args)
	case "/voice":
		t.toggleVoiceRecording()
	case "/pr_comments":
		t.prCommentsCommand(args)
	default:
		return false
	}
	return true
}

func (t *TUI) slashSkills() {
	reg := skills.Load(skills.DefaultDirs()...)
	var a strings.Builder
	a.WriteString("可用技能 (SKILL.md):\n")
	if list := reg.List(); len(list) == 0 {
		a.WriteString("  （无。在 ~/.icode/skills/ 或 .icode/skills/ 下放置 SKILL.md 即可启用）\n")
	} else {
		for _, s := range list {
			trig := ""
			if len(s.Triggers) > 0 {
				trig = " 触发: " + strings.Join(s.Triggers, ", ")
			}
			a.WriteString(fmt.Sprintf("  %s — %s%s\n", s.Name, s.Description, trig))
		}
	}
	t.add(RoleSystem, a.String())
}

func (t *TUI) slashTeams() {
	var a strings.Builder
	a.WriteString("多智能体团队:\n")
	list := agent.LoadTeams(agent.TeamDefaultDirs()...)
	if len(list) == 0 {
		list = agent.DefaultTeamDefs()
	}
	for _, td := range list {
		members := make([]string, 0, len(td.Members))
		for _, m := range td.Members {
			members = append(members, m.Name)
		}
		a.WriteString(fmt.Sprintf("  %s — %s [成员: %s]\n", td.Name, td.Description, strings.Join(members, ", ")))
	}
	t.add(RoleSystem, a.String())
}

// handleSlashFallback is the default branch: user-defined slash commands from
// .icode/commands/*.md first, then whatever the host engine understands.
func (t *TUI) handleSlashFallback(cmd string, args []string) {
	// User-defined slash command? Look it up in .icode/commands/*.md
	// (user + project scope) and expand it into a normal chat message.
	if custom := t.tryCustomSlash(cmd, strings.Join(args, " ")); custom {
		return
	}
	if t.callback != nil {
		t.callback.OnSlashCommand(cmd, args)
	}
}
