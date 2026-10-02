package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// Real subprocess tests. The previous suite only ever poked the JSON plumbing
// with in-memory fakes, which is how a stdio transport that deadlocked on its
// very first `initialize` survived at 10% coverage: nothing ever started a
// server process. These tests do, over the actual pipe.

var (
	fakeOnce   sync.Once
	fakeBinary string
	fakeDir    string
	fakeBuild  error
)

// fakeServerPath compiles testdata/fakeserver once per package.
func fakeServerPath(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		goBin, err := exec.LookPath("go")
		if err != nil {
			fakeBuild = fmt.Errorf("go toolchain not found: %w", err)
			return
		}
		dir, err := os.MkdirTemp("", "icodemcpfake")
		if err != nil {
			fakeBuild = err
			return
		}
		fakeDir = dir
		fakeBinary = filepath.Join(dir, "fakeserver"+exeSuffix())
		cmd := exec.Command(goBin, "build", "-o", fakeBinary, "./testdata/fakeserver")
		// No cmd.Dir: the working directory is already this package, where
		// testdata/fakeserver lives, and the module root is an ancestor.
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			fakeBuild = fmt.Errorf("build fakeserver: %v: %s", err, stderr.String())
			return
		}
	})
	if fakeBuild != nil {
		t.Skipf("fake MCP server unavailable: %v", fakeBuild)
	}
	return fakeBinary
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// TestMain removes the scratch build directory once every test is done —
// the build happens inside a sync.Once, where t.Cleanup is not available.
func TestMain(m *testing.M) {
	code := m.Run()
	if fakeDir != "" {
		_ = os.RemoveAll(fakeDir)
	}
	os.Exit(code)
}

func newFakeConfig(binary, name string, extra ...string) ServerConfig {
	return ServerConfig{
		Name:    name,
		Type:    TransportStdio,
		Command: binary,
		Args:    extra,
		Env:     []string{"FAKE_SERVER_NAME=" + name},
		Enabled: true,
	}
}

func connectFake(t *testing.T, name string) *Client {
	t.Helper()
	c := NewClient(newFakeConfig(fakeServerPath(t), name))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.DiscoverTools(ctx); err != nil {
		t.Fatalf("discover tools: %v", err)
	}
	return c
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %v: %s", timeout, msg)
}

// A handshake that never completed is the bug these tests exist for.
func TestIntegration_StdioHandshakeAndToolCall(t *testing.T) {
	c := connectFake(t, "alpha")

	tools := c.Tools()
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d: %v", len(tools), toolNames(tools))
	}
	for _, tool := range tools {
		if !strings.HasPrefix(tool.Name, "mcp_alpha_") {
			t.Errorf("tool %q not namespaced under mcp_alpha_", tool.Name)
		}
		if tool.ServerName != "alpha" {
			t.Errorf("tool %q ServerName = %q, want alpha", tool.Name, tool.ServerName)
		}
	}

	res, err := c.CallTool(context.Background(), "mcp_alpha_echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.Success {
		t.Fatalf("call unsuccessful: %s", res.Error)
	}
	var got struct {
		Server       string `json:"server"`
		ReceivedName string `json:"received_name"`
	}
	if err := json.Unmarshal([]byte(res.Content), &got); err != nil {
		t.Fatalf("content not JSON: %q", res.Content)
	}
	// The model-facing name is namespaced; the server must only ever see "echo".
	if got.ReceivedName != "echo" {
		t.Errorf("server received tool name %q, want %q", got.ReceivedName, "echo")
	}
}

func TestIntegration_SchemaRoundTrip(t *testing.T) {
	c := connectFake(t, "alpha")
	def := c.Tools()[0]
	if def.Name == "" || def.Description == "" {
		t.Fatalf("tool def incomplete: %+v", def)
	}
	// A tool's input schema must survive discovery so the model can call it.
	if !strings.Contains(fmt.Sprint(def.Parameters), "object") {
		t.Errorf("input schema lost: %+v", def.Parameters)
	}
}

func TestIntegration_PoolCollisionRouting(t *testing.T) {
	bin := fakeServerPath(t)
	pool := NewPool()
	t.Cleanup(pool.CloseAll)
	ctx := context.Background()

	// "beta" and "beta " sanitize to the same component; without namespacing
	// both would publish mcp_beta_* and the pool could not tell them apart.
	if err := pool.Add(ctx, newFakeConfig(bin, "beta")); err != nil {
		t.Fatalf("add beta: %v", err)
	}
	if err := pool.Add(ctx, newFakeConfig(bin, "beta-2")); err != nil {
		t.Fatalf("add beta-2: %v", err)
	}

	all := pool.AllTools()
	seen := map[string]bool{}
	for _, toolDef := range all {
		if seen[toolDef.Name] {
			t.Fatalf("duplicate tool name published by two servers: %s", toolDef.Name)
		}
		seen[toolDef.Name] = true
	}
	if pool.Count() != 2 {
		t.Fatalf("pool count = %d, want 2", pool.Count())
	}

	// Route each server's whoami and confirm it lands on the right process.
	for _, name := range []string{"beta", "beta-2"} {
		tools := pool.ToolsByServer(name)
		if len(tools) == 0 {
			t.Fatalf("ToolsByServer(%q) empty", name)
		}
		var who string
		for _, toolDef := range tools {
			if strings.HasSuffix(toolDef.Name, "_whoami") {
				who = toolDef.Name
			}
		}
		if who == "" {
			t.Fatalf("no whoami tool for %q", name)
		}
		res, err := pool.Execute(ctx, who, nil)
		if err != nil {
			t.Fatalf("execute %s: %v", who, err)
		}
		want := strings.TrimSuffix(strings.TrimPrefix(who, "mcp_"), "_whoami")
		want = strings.ReplaceAll(want, "-", "_")
		if strings.ReplaceAll(res.Content, "-", "_") != want {
			t.Errorf("tool %s routed to server %q, want %q", who, res.Content, want)
		}
	}
}

func TestIntegration_UnknownToolRejected(t *testing.T) {
	pool := NewPool()
	t.Cleanup(pool.CloseAll)
	if err := pool.Add(context.Background(), newFakeConfig(fakeServerPath(t), "gamma")); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := pool.Execute(context.Background(), "mcp_gamma_does_not_exist", nil); err == nil {
		t.Fatal("expected unknown tool to error")
	}
}

func TestIntegration_ReconnectAfterCrash(t *testing.T) {
	c := connectFake(t, "delta")
	var reconnects atomic.Int32
	c.SetOnReconnect(func() { reconnects.Add(1) })

	if _, err := c.CallTool(context.Background(), "mcp_delta_boom", nil); err != nil {
		t.Fatalf("boom: %v", err)
	}

	// The supervisor waits 1s before the first restart attempt.
	waitFor(t, 25*time.Second, func() bool {
		return c.IsAlive() && reconnects.Load() > 0
	}, "server was not restarted by the supervisor")

	res, err := c.CallTool(context.Background(), "mcp_delta_whoami", nil)
	if err != nil {
		t.Fatalf("call after restart: %v", err)
	}
	if res.Content != "delta" {
		t.Fatalf("after restart got %q, want delta", res.Content)
	}
}

func TestIntegration_ToolsListChangedRefreshesPool(t *testing.T) {
	pool := NewPool()
	t.Cleanup(pool.CloseAll)
	ctx := context.Background()

	var changes atomic.Int32
	pool.SetOnToolsChanged(func(string) { changes.Add(1) })

	if err := pool.Add(ctx, newFakeConfig(fakeServerPath(t), "eps")); err != nil {
		t.Fatalf("add: %v", err)
	}
	before := len(pool.ToolsByServer("eps"))
	if before != 4 {
		t.Fatalf("before = %d tools, want 4", before)
	}

	if _, err := pool.Execute(ctx, "mcp_eps_grow", nil); err != nil {
		t.Fatalf("grow: %v", err)
	}

	waitFor(t, 15*time.Second, func() bool {
		return len(pool.ToolsByServer("eps")) == before+1 && changes.Load() > 0
	}, "list_changed did not refresh the catalog")
}

func TestIntegration_ResourcesAndPrompts(t *testing.T) {
	ctx := context.Background()
	c := connectFake(t, "zeta")

	if !c.Supports("resources") || !c.Supports("prompts") {
		t.Fatalf("capabilities not recorded from initialize: %+v", c.Capabilities())
	}

	resources, err := c.ListResources(ctx)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) != 1 || resources[0].URI != "file:///notes.txt" {
		t.Fatalf("unexpected resources: %+v", resources)
	}

	content, err := c.ReadResource(ctx, "file:///notes.txt")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(content) != 1 || !strings.Contains(content[0].Text, "hello from zeta") {
		t.Fatalf("unexpected resource content: %+v", content)
	}

	prompts, err := c.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Name != "review" || prompts[0].ServerName != "zeta" {
		t.Fatalf("unexpected prompts: %+v", prompts)
	}

	msgs, err := c.GetPrompt(ctx, "review", map[string]any{"path": "internal/mcp"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "internal/mcp") {
		t.Fatalf("unexpected prompt messages: %+v", msgs)
	}
}

func TestIntegration_CloseStopsSupervisor(t *testing.T) {
	c := connectFake(t, "eta")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if c.IsAlive() {
		t.Fatal("client reports alive after Close")
	}
	// A call on a closed client must fail fast, not wait out the 30s timeout.
	start := time.Now()
	res, err := c.CallTool(ctx, "mcp_eta_whoami", nil)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("closed call took %v — it should not block", elapsed)
	}
	if err == nil && res.Success {
		t.Fatalf("expected a failed result on a closed client, got %+v / %v", res, err)
	}
	time.Sleep(2 * time.Second)
	if c.IsAlive() {
		t.Fatal("closed client came back — supervisor must not restart an explicit Close")
	}
}

func TestIntegration_PoolRemoveFreesSlug(t *testing.T) {
	bin := fakeServerPath(t)
	pool := NewPool()
	t.Cleanup(pool.CloseAll)
	ctx := context.Background()

	cfg := newFakeConfig(bin, "theta")
	if err := pool.Add(ctx, cfg); err != nil {
		t.Fatalf("add: %v", err)
	}
	pool.Remove("theta")
	if pool.Has("theta") {
		t.Fatal("still registered after Remove")
	}
	// Re-adding must reuse the original namespace, not theta_2.
	if err := pool.Add(ctx, cfg); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	tools := pool.ToolsByServer("theta")
	if len(tools) == 0 || !strings.HasPrefix(tools[0].Name, "mcp_theta_") {
		t.Fatalf("slug not released on Remove: %+v", toolNames(tools))
	}
}

func toolNames(tools []types.ToolDef) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}
