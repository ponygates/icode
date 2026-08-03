package server

import (
	"bufio"
	"context"
	"encoding/json"
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

// ── fake provider / registry for deterministic chat streaming ──

type fakeProvider struct {
	name string
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) ListModels() []types.ModelInfo {
	return []types.ModelInfo{{ID: "openrouter/free", Provider: "openrouter", MaxOutputTokens: 1024}}
}
func (p *fakeProvider) ChatStream(ctx context.Context, req types.ChatRequest) (<-chan types.StreamEvent, error) {
	out := make(chan types.StreamEvent, 16)
	go func() {
		defer close(out)
		out <- types.StreamEvent{Type: types.EventText, Content: "你好，这是来自假 provider 的回复。"}
		out <- types.StreamEvent{Type: types.EventDone, Meta: types.StreamMeta{
			Usage: types.TokenUsage{PromptTokens: 5, CompletionTokens: 12},
		}}
	}()
	return out, nil
}
func (p *fakeProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.Message, error) {
	return &types.Message{Role: types.RoleAssistant, Content: "ok"}, nil
}
func (p *fakeProvider) Health(ctx context.Context) error { return nil }
func (p *fakeProvider) SupportsCache() bool              { return false }

type fakeRegistry struct {
	p *fakeProvider
}

func (r *fakeRegistry) Register(p types.Provider) error         { return nil }
func (r *fakeRegistry) Get(name string) (types.Provider, error) { return r.p, nil }
func (r *fakeRegistry) List() []string                          { return []string{"openrouter"} }
func (r *fakeRegistry) ListAllModels() []types.ModelInfo        { return r.p.ListModels() }
func (r *fakeRegistry) RefreshAll(ctx context.Context) []error  { return nil }
func (r *fakeRegistry) ResolveModel(modelID string) (types.Provider, types.ModelInfo, error) {
	return r.p, types.ModelInfo{ID: "openrouter/free", Provider: "openrouter", MaxOutputTokens: 1024}, nil
}
func (r *fakeRegistry) SetCredentials(name, key, base string) bool          { return true }
func (r *fakeRegistry) SetTimeout(name string, sec int) bool                { return true }
func (r *fakeRegistry) RegisterCustomModel(m types.ModelInfo, alias string) {}
func (r *fakeRegistry) RemoveCustomModel(canonicalID string)                {}
func (r *fakeRegistry) Deregister(name string)                              {}

// TestChatStreamIntegration reproduces the exact desktop failure path:
//  1. pre-populate the store with sessions (so List() returns rows),
//  2. call GET /api/sessions (List) — this is what used to deadlock and
//     silently block the single SQLite connection,
//  3. call POST /api/chat — must stream a "text" event + "done" event.
//
// If the List() deadlock were still present, step 2 would hang and step 3
// would never return a reply (matching the user's "no reply" symptom).
func TestChatStreamIntegration(t *testing.T) {
	// In-memory SQLite store (unique path so we don't touch the real db).
	store, err := db.New(db.Config{Path: fmt.Sprintf("file::memory:?cache=shared&_conn=%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	reg := &fakeRegistry{p: &fakeProvider{name: "openrouter"}}
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

	// Step 1: create two sessions with messages (so List returns rows).
	for i := 0; i < 2; i++ {
		sid := fmt.Sprintf("seed-%d", i)
		_ = store.Create(&types.Session{ID: sid, Title: "seed", ModelID: "openrouter/free", ProviderName: "openrouter"})
		_ = store.AppendMessage(sid, types.Message{Role: types.RoleUser, Content: "hi", Timestamp: time.Now()})
		_ = store.AppendMessage(sid, types.Message{Role: types.RoleAssistant, Content: "hello", Timestamp: time.Now()})
	}

	// Step 2: List (the old deadlock point). Must return quickly.
	listDone := make(chan error, 1)
	go func() {
		resp, e := http.Get(base + "/api/sessions")
		if e != nil {
			listDone <- e
			return
		}
		defer resp.Body.Close()
		var list []types.Session
		e = json.NewDecoder(resp.Body).Decode(&list)
		if e != nil {
			listDone <- e
			return
		}
		if len(list) != 2 {
			listDone <- fmt.Errorf("expected 2 sessions, got %d", len(list))
			return
		}
		listDone <- nil
	}()
	select {
	case e := <-listDone:
		if e != nil {
			t.Fatalf("List() failed: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("List() deadlocked — the SQLite nested-query bug is NOT fixed")
	}

	// Step 3: chat. Must stream a reply.
	resp, err := http.Post(base+"/api/chat", "application/json",
		strings.NewReader(`{"session_id":"chat-test","content":"你好","model":"openrouter/free","provider":"openrouter"}`))
	if err != nil {
		t.Fatalf("post chat: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	gotText := false
	gotDone := false
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("chat stream timed out — no reply received (reproduces user symptom)")
		default:
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var ev types.StreamEvent
		if e := json.Unmarshal([]byte(payload), &ev); e != nil {
			continue
		}
		if ev.Type == types.EventText && ev.Content != "" {
			gotText = true
		}
		if ev.Type == types.EventDone {
			gotDone = true
		}
		if gotText && gotDone {
			return
		}
	}
	if !gotText {
		t.Fatal("chat stream produced no text event — no reply")
	}
	if !gotDone {
		t.Fatal("chat stream never sent done event")
	}
}
