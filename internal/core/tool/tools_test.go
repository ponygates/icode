package tool

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// TestBashTool_StreamsProgress verifies that when a progress callback is
// attached to the context, bash output arrives incrementally (while the
// tool is still executing) AND is still accumulated into the final result.
func TestBashTool_StreamsProgress(t *testing.T) {
	var mu sync.Mutex
	var chunks []string
	var executing int32 // atomic: 1 while Execute is still running
	ctx := WithProgress(context.Background(), func(chunk string) {
		mu.Lock()
		chunks = append(chunks, chunk)
		mu.Unlock()
		if atomic.LoadInt32(&executing) == 1 {
			// At least one chunk arrived mid-execution — the streaming path works.
			atomic.StoreInt32(&streamingObserved, 1)
		}
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer atomic.StoreInt32(&executing, 0)
		atomic.StoreInt32(&executing, 1)
		res, err := (&BashTool{}).Execute(ctx, `{"command": "echo line1 && echo line2 && echo line3"}`)
		if err != nil {
			t.Errorf("Execute: %v", err)
			return
		}
		if !res.Success {
			t.Errorf("expected success, got: %s", res.Error)
			return
		}
		if !strings.Contains(res.Content, "line1") || !strings.Contains(res.Content, "line3") {
			t.Errorf("result should carry the full output, got %q", res.Content)
		}
	}()
	<-done

	mu.Lock()
	joined := strings.Join(chunks, "")
	mu.Unlock()
	if atomic.LoadInt32(&streamingObserved) != 1 {
		t.Fatal("no progress chunk arrived while the command was still executing")
	}
	if !strings.Contains(joined, "line1") || !strings.Contains(joined, "line3") {
		t.Errorf("progress chunks should carry the output, got %q", joined)
	}
}

// streamingObserved is set when a progress chunk arrives mid-execution.
// Package-level because the callback closes over it.
var streamingObserved int32

// TestBashTool_NoProgressKeepsCombinedOutput guards the non-streaming path:
// without a progress callback the tool behaves exactly as before.
func TestBashTool_NoProgressKeepsCombinedOutput(t *testing.T) {
	res, err := (&BashTool{}).Execute(context.Background(), `{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success || !strings.Contains(res.Content, "hello") {
		t.Fatalf("expected success with output, got success=%v content=%q err=%q", res.Success, res.Content, res.Error)
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
	// Chunked-read params advertised (large-file parity)
	if _, ok := props["offset"]; !ok {
		t.Error("expected 'offset' property")
	}
	if _, ok := props["limit"]; !ok {
		t.Error("expected 'limit' property")
	}
}

// TestReadFileTool_Chunked verifies offset/limit line-window reads: only the
// requested block is returned, the header reports the total line count, and a
// "continue with offset=N" hint is emitted when more content follows.
func TestReadFileTool_Chunked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&sb, "line %03d\n", i)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tool := &ReadFileTool{}

	// Second 10-line block (lines 11-20).
	res, err := tool.Execute(context.Background(), fmt.Sprintf(`{"path": %q, "offset": 11, "limit": 10}`, path))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.Content, "共 100 行") {
		t.Fatalf("header missing total line count: %q", res.Content)
	}
	if !strings.Contains(res.Content, "第 11–20 行") {
		t.Fatalf("header missing window range: %q", res.Content)
	}
	if strings.Contains(res.Content, "line 010") || strings.Contains(res.Content, "line 021") {
		t.Fatalf("chunk leaked outside the window: %q", res.Content)
	}
	if !strings.Contains(res.Content, "line 011") || !strings.Contains(res.Content, "line 020") {
		t.Fatalf("chunk missing expected lines: %q", res.Content)
	}
	if !strings.Contains(res.Content, "offset=21") {
		t.Fatalf("continue hint missing: %q", res.Content)
	}

	// Last block with nothing after → no continue hint.
	res2, err := tool.Execute(context.Background(), fmt.Sprintf(`{"path": %q, "offset": 95, "limit": 10}`, path))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res2.Content, "第 95–100 行") {
		t.Fatalf("tail window range wrong: %q", res2.Content)
	}
	if strings.Contains(res2.Content, "offset=") {
		t.Fatalf("tail chunk must not offer a next block: %q", res2.Content)
	}

	// No offset/limit → full content (backward compatible).
	res3, err := tool.Execute(context.Background(), fmt.Sprintf(`{"path": %q}`, path))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res3.Content, "line 100") {
		t.Fatalf("full read missing tail: %q", res3.Content)
	}

	// offset past EOF clamps to the last line.
	res4, err := tool.Execute(context.Background(), fmt.Sprintf(`{"path": %q, "offset": 500, "limit": 5}`, path))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res4.Content, "line 100") {
		t.Fatalf("clamped read should show last line: %q", res4.Content)
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

// TestGrepTool_Regex verifies the schema-advertised regex behavior.
func TestGrepTool_Regex(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("a.txt", "hello world\nfoo bar\n")
	write("b.txt", "HELLO there\n")

	// Regex: ^foo matches only the line starting with foo.
	res, err := (&GrepTool{}).Execute(context.Background(),
		fmt.Sprintf(`{"pattern": "^foo", "path": %q}`, dir))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success || !strings.Contains(res.Content, "foo bar") || strings.Contains(res.Content, "hello") {
		t.Fatalf("regex ^foo should match only foo bar, got: %q", res.Content)
	}

	// Invalid regex falls back to substring (old behavior).
	res, err = (&GrepTool{}).Execute(context.Background(),
		fmt.Sprintf(`{"pattern": "hello", "path": %q}`, dir))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "hello world") {
		t.Fatalf("substring fallback failed, got: %q", res.Content)
	}

	// ignore_case matches HELLO in b.txt.
	res, err = (&GrepTool{}).Execute(context.Background(),
		fmt.Sprintf(`{"pattern": "hello", "path": %q, "ignore_case": true}`, dir))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "HELLO there") {
		t.Fatalf("ignore_case should match HELLO, got: %q", res.Content)
	}
}

// TestGlobTool_DoubleStar verifies "**" recursion matches at any depth.
func TestGlobTool_DoubleStar(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "deep", "deeper"), 0755)
	os.MkdirAll(filepath.Join(dir, "pkg"), 0755)
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("root.go")
	write("src/a.go")
	write("src/deep/b.go")
	write("src/deep/deeper/c.go")
	write("pkg/d.go")

	pattern := filepath.Join(dir, "**", "*.go")
	res, err := (&GlobTool{}).Execute(context.Background(), fmt.Sprintf(`{"pattern": %q}`, pattern))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"root.go", "a.go", "b.go", "c.go", "d.go"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("**/*.go should match %s, got: %q", want, res.Content)
		}
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

// TestEditTool_FuzzyWhitespace verifies the whitespace-normalized fallback:
// a model that mismatches indentation still succeeds, and the file's own
// indentation is preserved in the replacement.
func TestEditTool_FuzzyWhitespace(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "fuzzy.go")
	os.WriteFile(filePath, []byte("func main() {\n\t    fmt.Println(\"a\")\n\tfmt.Println(\"b\")\n}"), 0644)

	// Model's old_string uses 4 spaces; the file uses tabs.
	editJSON := `{"file_path":"` + jsonEscape(filePath) + `",
		"old_string":"    fmt.Println(\"a\")",
		"new_string":"    fmt.Println(\"A\")"}`
	result, err := (&EditTool{}).Execute(context.Background(), editJSON)
	if err != nil {
		t.Fatalf("Edit failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("fuzzy edit failed: %s", result.Error)
	}

	data, _ := os.ReadFile(filePath)
	if !strings.Contains(string(data), "fmt.Println(\"A\")") {
		t.Errorf("fuzzy edit should replace the matched line, got: %q", string(data))
	}
	// The tab-indented second line must survive untouched.
	if !strings.Contains(string(data), "\tfmt.Println(\"b\")") {
		t.Errorf("unrelated line must survive, got: %q", string(data))
	}
}

// TestEditTool_FuzzyNoMatch verifies an unknown old_string still errors
// (fuzzy matching must not invent matches).
func TestEditTool_FuzzyNoMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "nomatch.txt")
	os.WriteFile(filePath, []byte("hello world"), 0644)

	result, _ := (&EditTool{}).Execute(context.Background(),
		`{"file_path":"`+jsonEscape(filePath)+`","old_string":"totally different","new_string":"x"}`)
	if result.Success {
		t.Error("unknown old_string must fail")
	}
	if !strings.Contains(result.Error, "not found") {
		t.Errorf("expected 'not found' error, got: %q", result.Error)
	}
}

// TestEditTool_FuzzyMultiLine verifies multiline fuzzy matches re-indent the
// replacement with the matched region's base indentation, keeping the
// model's relative nesting.
func TestEditTool_FuzzyMultiLine(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "multi.go")
	os.WriteFile(filePath, []byte("func a() {\n\tif x {\n\t\tfoo()\n\t}\n}"), 0644)

	// Model passes the block with different indentation.
	editJSON := `{"file_path":"` + jsonEscape(filePath) + `",
		"old_string":"if x {\n        foo()\n    }",
		"new_string":"if x {\n        bar()\n    }"}`
	result, _ := (&EditTool{}).Execute(context.Background(), editJSON)
	if !result.Success {
		t.Fatalf("multiline fuzzy edit failed: %s", result.Error)
	}
	data, _ := os.ReadFile(filePath)
	// The replacement landed (bar exists, foo is gone)…
	if !strings.Contains(string(data), "bar()") || strings.Contains(string(data), "foo()") {
		t.Errorf("fuzzy multiline should swap foo for bar, got: %q", string(data))
	}
	// …and the match's own line keeps its file indentation (tab before if).
	if !strings.Contains(string(data), "\tif x {") {
		t.Errorf("matched line must keep its file indentation, got: %q", string(data))
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

// Registry map access must be safe under concurrent Register/Unregister/Get/
// ListDefs (MCP tool refresh runs in a background goroutine while chat reads).
func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			r.Register(&LSTool{})
			_, _ = r.Get("ls")
			_ = r.ListDefs()
		}
	}()
	for i := 0; i < 500; i++ {
		r.Unregister("ls")
		_, _ = r.Get("ls")
		_ = r.ListDefs()
		r.Register(&LSTool{})
	}
	<-done
}

func TestValidateFetchURL_BlocksSSRF(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1:8080/admin",             // loopback
		"http://169.254.169.254/latest/meta-data", // cloud metadata
		"http://10.0.0.5/",                        // private
		"http://192.168.1.1/",                     // private
		"http://172.16.0.1/",                      // private
		"http://localhost:3000",                   // loopback hostname
		"file:///etc/passwd",                      // non-http scheme
		"ftp://example.com/x",                     // non-http scheme
	}
	for _, u := range blocked {
		if err := validateFetchURL(u); err == nil {
			t.Errorf("validateFetchURL(%q) = nil, want block", u)
		}
	}
}

func TestValidateFetchURL_AllowsPublic(t *testing.T) {
	// blockedBySSRF is the network-independent core; public IPs must pass.
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2001:4860:4860::8888"}
	for _, s := range public {
		if blockedBySSRF(net.ParseIP(s)) {
			t.Errorf("blockedBySSRF(%q) = true, want false", s)
		}
	}
}

// TestReadFileTool_Image verifies read_file attaches a local PNG for vision
// models (read_image merged into read_file, E2) and leaves text as content.
func TestReadFileTool_Image(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pixel.png")
	img, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	res, err := (&ReadFileTool{}).Execute(context.Background(), fmt.Sprintf(`{"path": %q}`, path))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if len(res.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(res.Attachments))
	}
	att := res.Attachments[0]
	if att.Type != "image" || att.MIMEType != "image/png" {
		t.Fatalf("unexpected attachment: type=%q mime=%q", att.Type, att.MIMEType)
	}
	if att.Data == "" || !strings.HasPrefix(att.Data, "iVBOR") {
		t.Fatalf("expected base64 PNG payload in attachment")
	}

	if res, _ := (&ReadFileTool{}).Execute(context.Background(), `{"path": "definitely-missing.png"}`); res.Success {
		t.Fatal("missing file should fail")
	}
}
