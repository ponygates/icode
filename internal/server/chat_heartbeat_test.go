package server

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/types"
)

// ── a provider that stays silent for a while — the shape of a long tool round ──

type stallProvider struct {
	stall time.Duration
}

func (p *stallProvider) Name() string { return "stall" }

func (p *stallProvider) ListModels() []types.ModelInfo {
	return []types.ModelInfo{{ID: "stall/model", Provider: "stall", MaxOutputTokens: 1024}}
}

func (p *stallProvider) ChatStream(ctx context.Context, req types.ChatRequest) (<-chan types.StreamEvent, error) {
	out := make(chan types.StreamEvent, 8)
	go func() {
		defer close(out)
		// The quiet stretch that a long `go test` / `npm build` round produces.
		select {
		case <-time.After(p.stall):
		case <-ctx.Done():
			return
		}
		out <- types.StreamEvent{Type: types.EventText, Content: "late reply"}
		out <- types.StreamEvent{Type: types.EventDone}
	}()
	return out, nil
}

func (p *stallProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.Message, error) {
	return &types.Message{Role: types.RoleAssistant, Content: "ok"}, nil
}

func (p *stallProvider) Health(ctx context.Context) error { return nil }

func (p *stallProvider) SupportsCache() bool { return false }

type stallRegistry struct{ p *stallProvider }

func (r *stallRegistry) Register(p types.Provider) error         { return nil }
func (r *stallRegistry) Get(name string) (types.Provider, error) { return r.p, nil }
func (r *stallRegistry) List() []string                          { return []string{"stall"} }
func (r *stallRegistry) ListAllModels() []types.ModelInfo        { return r.p.ListModels() }
func (r *stallRegistry) RefreshAll(ctx context.Context) []error  { return nil }
func (r *stallRegistry) ResolveModel(modelID string) (types.Provider, types.ModelInfo, error) {
	return r.p, types.ModelInfo{ID: "stall/model", Provider: "stall", MaxOutputTokens: 1024}, nil
}
func (r *stallRegistry) SetCredentials(name, key, base string) bool          { return true }
func (r *stallRegistry) SetTimeout(name string, sec int) bool                { return true }
func (r *stallRegistry) RegisterCustomModel(m types.ModelInfo, alias string) {}
func (r *stallRegistry) RemoveCustomModel(canonicalID string)                {}
func (r *stallRegistry) Deregister(name string)                              {}

// TestChatStreamHeartbeat proves the SSE stream keeps the connection warm
// while the engine is busy and has nothing to report. Without the keepalive a
// long tool round leaves the socket byte-silent, which intermediaries drop and
// the desktop UI cannot distinguish from a dead connection.
func TestChatStreamHeartbeat(t *testing.T) {
	// Shorten the interval — the production value is 15s, far too slow to test.
	orig := sseHeartbeatInterval
	sseHeartbeatInterval = 40 * time.Millisecond
	defer func() { sseHeartbeatInterval = orig }()

	store, err := db.New(db.Config{Path: fmt.Sprintf("file::memory:?cache=shared&_hb=%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	reg := &stallRegistry{p: &stallProvider{stall: 500 * time.Millisecond}}
	engine := conversation.NewEngine(reg, store, nil)

	cfg := config.Default()
	srv := New(ServerConfig{
		Config:   cfg,
		Registry: reg,
		Store:    store,
		DB:       store,
		Engine:   engine,
		Version:  "test",
		Port:     0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port, err := srv.Start(ctx)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	resp, err := http.Post(base+"/api/chat", "application/json",
		strings.NewReader(`{"session_id":"hb-test","content":"hi","model":"stall/model","provider":"stall"}`))
	if err != nil {
		t.Fatalf("post chat: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	gotPing := false
	gotEvent := false
	deadline := time.After(10 * time.Second)

	for !gotPing {
		select {
		case <-deadline:
			t.Fatalf("no heartbeat frame within the deadline (ping=%v event=%v)", gotPing, gotEvent)
		default:
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// A keepalive is an SSE comment: it starts with ':' and carries no
		// "data:" payload, so EventSource ignores it.
		if strings.HasPrefix(trimmed, ":") {
			gotPing = true
			break
		}
		if strings.HasPrefix(trimmed, "data:") {
			gotEvent = true
		}
	}

	if !gotPing {
		t.Fatal("the chat SSE stream emitted no keepalive frame while idle — a long tool round would look like a dead connection")
	}
}
