// Package tool provides the built-in tool system for iCode.

package tool

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ponygates/icode/internal/types"
)

// Registry holds all available tools.

type Registry struct {
	mu sync.RWMutex

	tools map[string]types.Tool
}

// writerFunc adapts a Write function into an io.Writer (used for the live

// bash output path so exec.Cmd copies child output into it directly).

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// NewRegistry creates a tool registry with all built-in tools.

func NewRegistry() *Registry {

	r := &Registry{tools: make(map[string]types.Tool)}

	// Register built-in tools

	r.Register(&BashTool{})

	r.Register(&ReadFileTool{})

	r.Register(&WriteFileTool{})

	r.Register(&EditTool{})

	r.Register(&GrepTool{})

	r.Register(&GlobTool{})

	r.Register(&LSTool{})

	r.Register(&FetchTool{})

	r.Register(&BrowserTool{})

	r.Register(&AskUserTool{})

	r.Register(&AskUserFormTool{})

	r.Register(&GitDiffTool{})

	r.Register(&GitCommitTool{})

	r.Register(&GitStatusTool{})

	r.Register(&GitLogTool{})

	r.Register(&GitBranchTool{})

	r.Register(&SearchReplaceTool{})

	r.Register(NewWebSearchTool())

	// Monitor: stream background-task output into the conversation live

	// (Claude Code parity) so the model can tail logs and react early.

	r.Register(&MonitorTool{})

	// CodeGraph symbol search (Claude Code parity — definition lookup)

	r.Register(NewCodeSearchTool())

	r.Register(&TaskOutputTool{})

	// Multimodal generation (image/video) — backend configured lazily via

	// SetMultimodalOptions. Registered with nil opts so the tools advertise

	// themselves and report "not configured" until wired up.

	r.Register(NewImageGenTool(nil))

	r.Register(NewVideoGenTool(nil))

	// Built-in disk management tools (no AI model required)

	r.Register(&DiskUsageTool{})

	r.Register(&DiskCleanupTool{})

	// Sub-agent delegation (Claude Code task tool parity)

	// The runner is injected later via SetTaskRunner when the Engine wires it up.

	r.Register(NewTaskTool(nil))

	// Session-scoped scratchpad tool

	r.Register(NewTodoWriteTool(nil))

	// On-demand skill loader: returns a SKILL.md body into volatile scratch

	// instead of bloating the immutable system prefix (token-saving keystone).

	r.Register(NewUseSkillTool(nil))

	// Computer-use tools (Claude Code parity): screenshot + desktop mouse /

	// keyboard control. High-risk surface → gated behind permission approval.

	r.Register(&ScreenshotTool{})

	r.Register(&ScreenReadTool{})

	r.Register(&MouseMoveTool{})

	r.Register(&MouseClickTool{})

	r.Register(&MouseScrollTool{})

	r.Register(&TypeTextTool{})

	r.Register(&KeyPressTool{})

	// Cross-session messaging (Claude Code SendMessage parity). The store is

	// injected later via SetMessageStore during app bootstrap.

	r.Register(NewSendMessageTool(nil))

	r.Register(NewInboxTool(nil))

	r.Register(NewListAgentsTool(nil))

	return r

}

// Register adds a tool to the registry. Safe for concurrent use (MCP tool

// refresh runs in a background goroutine while chat requests read the map).

func (r *Registry) Register(t types.Tool) {

	r.mu.Lock()

	defer r.mu.Unlock()

	r.tools[t.Def().Name] = t

}

// SetTaskRunner injects the sub-agent runner into the Task tool.

// Called during Engine initialisation once the runner is available.

func (r *Registry) SetTaskRunner(runner SubAgentRunner) {

	r.mu.RLock()

	defer r.mu.RUnlock()

	if tt, ok := r.tools["task"]; ok {

		if task, ok := tt.(*TaskTool); ok {

			task.runner = runner

		}

	}

}

// SetMessageStore injects the persistence layer into the cross-session

// messaging tools. Called during app bootstrap once SQLite is ready.

func (r *Registry) SetMessageStore(store MessageStore) {

	r.mu.RLock()

	defer r.mu.RUnlock()

	if st, ok := r.tools["send_message"]; ok {

		if t, ok := st.(*SendMessageTool); ok {

			t.store = store

		}

	}

	if it, ok := r.tools["inbox"]; ok {

		if t, ok := it.(*InboxTool); ok {

			t.store = store

		}

	}

	if la, ok := r.tools["list_agents"]; ok {

		if t, ok := la.(*ListAgentsTool); ok {

			t.store = store

		}

	}

}

// SetMultimodalOptions injects the multimodal backend config into the

// image_gen / video_gen tools. Called during Bootstrap once config is loaded.

func (r *Registry) SetMultimodalOptions(opts MultimodalOptions) {

	r.mu.RLock()

	defer r.mu.RUnlock()

	if it, ok := r.tools["image_gen"]; ok {

		if img, ok := it.(*ImageGenTool); ok {

			cp := opts

			img.opts = &cp

		}

	}

	if vt, ok := r.tools["video_gen"]; ok {

		if vid, ok := vt.(*VideoGenTool); ok {

			cp := opts

			vid.opts = &cp

		}

	}

}

// SetSkillsLoader injects the skill resolver into the use_skill tool. Called

// during Bootstrap once the engine's skill registry is loaded. The loader

// fetches a SKILL.md body on demand so it never lives in the cached prefix.

func (r *Registry) SetSkillsLoader(fn SkillLoader) {

	r.mu.RLock()

	defer r.mu.RUnlock()

	if st, ok := r.tools["use_skill"]; ok {

		if sk, ok := st.(*UseSkillTool); ok {

			sk.loader = fn

		}

	}

}

// Get returns a tool by name.

func (r *Registry) Get(name string) (types.Tool, bool) {

	r.mu.RLock()

	defer r.mu.RUnlock()

	t, ok := r.tools[name]

	return t, ok

}

// Unregister removes a tool from the registry by name.

func (r *Registry) Unregister(name string) {

	r.mu.Lock()

	defer r.mu.Unlock()

	delete(r.tools, name)

}

// ListDefs returns tool definitions for all registered tools.

func (r *Registry) ListDefs() []types.ToolDef {

	r.mu.RLock()

	defer r.mu.RUnlock()

	defs := make([]types.ToolDef, 0, len(r.tools))

	for _, t := range r.tools {

		defs = append(defs, t.Def())

	}

	// Cache-friendly ordering (Claude Code assembleToolPool parity): prompt

	// caches only hit when the request prefix is byte-identical, and Go map

	// iteration is randomised — without sorting, every request would present

	// the tool list in a different order and silently invalidate the cache.

	// Built-ins sort first alphabetically; MCP tools trail after so a late

	// MCP connect/disconnect only perturbs the tail of the array.

	sort.Slice(defs, func(i, j int) bool {

		mi := strings.HasPrefix(defs[i].Name, "mcp_")

		mj := strings.HasPrefix(defs[j].Name, "mcp_")

		if mi != mj {

			return mj // built-in before mcp

		}

		return defs[i].Name < defs[j].Name

	})

	return defs

}

// Execute runs a named tool with arguments.

func (r *Registry) Execute(ctx context.Context, name, args string) (*types.ToolResult, error) {

	t, ok := r.Get(name)

	if !ok {

		return nil, fmt.Errorf("unknown tool: %s", name)

	}

	return t.Execute(ctx, args)

}
