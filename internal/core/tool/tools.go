// Package tool provides the built-in tool system for iCode.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/types"
)

// Registry holds all available tools.
type Registry struct {
	mu    sync.RWMutex
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
	r.Register(&AskUserTool{})
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

// ============================================================================
// BashTool — execute shell commands
// ============================================================================

type BashTool struct{}

func (t *BashTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "bash",
		Description: "Execute a shell command and return its output. The command runs in the project directory by default. Use 'cwd' to change the working directory for the command.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the command. Defaults to project root. Accepts absolute paths like C:\\Users\\... or relative paths.",
				},
				"run_in_background": map[string]any{
					"type":        "boolean",
					"description": "Run the command in the background and return a task id immediately. Use the task_output tool to poll its output/status later. Use for long-running commands (builds, servers, installs).",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	cmdStr, err := parseArg(args, "command")
	if err != nil {
		return nil, err
	}
	workDir, _ := parseArg(args, "cwd")

	// Background mode: start detached, return the task id immediately.
	if parseBoolArgWithDefault(args, "run_in_background", false) {
		id, err := bgTasks.Start(cmdStr, workDir)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "failed to start background task: " + err.Error()}, nil
		}
		return &types.ToolResult{
			Success: true,
			Content: fmt.Sprintf("Started background task %s. Use task_output with task_id=%q to check its output and status.", id, id),
		}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if strings.Contains(os.Getenv("OS"), "Windows") {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}

	// Apply working directory
	if workDir != "" {
		if absDir, err := filepath.Abs(workDir); err == nil {
			if info, err := os.Stat(absDir); err == nil && info.IsDir() {
				cmd.Dir = absDir
			}
		}
	}

	// Live streaming: when the engine attached a progress callback (TUI /
	// desktop), forward stdout/stderr incrementally so the user sees output
	// as the command runs instead of one dump at the end. The full output is
	// still accumulated and returned for the model. cmd.Wait drains the
	// exec-managed pipes into these writers before returning, so the
	// accumulated content is complete when Wait returns.
	progress := ProgressFromContext(ctx)
	if progress != nil {
		type progressWriter struct {
			mu  sync.Mutex
			buf strings.Builder
		}
		newPW := func() *progressWriter {
			w := &progressWriter{}
			return w
		}
		attach := func(w *progressWriter) io.Writer {
			return writerFunc(func(p []byte) (int, error) {
				w.mu.Lock()
				w.buf.Write(p)
				w.mu.Unlock()
				progress(string(p))
				return len(p), nil
			})
		}
		stdoutPW := newPW()
		stderrPW := newPW()
		cmd.Stdout = attach(stdoutPW)
		cmd.Stderr = attach(stderrPW)
		if err := cmd.Start(); err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, nil
		}
		waitErr := cmd.Wait()
		var content strings.Builder
		stdoutPW.mu.Lock()
		content.WriteString(stdoutPW.buf.String())
		stdoutPW.mu.Unlock()
		stderrPW.mu.Lock()
		content.WriteString(stderrPW.buf.String())
		stderrPW.mu.Unlock()
		if waitErr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return &types.ToolResult{Success: false, Content: content.String(), Error: "command timed out after 120 seconds"}, nil
			}
			return &types.ToolResult{Success: false, Content: content.String(), Error: waitErr.Error()}, nil
		}
		return &types.ToolResult{Success: true, Content: content.String()}, nil
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &types.ToolResult{
				Success: false,
				Content: string(output),
				Error:   "command timed out after 120 seconds",
			}, nil
		}
		return &types.ToolResult{
			Success: false,
			Content: string(output),
			Error:   err.Error(),
		}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

// ============================================================================
// ReadFileTool — read file contents
// ============================================================================

type ReadFileTool struct{}

func (t *ReadFileTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "read_file",
		Description: "Read the contents of a file at a given path. Image files (png/jpeg/gif/webp/bmp) are attached for vision-capable models. For large files, pass offset (1-based line) and limit (max lines) to read them in chunks — the response reports the total line count so you can plan the next chunk.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute or relative path to the file",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Start line, 1-based. Omit to read from the top.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max number of lines to return. Use with offset to read large files in chunks (e.g. offset=501&limit=500 for the second 500-line block).",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	path, err := parseArg(args, "path")
	if err != nil {
		return nil, err
	}

	// Images are attached as vision payloads (read_image merged into read_file).
	if res, isImage := loadImageAttachment(path); isImage {
		return res, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return &types.ToolResult{Success: false, Content: "", Error: err.Error()}, nil
	}

	// Chunked reads (large-file parity): offset/limit select a 1-based line
	// window so the model can page through a big file without pulling the
	// whole thing into context. When neither is given, the full content is
	// returned as before.
	offset, limit := 0, 0
	if s, perr := parseArg(args, "offset"); perr == nil {
		offset, _ = strconv.Atoi(strings.TrimSpace(s))
	}
	if s, perr := parseArg(args, "limit"); perr == nil {
		limit, _ = strconv.Atoi(strings.TrimSpace(s))
	}
	if offset > 0 || limit > 0 {
		text := string(data)
		text = strings.TrimSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\r")
		lines := strings.Split(text, "\n")
		total := len(lines)
		if offset < 1 {
			offset = 1
		}
		if offset > total {
			offset = total
		}
		end := total
		if limit > 0 && offset-1+limit < end {
			end = offset - 1 + limit
		}
		chunk := lines[offset-1 : end]
		var sb strings.Builder
		fmt.Fprintf(&sb, "文件 %s 共 %d 行，当前返回第 %d–%d 行。\n",
			path, total, offset, end)
		if offset+len(chunk) <= total && len(chunk) >= 2 {
			fmt.Fprintf(&sb, "（继续读取请用 offset=%d 再取下一段）\n", offset+len(chunk))
		}
		for _, ln := range chunk {
			sb.WriteString(ln)
			sb.WriteString("\n")
		}
		return &types.ToolResult{Success: true, Content: sb.String()}, nil
	}

	return &types.ToolResult{Success: true, Content: string(data)}, nil
}

// ============================================================================
// WriteFileTool — write content to a file
// ============================================================================

type WriteFileTool struct{}

func (t *WriteFileTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "write_file",
		Description: "Write content to a file, creating parent directories as needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute or relative path to the file",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write to the file",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	path, err := parseArg(args, "path")
	if err != nil {
		return nil, err
	}
	content, err := parseArg(args, "content")
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	return &types.ToolResult{Success: true, Content: fmt.Sprintf("Wrote %d bytes to %s", len(content), path)}, nil
}

// ============================================================================
// GrepTool — search text in files (regex-aware)
// ============================================================================

type GrepTool struct{}

func (t *GrepTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "grep",
		Description: "Search for a pattern in files. The pattern is a regular expression; plain text also works (special characters are escaped automatically when the pattern is not a valid regex). Returns matching lines with file paths and line numbers.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "The regex pattern (or plain text) to search for",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in",
				},
				"ignore_case": map[string]any{
					"type":        "boolean",
					"description": "Case-insensitive search (default false)",
				},
			},
			"required": []string{"pattern", "path"},
		},
	}
}

func (t *GrepTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	pattern, err := parseArg(args, "pattern")
	if err != nil {
		return nil, err
	}
	searchPath, err := parseArg(args, "path")
	if err != nil {
		// Default to current directory
		searchPath = "."
	}
	ignoreCase := parseBoolArgWithDefault(args, "ignore_case", false)

	// Compile the pattern as a regex. If it fails to compile, fall back to
	// plain substring search (the old behavior) so a model that passes
	// literal text like "fmt.Println(" keeps working.
	re, reErr := regexp.Compile(pattern)
	if reErr != nil {
		re = nil
	} else if ignoreCase {
		// Recompile with the case-insensitive flag — lowering only the text
		// would break uppercase classes in the pattern itself.
		if reIC, err := regexp.Compile("(?i)" + pattern); err == nil {
			re = reIC
		}
	}

	// Use Go-native grep (cross-platform, no external dependency)
	var results []string
	filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			// Skip common directories
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == "release" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Skip binary files
		if isBinaryFile(path) {
			return nil
		}

		// Read file and search
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			var matched bool
			if re != nil {
				matched = re.MatchString(line)
			} else if ignoreCase {
				matched = strings.Contains(strings.ToLower(line), strings.ToLower(pattern))
			} else {
				matched = strings.Contains(line, pattern)
			}
			if matched {
				results = append(results, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})

	if len(results) == 0 {
		return &types.ToolResult{Success: true, Content: "No matches found."}, nil
	}

	// Limit results
	if len(results) > 100 {
		results = results[:100]
		results = append(results, fmt.Sprintf("\n... (%d more matches truncated)", len(results)-100))
	}

	return &types.ToolResult{Success: true, Content: strings.Join(results, "\n")}, nil
}

func isBinaryFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	binaryExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".bmp": true, ".ico": true, ".woff": true, ".woff2": true,
		".ttf": true, ".eot": true, ".zip": true, ".gz": true,
		".tar": true, ".7z": true, ".rar": true, ".pdf": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true,
	}
	return binaryExts[ext]
}

// ============================================================================
// GlobTool — find files by pattern
// ============================================================================

type GlobTool struct{}

func (t *GlobTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "glob",
		Description: "Find files matching a glob pattern (e.g., '**/*.go', 'src/*.ts').",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern to match files against",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GlobTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	pattern, err := parseArg(args, "pattern")
	if err != nil {
		return nil, err
	}

	// filepath.Glob does not support "**" recursion, but the schema advertises
	// '**/*.go' examples. Convert a leading "**/" into a recursive walk and
	// fall back to plain filepath.Glob for simple patterns.
	var matches []string
	if strings.Contains(pattern, "**") {
		matches = globDoubleStar(pattern)
	} else {
		m, err := filepath.Glob(pattern)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, nil
		}
		matches = m
	}

	var sb strings.Builder
	for _, m := range matches {
		sb.WriteString(m)
		sb.WriteString("\n")
	}
	return &types.ToolResult{Success: true, Content: sb.String()}, nil
}

// globDoubleStar implements the "**" recursion that filepath.Glob lacks.
// It walks from the fixed prefix of the pattern and matches each remaining
// segment against the relative paths at every depth, so '**/*.go' matches
// *.go at any depth and 'src/**/x.go' matches x.go under src recursively.
func globDoubleStar(pattern string) []string {
	// Split into the literal root and the recursive tail.
	idx := strings.Index(pattern, "**")
	root := strings.TrimSuffix(pattern[:idx], string(filepath.Separator))
	if root == "" {
		root = "."
	}
	tail := strings.TrimPrefix(pattern[idx+2:], string(filepath.Separator))
	tail = strings.TrimPrefix(tail, "/")

	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if tail == "" {
			out = append(out, path)
			return nil
		}
		// Match the tail against every suffix of rel so '**/x.go' also
		// matches x.go at depth 0 below root.
		ok := false
		parts := strings.Split(rel, string(filepath.Separator))
		for i := range parts {
			cand := strings.Join(parts[i:], string(filepath.Separator))
			if m, err := filepath.Match(tail, cand); err == nil && m {
				ok = true
				break
			}
		}
		if ok {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// ============================================================================
// EditTool — precise in-place string replacement with MultiEdit support
// ============================================================================

type EditTool struct{}

type editOp struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

type editInput struct {
	FilePath string   `json:"file_path"`
	OldStr   string   `json:"old_string"`
	NewStr   string   `json:"new_string"`
	Replace  bool     `json:"replace_all"`
	Edits    []editOp `json:"edits"`
}

func (t *EditTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "edit",
		Description: "Edit a file by replacing exact matching strings, preserving indentation. Single edit (file_path+old_string+new_string) or MultiEdit (file_path+edits[]) for one file.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "File to edit",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "[single mode] Exact text to find",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "[single mode] Replacement text",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "[single mode] Replace all matches (default: false)",
				},
				"edits": map[string]any{
					"type":        "array",
					"description": "[MultiEdit mode] Ordered replacements for one file",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"old_string": map[string]any{
								"type":        "string",
								"description": "Text to find",
							},
							"new_string": map[string]any{
								"type":        "string",
								"description": "Replacement text",
							},
							"replace_all": map[string]any{
								"type":        "boolean",
								"description": "Replace all matches",
							},
						},
						"required": []string{"old_string", "new_string"},
					},
				},
			},
			"oneOf": []any{
				map[string]any{"required": []string{"file_path", "old_string", "new_string"}},
				map[string]any{"required": []string{"file_path", "edits"}},
			},
		},
	}
}

func (t *EditTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in editInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{
			Success: false, Error: fmt.Sprintf("invalid args: %v", err),
		}, nil
	}
	if in.FilePath == "" {
		return &types.ToolResult{Success: false, Error: "file_path is required"}, nil
	}

	content, err := os.ReadFile(in.FilePath)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("read %s: %v", in.FilePath, err)}, nil
	}
	text := string(content)
	original := text

	totalReplacements := 0

	// applyEdit performs one replacement on text. When the exact old_string is
	// absent it retries with whitespace-normalized fuzzy matching (Aider-style
	// anchor matching) so a model that mismatches indentation still succeeds.
	applyEdit := func(oldStr, newStr string, replaceAll bool) (newText string, replaced int, errMsg string) {
		c := strings.Count(text, oldStr)
		if c > 0 {
			if c > 1 && !replaceAll {
				return "", 0, fmt.Sprintf("old_string %q appears %d times. Use replace_all or add context.", truncateStr(oldStr, 60), c)
			}
			if replaceAll {
				return strings.ReplaceAll(text, oldStr, newStr), c, ""
			}
			return strings.Replace(text, oldStr, newStr, 1), 1, ""
		}
		// Fuzzy fallback: whitespace-normalized match.
		idx, end, fuzzyNew := fuzzyFind(text, oldStr, newStr)
		if idx < 0 {
			return "", 0, fmt.Sprintf("old_string %q not found.", truncateStr(oldStr, 60))
		}
		return text[:idx] + fuzzyNew + text[end:], 1, ""
	}

	if len(in.Edits) > 0 {
		// MultiEdit mode: apply edits sequentially
		for _, ed := range in.Edits {
			if ed.OldString == "" {
				continue
			}
			nt, n, errMsg := applyEdit(ed.OldString, ed.NewString, ed.ReplaceAll)
			if errMsg != "" {
				// Let the model know which edit failed but keep partial progress
				return &types.ToolResult{
					Success: false,
					Content: fmt.Sprintf("after %d replacements, failed on: %s",
						totalReplacements, errMsg),
				}, nil
			}
			text = nt
			totalReplacements += n
		}
	} else {
		// Single edit mode (backward compatible)
		if in.OldStr == "" {
			return &types.ToolResult{Success: false, Error: "old_string is required"}, nil
		}
		nt, n, errMsg := applyEdit(in.OldStr, in.NewStr, in.Replace)
		if errMsg != "" {
			return &types.ToolResult{Success: false, Error: errMsg}, nil
		}
		text = nt
		totalReplacements = n
	}

	if err := os.WriteFile(in.FilePath, []byte(text), 0644); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("write: %v", err)}, nil
	}

	diffOut := unifiedDiff(in.FilePath, original, text)
	return &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Edited %s (%d replacements)\n%s", in.FilePath, totalReplacements, diffOut),
	}, nil
}

// fuzzyFind locates oldStr in text after collapsing runs of whitespace in
// both sides (so indentation or line-ending differences don't break the
// match). It returns the byte span of the matched region plus the
// replacement with the file's own indentation style applied — the caller
// splices matchedNew at idx..end, preserving the file's whitespace while
// applying the model's content change.
func fuzzyFind(text, oldStr, newStr string) (idx, end int, matchedNew string) {
	needleWords := wordSpans(oldStr)
	if len(needleWords) == 0 {
		return -1, 0, ""
	}
	textWords := wordSpans(text)

	// Find the first run of textWords whose word texts equal the needle's.
	needleTexts := make([]string, len(needleWords))
	for i, w := range needleWords {
		needleTexts[i] = oldStr[w.start:w.end]
	}
	matched := -1
	for i := 0; i+len(needleTexts) <= len(textWords); i++ {
		ok := true
		for k, nt := range needleTexts {
			if text[textWords[i+k].start:textWords[i+k].end] != nt {
				ok = false
				break
			}
		}
		if ok {
			matched = i
			break
		}
	}
	if matched < 0 {
		return -1, 0, ""
	}

	first := textWords[matched]
	last := textWords[matched+len(needleTexts)-1]
	idx = first.start
	end = last.end
	// Include any trailing whitespace of the matched region so the splice
	// lands cleanly on the line break.
	for end < len(text) && (text[end] == ' ' || text[end] == '\t' || text[end] == '\n' || text[end] == '\r') {
		end++
	}

	// Re-indent the replacement with the matched region's base indentation:
	// the whitespace between the start of the matched line and the match
	// itself (e.g. the "\t" before "if x {"), so multiline replacements stay
	// aligned inside their block.
	leadWS := leadingWhitespace(text[:idx])
	lines := strings.Split(newStr, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			lines[i] = leadWS + lines[i]
		}
	}
	matchedNew = strings.Join(lines, "\n")
	return idx, end, matchedNew
}

// wordSpan is a byte range of one whitespace-delimited word.
type wordSpan struct{ start, end int }

// wordSpans returns the byte spans of all whitespace-delimited words.
func wordSpans(s string) []wordSpan {
	var spans []wordSpan
	i := 0
	isWS := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
	for i < len(s) {
		for i < len(s) && isWS(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && !isWS(s[i]) {
			i++
		}
		spans = append(spans, wordSpan{start, i})
	}
	return spans
}

// leadingWhitespace returns the whitespace prefix of the LAST line of s
// (the indentation immediately before the match). Handles Windows line
// endings so the base indent is whatever is between the last \n and the
// matched token.
func leadingWhitespace(s string) string {
	i := len(s)
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
		i--
	}
	return s[i:]
}

// unifiedDiff produces a compact unified-diff-format string showing what
// changed. Uses Myers diff internally (simplified inline implementation).
func unifiedDiff(path, oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Find changed regions
	type hunk struct{ oldStart, newStart, oldCount, newCount int }
	var hunks []hunk

	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			i++
			j++
			continue
		}
		h := hunk{oldStart: i, newStart: j, oldCount: 0, newCount: 0}
		for i < len(oldLines) && j < len(newLines) && oldLines[i] != newLines[j] {
			h.oldCount++
			h.newCount++
			i++
			j++
		}
		// Drain remaining when one side is exhausted
		for i < len(oldLines) && (j >= len(newLines) || oldLines[i] != newLines[j]) {
			h.oldCount++
			i++
		}
		for j < len(newLines) && (i >= len(oldLines) || oldLines[i] != newLines[j]) {
			h.newCount++
			j++
		}
		if h.oldCount > 0 || h.newCount > 0 {
			hunks = append(hunks, h)
		}
	}
	// Remaining lines after the shorter side exhausted
	if i < len(oldLines) || j < len(newLines) {
		hunks = append(hunks, hunk{
			oldStart: i, newStart: j,
			oldCount: len(oldLines) - i,
			newCount: len(newLines) - j,
		})
	}

	if len(hunks) == 0 {
		return "(no changes)"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- %s\n+++ %s\n", path, path))
	for _, h := range hunks {
		if h.oldCount == 0 {
			h.oldStart-- // context before insertion
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
			h.oldStart+1, h.oldCount,
			h.newStart+1, h.newCount)

		o, n := h.oldStart, h.newStart
		for k := 0; k < h.oldCount || k < h.newCount; k++ {
			switch {
			case k < h.oldCount && k < h.newCount:
				b.WriteString(fmt.Sprintf("-%s\n+%s\n", oldLines[o+k], newLines[n+k]))
			case k < h.oldCount:
				b.WriteString(fmt.Sprintf("-%s\n", oldLines[o+k]))
			case k < h.newCount:
				b.WriteString(fmt.Sprintf("+%s\n", newLines[n+k]))
			}
		}
		_ = n
	}
	return b.String()
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func parseBoolArg(args, key string) (bool, error) {
	parsed, err := parseJSONArgs(args)
	if err != nil {
		return false, nil
	}
	if v, ok := parsed[key]; ok {
		if b, ok := v.(bool); ok {
			return b, nil
		}
	}
	return false, nil
}

// parseJSONArgs parses a JSON argument string into a map.
func parseJSONArgs(rawJSON string) (map[string]any, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ============================================================================
// GitTool — git diff/commit/status
// ============================================================================

type GitDiffTool struct{}

func (t *GitDiffTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_diff",
		Description: "Show git diff for staged or unstaged changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"staged": map[string]any{
					"type":        "boolean",
					"description": "If true, show staged (cached) changes. Default: false.",
				},
			},
		},
	}
}

func (t *GitDiffTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	staged, _ := parseBoolArg(args, "staged")

	cmdArgs := []string{"diff"}
	if staged {
		cmdArgs = append(cmdArgs, "--cached")
	}

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := executil.CommandContext(ctx2, "git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git diff: %v", err)}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

type GitCommitTool struct{}

func (t *GitCommitTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_commit",
		Description: "Stage changes and commit with a message. By default stages ALL changes; pass 'files' to stage specific paths only.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "Commit message",
				},
				"files": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional: paths to stage and commit (relative to repo root). When omitted, all changes are staged.",
				},
			},
			"required": []string{"message"},
		},
	}
}

func (t *GitCommitTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	msg, err := parseArg(args, "message")
	if err != nil {
		return nil, err
	}

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Stage specific files when given; otherwise stage everything. Selective
	// staging keeps unrelated work out of the commit (Claude Code parity).
	var addArgs []string
	if files := parseStringArrayArg(args, "files"); len(files) > 0 {
		addArgs = append(addArgs, files...)
	} else {
		addArgs = append(addArgs, "-A")
	}
	addCmd := executil.CommandContext(ctx2, "git", append([]string{"add"}, addArgs...)...)
	if err := addCmd.Run(); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git add: %v", err)}, nil
	}

	// git commit -m
	commitCmd := executil.CommandContext(ctx2, "git", "commit", "-m", msg)
	output, err := commitCmd.CombinedOutput()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git commit: %v\n%s", err, string(output))}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

type GitStatusTool struct{}

func (t *GitStatusTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_status",
		Description: "Show git status (modified, staged, untracked files).",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *GitStatusTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := executil.CommandContext(ctx2, "git", "status", "--short", "--branch")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git status: %v", err)}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

type GitLogTool struct{}

func (t *GitLogTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_log",
		Description: "Show recent commit history (hash, date, message).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"n": map[string]any{
					"type":        "integer",
					"description": "Number of commits to show. Default: 20.",
				},
			},
		},
	}
}

func (t *GitLogTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	n := 20
	if nStr, err := parseArg(args, "n"); err == nil {
		if v, convErr := strconv.Atoi(strings.TrimSpace(nStr)); convErr == nil && v > 0 && v <= 200 {
			n = v
		}
	}

	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := executil.CommandContext(ctx2, "git", "log",
		"--pretty=format:%h %ad %s", "--date=short", "-n", strconv.Itoa(n))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git log: %v", err)}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

type GitBranchTool struct{}

func (t *GitBranchTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "git_branch",
		Description: "List local/remote branches (current marked with *), or switch to an existing branch by name.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch name to switch to. Omit to list branches.",
				},
			},
		},
	}
}

func (t *GitBranchTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	branch, _ := parseArg(args, "branch")

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if branch != "" {
		cmd := executil.CommandContext(ctx2, "git", "checkout", branch)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("git checkout: %v\n%s", err, string(output))}, nil
		}
		return &types.ToolResult{Success: true, Content: string(output)}, nil
	}

	cmd := executil.CommandContext(ctx2, "git", "branch", "-a", "-vv")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("git branch: %v", err)}, nil
	}

	return &types.ToolResult{Success: true, Content: string(output)}, nil
}

// ============================================================================
// LSTool — list directory contents
// ============================================================================

type LSTool struct{}

func (t *LSTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "ls",
		Description: "List files and directories at a given path.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path to list",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *LSTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	dir, err := parseArg(args, "path")
	if err != nil {
		dir = "."
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			sb.WriteString(fmt.Sprintf("DIR  %s\n", e.Name()))
		} else {
			info, _ := e.Info()
			sb.WriteString(fmt.Sprintf("FILE %-30s %d bytes\n", e.Name(), info.Size()))
		}
	}
	return &types.ToolResult{Success: true, Content: sb.String()}, nil
}

// ============================================================================
// FetchTool — HTTP GET a URL
// ============================================================================

type FetchTool struct{}

func (t *FetchTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "fetch",
		Description: "Fetch content from a URL (HTTP GET).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (t *FetchTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	url, err := parseArg(args, "url")
	if err != nil {
		return nil, err
	}

	if err := validateFetchURL(url); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			// Re-validate every redirect hop so a public URL can't bounce us
			// onto loopback/private/metadata targets (SSRF via redirect).
			if err := validateFetchURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}

	httpreq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	httpreq.Header.Set("User-Agent", "iCode/0.1.0")

	resp, err := client.Do(httpreq)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	maxSize := int64(256 * 1024) // 256KB limit
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	content := string(body)
	// HTML pages → readable plain text (Claude Code WebFetch parity): raw
	// markup would burn tokens and hurt comprehension. JSON/plain responses
	// stay untouched.
	if isHTML(content, resp.Header.Get("Content-Type")) {
		content = htmlToText(content, 12000)
	} else if int64(len(body)) >= maxSize {
		content += "\n\n[Response truncated at 256KB]"
	}

	return &types.ToolResult{
		Success: true,
		Content: content,
	}, nil
}

// validateFetchURL blocks SSRF targets before the fetch tool dials them:
// only http(s) schemes, and no loopback / private / link-local addresses
// (which include cloud metadata 169.254.169.254). Every resolved IP is checked
// so a DNS name mixing public + private records can't slip through.
func validateFetchURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("fetch: invalid URL %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("fetch: only http/https URLs are allowed (got %q)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("fetch: missing host in %q", raw)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("fetch: cannot resolve host %q", host)
	}
	if len(ips) == 0 {
		return fmt.Errorf("fetch: no addresses for host %q", host)
	}
	for _, ip := range ips {
		if blockedBySSRF(ip) {
			return fmt.Errorf("fetch: blocked address %s (private/loopback/link-local not allowed)", ip)
		}
	}
	return nil
}

func blockedBySSRF(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 127: // loopback
			return true
		case v4[0] == 0: // 0.0.0.0/8
			return true
		case v4[0] == 10: // 10.0.0.0/8
			return true
		case v4[0] == 169 && v4[1] == 254: // 169.254.0.0/16 link-local + cloud metadata
			return true
		case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31: // 172.16.0.0/12
			return true
		case v4[0] == 192 && v4[1] == 168: // 192.168.0.0/16
			return true
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // CGNAT 100.64.0.0/10
			return true
		default:
			return false
		}
	}
	// IPv6
	switch {
	case ip.IsLoopback():
		return true
	case ip.IsLinkLocalUnicast(): // fe80::/10
		return true
	case ip.IsLinkLocalMulticast(): // ff02::/16
		return true
	case ip.IsPrivate(): // fc00::/7
		return true
	case ip.IsUnspecified(): // ::
		return true
	default:
		return false
	}
}

// ============================================================================
// Helpers
// ============================================================================

func parseArg(rawJSON, key string) (string, error) {
	// Primary: strict JSON parsing (handles escaped quotes, nested content).
	if m, err := parseJSONArgs(rawJSON); err == nil {
		v, ok := m[key]
		if !ok {
			return "", fmt.Errorf("missing required argument: %s", key)
		}
		switch val := v.(type) {
		case string:
			return val, nil
		case bool:
			return fmt.Sprintf("%t", val), nil
		case float64:
			// Preserve integer-looking numbers without trailing .0
			if val == float64(int64(val)) {
				return fmt.Sprintf("%d", int64(val)), nil
			}
			return fmt.Sprintf("%v", val), nil
		default:
			return fmt.Sprintf("%v", val), nil
		}
	}

	// Fallback: lenient substring search for loosely-formatted arguments.
	raw := strings.TrimSpace(rawJSON)
	search := fmt.Sprintf(`"%s":`, key)
	idx := strings.Index(raw, search)
	if idx < 0 {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	start := idx + len(search)
	rest := strings.TrimSpace(raw[start:])
	if !strings.HasPrefix(rest, `"`) {
		return "", fmt.Errorf("argument %s must be a string", key)
	}
	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return rest[1:], nil
	}
	return rest[1 : end+1], nil
}

// parseStringArrayArg extracts a []string argument (e.g. the git_commit
// "files" list). Returns nil when the key is absent or not a JSON array.
func parseStringArrayArg(rawJSON, key string) []string {
	m, err := parseJSONArgs(rawJSON)
	if err != nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
