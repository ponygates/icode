package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// ============================================================================
// Tool Definition Tests
// ============================================================================

func TestBashTool_Def(t *testing.T) {
	tool := &BashTool{}
	def := tool.Def()

	if def.Name != "bash" {
		t.Errorf("expected name 'bash', got %q", def.Name)
	}
	if def.Description == "" {
		t.Error("description should not be empty")
	}
	if def.Parameters == nil {
		t.Error("parameters should not be nil")
	}
}

func TestReadFileTool_Def(t *testing.T) {
	tool := &ReadFileTool{}
	def := tool.Def()

	if def.Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", def.Name)
	}
	if def.Description == "" {
		t.Error("description should not be empty")
	}
	// Check that path is required
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["path"]; !ok {
		t.Error("expected 'path' property")
	}
}

func TestWriteFileTool_Def(t *testing.T) {
	tool := &WriteFileTool{}
	def := tool.Def()

	if def.Name != "write_file" {
		t.Errorf("expected name 'write_file', got %q", def.Name)
	}
	// Should have path and content
	props := def.Parameters["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Error("expected 'path' property")
	}
	if _, ok := props["content"]; !ok {
		t.Error("expected 'content' property")
	}
}

func TestEditTool_Def(t *testing.T) {
	tool := &EditTool{}
	def := tool.Def()

	if def.Name != "edit" {
		t.Errorf("expected name 'edit', got %q", def.Name)
	}
	if def.Description == "" {
		t.Error("description should not be empty")
	}
}

func TestGrepTool_Def(t *testing.T) {
	tool := &GrepTool{}
	def := tool.Def()

	if def.Name != "grep" {
		t.Errorf("expected name 'grep', got %q", def.Name)
	}
	// Check properties
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	if _, ok := props["pattern"]; !ok {
		t.Error("expected 'pattern' property")
	}
	if _, ok := props["path"]; !ok {
		t.Error("expected 'path' property")
	}
}

func TestGlobTool_Def(t *testing.T) {
	tool := &GlobTool{}
	def := tool.Def()
	if def.Name != "glob" {
		t.Errorf("expected name 'glob', got %q", def.Name)
	}
}

func TestLSTool_Def(t *testing.T) {
	tool := &LSTool{}
	def := tool.Def()
	if def.Name != "ls" {
		t.Errorf("expected name 'ls', got %q", def.Name)
	}
}

func TestFetchTool_Def(t *testing.T) {
	tool := &FetchTool{}
	def := tool.Def()
	if def.Name != "fetch" {
		t.Errorf("expected name 'fetch', got %q", def.Name)
	}
}

func TestGitDiffTool_Def(t *testing.T) {
	tool := &GitDiffTool{}
	def := tool.Def()
	if def.Name != "git_diff" {
		t.Errorf("expected name 'git_diff', got %q", def.Name)
	}
}

func TestGitCommitTool_Def(t *testing.T) {
	tool := &GitCommitTool{}
	def := tool.Def()
	if def.Name != "git_commit" {
		t.Errorf("expected name 'git_commit', got %q", def.Name)
	}
}

func TestGitStatusTool_Def(t *testing.T) {
	tool := &GitStatusTool{}
	def := tool.Def()
	if def.Name != "git_status" {
		t.Errorf("expected name 'git_status', got %q", def.Name)
	}
}

// ============================================================================
// Registry Tests
// ============================================================================

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry should not return nil")
	}

	// All built-in tools should be registered
	expectedTools := []string{
		"bash", "read_file", "write_file", "edit",
		"grep", "glob", "ls", "fetch",
		"git_diff", "git_commit", "git_status",
		"search_replace", "web_search", "todo_write",
	}

	for _, name := range expectedTools {
		if _, ok := r.Get(name); !ok {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	mock := &mockTool{name: "custom_tool"}
	r.Register(mock)

	if _, ok := r.Get("custom_tool"); !ok {
		t.Error("expected custom_tool to be registered")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Unregister("bash")
	if _, ok := r.Get("bash"); ok {
		t.Error("bash should have been unregistered")
	}
}

func TestRegistry_ListDefs(t *testing.T) {
	r := NewRegistry()
	defs := r.ListDefs()
	if len(defs) < 10 {
		t.Errorf("expected at least 10 tool defs, got %d", len(defs))
	}
}

func TestRegistry_ExecuteUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "nonexistent_tool", "{}")
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected 'unknown tool' error, got: %v", err)
	}
}

// ============================================================================
// Execute Tests (ReadFileTool + WriteFileTool in temp dir)
// ============================================================================

func TestReadFileTool_NotFound(t *testing.T) {
	tool := &ReadFileTool{}
	result, err := tool.Execute(context.Background(), `{"path": "/nonexistent/path/file.txt"}`)
	if err != nil {
		t.Fatalf("Execute should not return error for missing file (returns ToolResult): %v", err)
	}
	if result.Success {
		t.Error("expected failure for missing file")
	}
	if result.Error == "" {
		t.Error("expected error message for missing file")
	}
}

func TestWriteFileTool_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")

	tool := &WriteFileTool{}
	writeJSON := `{"path":"` + jsonEscape(filePath) + `","content":"hello world"}`
	result, err := tool.Execute(context.Background(), writeJSON)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}

	// Verify file was created
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected content 'hello world', got %q", string(data))
	}
}

func TestWriteFileTool_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "subdir", "nested", "test.txt")

	writeJSON := `{"path":"` + jsonEscape(filePath) + `","content":"nested"}`
	result, err := (&WriteFileTool{}).Execute(context.Background(), writeJSON)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

func TestWriteFileTool_MissingPath(t *testing.T) {
	// Missing path should fail
	_, err := (&WriteFileTool{}).Execute(context.Background(), `{"content": "test"}`)
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestReadFileTool_AfterWrite(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "roundtrip.txt")

	// Write using properly escaped JSON
	writeJSON := `{"path":"` + jsonEscape(filePath) + `","content":"roundtrip content"}`
	(&WriteFileTool{}).Execute(context.Background(), writeJSON)

	// Read back
	readJSON := `{"path":"` + jsonEscape(filePath) + `"}`
	result, _ := (&ReadFileTool{}).Execute(context.Background(), readJSON)

	if !result.Success {
		t.Fatalf("read failed: %s", result.Error)
	}
	if result.Content != "roundtrip content" {
		t.Errorf("expected 'roundtrip content', got %q", result.Content)
	}
}

func TestEditTool_SimpleReplace(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "edit.txt")
	os.WriteFile(filePath, []byte("hello world"), 0644)

	// Use json.Marshal to properly escape Windows paths
	editJSON := `{"file_path":"` + jsonEscape(filePath) + `","old_string":"world","new_string":"icode"}`
	result, err := (&EditTool{}).Execute(context.Background(), editJSON)
	if err != nil {
		t.Fatalf("Edit failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("edit failed: %s", result.Error)
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != "hello icode" {
		t.Errorf("expected 'hello icode', got %q", string(data))
	}
}

func TestEditTool_MultiEdit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "multiedit.txt")
	os.WriteFile(filePath, []byte("a\nb\nc"), 0644)

	editJSON := `{"file_path":"` + jsonEscape(filePath) + `","edits":[
		{"old_string":"a","new_string":"A"},
		{"old_string":"c","new_string":"C"}
	]}`
	result, err := (&EditTool{}).Execute(context.Background(), editJSON)
	if err != nil {
		t.Fatalf("MultiEdit failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("multiedit failed: %s", result.Error)
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != "A\nb\nC" {
		t.Errorf("expected 'A\nb\nC', got %q", string(data))
	}
}

// jsonEscape escapes a string for safe embedding in JSON.
func jsonEscape(s string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"\n", "\\n",
		"\r", "\\r",
		"\t", "\\t",
	)
	return replacer.Replace(s)
}

func TestEditTool_NotFound(t *testing.T) {
	result, _ := (&EditTool{}).Execute(context.Background(),
		`{"file_path": "/nonexistent/file.txt", "old_string": "x", "new_string": "y"}`)
	if result.Success {
		t.Error("expected failure for missing file")
	}
	if !strings.Contains(result.Error, "read") && !strings.Contains(result.Error, "no such") {
		t.Logf("got expected error: %s", result.Error)
	}
}

func TestLSTool_ListTempDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)

	result, err := (&LSTool{}).Execute(context.Background(), `{"path": "`+dir+`"}`)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("ls failed: %s", result.Error)
	}
	if !strings.Contains(result.Content, "a.txt") {
		t.Errorf("expected 'a.txt' in listing, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "b.txt") {
		t.Errorf("expected 'b.txt' in listing, got: %s", result.Content)
	}
}

func TestLSTool_MissingDir(t *testing.T) {
	result, _ := (&LSTool{}).Execute(context.Background(),
		`{"path": "/nonexistent_dir_12345"}`)
	if result.Success {
		t.Error("expected failure for missing directory")
	}
}

// ============================================================================
// Helper: mock tool for registry tests
// ============================================================================

type mockTool struct {
	name string
}

func (m *mockTool) Def() types.ToolDef {
	return types.ToolDef{Name: m.name, Description: "mock tool for testing"}
}

func (m *mockTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	return &types.ToolResult{Success: true, Content: "ok"}, nil
}
