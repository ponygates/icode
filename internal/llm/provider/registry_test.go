package registry

import (
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// fakeProvider is a minimal types.Provider used to observe what model string
// would be placed on the wire. The embedded interface satisfies the full
// Provider contract; unimplemented methods panic if ever called.
type fakeProvider struct {
	types.Provider
	name   string
	models []types.ModelInfo
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) ListModels() []types.ModelInfo { return f.models }

func TestWireModel(t *testing.T) {
	t.Run("内置模型：无 APIModelID 时 WireModel 即 ID", func(t *testing.T) {
		m := types.ModelInfo{ID: "openrouter/free", Provider: "openrouter"}
		if got := m.WireModel(); got != "openrouter/free" {
			t.Fatalf("WireModel() = %q, want %q", got, "openrouter/free")
		}
	})

	t.Run("自定义模型：复合 ID 的 wire 名是裸 model_id", func(t *testing.T) {
		m := types.ModelInfo{
			ID:         "agnes/agnes-3.0-flash",
			APIModelID: "agnes-3.0-flash",
			Provider:   "agnes",
		}
		if got := m.WireModel(); got != "agnes-3.0-flash" {
			t.Fatalf("WireModel() = %q, want %q", got, "agnes-3.0-flash")
		}
	})
}

func TestResolveModelCustomWireName(t *testing.T) {
	r := NewRegistry()
	agnes := &fakeProvider{name: "agnes"}
	if err := r.Register(agnes); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Mirror what server.registerCustomModel builds from config.yaml:
	// id "agnes/agnes-3.0-flash" + model_id "agnes-3.0-flash".
	r.RegisterCustomModel(types.ModelInfo{
		ID:         "agnes/agnes-3.0-flash",
		Name:       "agnes-3.0-flash",
		Provider:   "agnes",
		APIModelID: "agnes-3.0-flash",
	}, "agnes-3.0-flash")

	t.Run("按复合 ID 解析", func(t *testing.T) {
		p, mi, err := r.ResolveModel("agnes/agnes-3.0-flash")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if p.Name() != "agnes" {
			t.Fatalf("provider = %q, want agnes", p.Name())
		}
		if got := mi.WireModel(); got != "agnes-3.0-flash" {
			t.Fatalf("WireModel() = %q, want agnes-3.0-flash", got)
		}
	})

	t.Run("按裸名 alias 解析也得到相同 wire 名", func(t *testing.T) {
		_, mi, err := r.ResolveModel("agnes-3.0-flash")
		if err != nil {
			t.Fatalf("resolve via alias: %v", err)
		}
		if got := mi.WireModel(); got != "agnes-3.0-flash" {
			t.Fatalf("WireModel() = %q, want agnes-3.0-flash", got)
		}
	})

	t.Run("未注册模型报错", func(t *testing.T) {
		if _, _, err := r.ResolveModel("agnes/does-not-exist"); err == nil {
			t.Fatal("expected error for unknown model")
		}
	})
}
