// Package tool — use_skill: on-demand skill loader.
//
// This is the counterpart to the compact skill index embedded in the system
// prefix (see skills.FormatIndex). The model discovers skills from that tiny
// index; when it actually decides to follow a skill's workflow, it calls
// use_skill to pull the full SKILL.md body. The body is returned as a tool
// result, which lives in the volatile scratch zone — never the immutable
// prefix — so the provider's KV cache stays warm and token usage stays low.
package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// SkillLoader resolves a skill name to its full body + description on demand.
// The engine wires this to its skill registry so the use_skill tool can fetch
// a SKILL.md body without that body ever living in the cached system prefix.
type SkillLoader func(name string) (body, description string, ok bool)

// UseSkillTool loads a skill's complete instructions on demand.
type UseSkillTool struct {
	loader SkillLoader
}

// NewUseSkillTool creates a use_skill tool. The loader is injected later via
// Registry.SetSkillsLoader once the engine's skill registry is ready (mirrors
// the SetTaskRunner / SetMultimodalOptions wiring pattern).
func NewUseSkillTool(loader SkillLoader) *UseSkillTool {
	return &UseSkillTool{loader: loader}
}

func (t *UseSkillTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "use_skill",
		Description: "Load a skill's full instructions on demand. " +
			"Call this with a skill name from the 'Available Skills' index when you decide to follow that skill's workflow. " +
			"Returns the complete SKILL.md body so you can execute its steps. Only use names present in the Available Skills index.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The skill name as listed in the Available Skills index (e.g. \"pdf\").",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t *UseSkillTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	name, err := parseArg(args, "name")
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return &types.ToolResult{Success: false, Error: "use_skill requires a non-empty 'name'"}, nil
	}
	if t.loader == nil {
		return &types.ToolResult{Success: false, Error: "skill loader not configured"}, nil
	}
	body, desc, ok := t.loader(name)
	if !ok || strings.TrimSpace(body) == "" {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf("skill %q not found. Use the /skills command or the Available Skills index to see valid names.", name),
		}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Skill: %s\n", name)
	if desc != "" {
		fmt.Fprintf(&b, "_%s_\n\n", desc)
	}
	b.WriteString(body)
	return &types.ToolResult{Success: true, Content: b.String()}, nil
}
