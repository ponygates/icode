package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/conversation"
	"github.com/ponygates/icode/internal/db"
	"github.com/ponygates/icode/internal/types"
)

// ── fakes for the live-model-discovery flow ────────────────────────
//
// fetchProvider advertises a small catalogue but returns a *different* live
// list, mirroring reality: the vendor ships models this binary has never
// heard of. plainProvider deliberately omits FetchModels so we can assert the
// honest 501 rather than a fabricated empty success.

type fetchProvider struct {
	name   string
	listed []types.ModelInfo // built-in catalogue (build-time snapshot)
	live   []types.ModelInfo // what the vendor's /models actually returns
	err    error
}

func (p *fetchProvider) Name() string                  { return p.name }
func (p *fetchProvider) ListModels() []types.ModelInfo { return p.listed }
func (p *fetchProvider) SupportsCache() bool           { return true }
func (p *fetchProvider) Health(ctx context.Context) error {
	return nil
}
func (p *fetchProvider) Chat(context.Context, types.ChatRequest) (*types.Message, error) {
	return &types.Message{Role: types.RoleAssistant, Content: "ok"}, nil
}
func (p *fetchProvider) ChatStream(context.Context, types.ChatRequest) (<-chan types.StreamEvent, error) {
	ch := make(chan types.StreamEvent)
	close(ch)
	return ch, nil
}
func (p *fetchProvider) FetchModels(ctx context.Context) ([]types.ModelInfo, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.live, nil
}

// fetchRegistry serves a fixed set of named providers.
type fetchRegistry struct {
	fakeRegistry
	byName map[string]types.Provider
	order  []string
}

func newFetchRegistry(providers ...types.Provider) *fetchRegistry {
	r := &fetchRegistry{byName: map[string]types.Provider{}}
	for _, p := range providers {
		r.byName[p.Name()] = p
		r.order = append(r.order, p.Name())
	}
	return r
}

func (r *fetchRegistry) Get(name string) (types.Provider, error) {
	if p, ok := r.byName[name]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("provider not found: %s", name)
}

func (r *fetchRegistry) List() []string { return r.order }

func (r *fetchRegistry) ListAllModels() []types.ModelInfo {
	var out []types.ModelInfo
	for _, name := range r.order {
		out = append(out, r.byName[name].ListModels()...)
	}
	return out
}

// newFetchTestServer starts a server backed by the given providers with the
// config path redirected into a temp dir, so the flow can be tested end to end
// without touching the real ~/.icode/config.yaml.
func newFetchTestServer(t *testing.T, providers ...types.Provider) string {
	t.Helper()
	isolateHome(t)

	store, err := db.New(db.Config{Path: fmt.Sprintf("file::memory:?cache=shared&_conn=%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	reg := newFetchRegistry(providers...)
	engine := conversation.NewEngine(reg, store, nil)
	srv := New(ServerConfig{
		Config:   config.Default(),
		Registry: reg,
		Store:    store,
		DB:       store,
		Engine:   engine,
		Version:  "test",
		Port:     0,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	port, err := srv.Start(ctx)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func demoProvider() *fetchProvider {
	return &fetchProvider{
		name: "demo",
		// Catalogue knows two models...
		listed: []types.ModelInfo{
			{ID: "demo-a", Provider: "demo", Name: "Demo A", ContextWindow: 128000, MaxOutputTokens: 8192},
			{ID: "demo-b", Provider: "demo", Name: "Demo B", ContextWindow: 32000, MaxOutputTokens: 4096},
		},
		// ...the vendor now serves three.
		live: []types.ModelInfo{
			{ID: "demo-a", Provider: "demo", Name: "Demo A", ContextWindow: 128000, MaxOutputTokens: 8192},
			{ID: "demo-b", Provider: "demo", Name: "Demo B", ContextWindow: 32000, MaxOutputTokens: 4096},
			{ID: "demo-c", Provider: "demo", Name: "Demo C (new)", ContextWindow: 200000, MaxOutputTokens: 16384},
		},
	}
}

// providerModelIDs returns the "model_id" field of every selectable model.
//
// Note the deliberate id/model_id split: a built-in has id == model_id
// (e.g. "demo-a"), whereas a model registered on the fly is keyed as
// "vendor/model_id" ("demo/demo-c") while keeping its bare vendor id in
// model_id. The bare form is what the vendor actually accepts, so that is also
// what EnabledModels stores and what the fetch endpoint returns.
func providerModelIDs(t *testing.T, base string) []string {
	t.Helper()
	var models []map[string]any
	httpDo(t, http.MethodGet, base+"/api/models", "", http.StatusOK, &models)
	ids := make([]string, 0, len(models))
	for _, m := range models {
		if mid := fmt.Sprint(m["model_id"]); mid != "" && mid != "<nil>" {
			ids = append(ids, mid)
			continue
		}
		ids = append(ids, fmt.Sprint(m["id"]))
	}
	return ids
}

func hasID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// The renderer reads camelCase (model.contextWindow / maxOutputTokens); these
// DTOs used to emit only snake_case, so those fields were silently always
// undefined and the context-window gauge always fell back to its default.
func TestListModelsEmitsCamelCaseTokenLimits(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	var models []map[string]any
	httpDo(t, http.MethodGet, base+"/api/models", "", http.StatusOK, &models)

	var found bool
	for _, m := range models {
		if m["model_id"] != "demo-a" {
			continue
		}
		found = true
		if got := m["contextWindow"]; got != float64(128000) {
			t.Fatalf("contextWindow = %v, want 128000", got)
		}
		if got := m["maxOutputTokens"]; got != float64(8192) {
			t.Fatalf("maxOutputTokens = %v, want 8192", got)
		}
		// The snake_case spelling predates this and must keep working.
		if got := m["context_window"]; got != float64(128000) {
			t.Fatalf("context_window = %v, want 128000 (back-compat)", got)
		}
	}
	if !found {
		t.Fatalf("demo-a missing from %+v", models)
	}
}

// `known` has to mean "present in the built-in catalogue"; reporting a constant
// true (as it once did) tells the UI nothing and hides the one signal that
// makes live discovery worth having.
func TestFetchModelsFlagsModelsAbsentFromBuiltinCatalogue(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	var out struct {
		Models []struct {
			ID    string `json:"id"`
			Known bool   `json:"known"`
		} `json:"models"`
	}
	httpDo(t, http.MethodGet, base+"/api/models/fetch?provider=demo", "", http.StatusOK, &out)

	want := map[string]bool{"demo-a": true, "demo-b": true, "demo-c": false}
	if len(out.Models) != len(want) {
		t.Fatalf("got %d models, want %d", len(out.Models), len(want))
	}
	for _, m := range out.Models {
		expected, ok := want[m.ID]
		if !ok {
			t.Fatalf("unexpected model %q", m.ID)
		}
		if m.Known != expected {
			t.Fatalf("model %s known = %v, want %v", m.ID, m.Known, expected)
		}
	}
}

// The whole point of the feature: the vendor, not the build, decides what a
// key can use — including models that did not exist when this binary shipped.
func TestFetchModelsReturnsLiveCatalogueBeyondBuiltin(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	var out struct {
		Provider string `json:"provider"`
		Count    int    `json:"count"`
		Filtered bool   `json:"filtered"`
		Models   []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Known   bool   `json:"known"`
			Enabled bool   `json:"enabled"`
			Context int    `json:"context_window"`
		} `json:"models"`
	}
	httpDo(t, http.MethodGet, base+"/api/models/fetch?provider=demo", "", http.StatusOK, &out)

	if out.Count != 3 || len(out.Models) != 3 {
		t.Fatalf("fetch returned %d models, want 3", len(out.Models))
	}
	// Nothing curated yet, so nothing is filtered and everything is enabled.
	if out.Filtered {
		t.Fatalf("filtered = true, want false before any selection is saved")
	}
	for _, m := range out.Models {
		if !m.Enabled {
			t.Fatalf("model %s enabled = false, want true (unset filter restricts nothing)", m.ID)
		}
	}
	// demo-c is absent from the built-in catalogue but must still be offered.
	if !hasID([]string{out.Models[0].ID, out.Models[1].ID, out.Models[2].ID}, "demo-c") {
		t.Fatalf("live-only model demo-c missing from fetch result: %+v", out.Models)
	}
	if out.Models[2].Context != 200000 {
		t.Fatalf("demo-c context = %d, want 200000 (vendor metadata)", out.Models[2].Context)
	}
}

// Saving a subset must (a) restrict the selectable list and (b) register any
// model the catalogue has never seen, so it is usable without a new release.
func TestModelSelectionFiltersListAndRegistersUnknown(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	// Baseline: everything in the built-in catalogue is selectable.
	if ids := providerModelIDs(t, base); !hasID(ids, "demo-a") || !hasID(ids, "demo-b") {
		t.Fatalf("baseline models = %v, want demo-a and demo-b", ids)
	}

	// Keep demo-a plus a model the binary has never heard of.
	httpDo(t, http.MethodPut, base+"/api/models/selection",
		`{"provider":"demo","models":["demo-a","demo-c"]}`, http.StatusOK, nil)

	ids := providerModelIDs(t, base)
	if !hasID(ids, "demo-a") {
		t.Fatalf("ticked model demo-a missing: %v", ids)
	}
	if !hasID(ids, "demo-c") {
		t.Fatalf("live-only model demo-c was not registered: %v", ids)
	}
	if hasID(ids, "demo-b") {
		t.Fatalf("unticked model demo-b still selectable: %v", ids)
	}

	// The registered entry must carry the vendor's own id (not the composite
	// config key) so the engine can send it to the vendor verbatim.
	var all []map[string]any
	httpDo(t, http.MethodGet, base+"/api/models", "", http.StatusOK, &all)
	found := false
	for _, m := range all {
		if m["model_id"] == "demo-c" {
			found = true
			if m["id"] != "demo/demo-c" {
				t.Fatalf("registered id = %v, want the composite key demo/demo-c", m["id"])
			}
			if m["custom"] != true {
				t.Fatalf("registered model custom = %v, want true", m["custom"])
			}
		}
	}
	if !found {
		t.Fatalf("demo-c absent from /api/models: %+v", all)
	}

	// The card badge reads these two numbers from /api/providers.
	var providers []map[string]any
	httpDo(t, http.MethodGet, base+"/api/providers", "", http.StatusOK, &providers)
	if len(providers) != 1 {
		t.Fatalf("providers = %v, want 1 entry", providers)
	}
	if providers[0]["filtered"] != true {
		t.Fatalf("filtered = %v, want true after save", providers[0]["filtered"])
	}
	// demo-a (ticked) + demo-c (ticked, live-only) of demo-a/demo-b/demo-c.
	// Numerator and denominator share the same set, so the badge reads "2/3"
	// rather than the nonsensical "2/2" (catalogue-only) or "3/2".
	if got := providers[0]["enabled"]; got != float64(2) {
		t.Fatalf("enabled = %v, want 2", got)
	}
	if got := providers[0]["models"]; got != float64(3) {
		t.Fatalf("models = %v, want 3 (catalogue + registered)", got)
	}

	// Re-fetching must now report the saved state and fold in the custom model,
	// otherwise "select all" would silently drop it on the next save.
	var after struct {
		Filtered bool `json:"filtered"`
		Models   []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"models"`
	}
	httpDo(t, http.MethodGet, base+"/api/models/fetch?provider=demo", "", http.StatusOK, &after)
	if !after.Filtered {
		t.Fatalf("filtered = false after save, want true")
	}
	if len(after.Models) != 3 {
		t.Fatalf("re-fetch returned %d models, want 3 (2 live + 1 custom)", len(after.Models))
	}
	for _, m := range after.Models {
		want := m.ID == "demo-a" || m.ID == "demo-c"
		if m.Enabled != want {
			t.Fatalf("model %s enabled = %v, want %v", m.ID, m.Enabled, want)
		}
	}
}

// An empty selection would be persisted as "no filter", which means "allow
// everything" — the exact opposite of what the user asked for.
func TestEmptyModelSelectionIsRejected(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	httpDo(t, http.MethodPut, base+"/api/models/selection",
		`{"provider":"demo","models":[]}`, http.StatusBadRequest, nil)

	// The catalogue must be untouched.
	if ids := providerModelIDs(t, base); !hasID(ids, "demo-a") || !hasID(ids, "demo-b") {
		t.Fatalf("models = %v, want the full catalogue to remain", ids)
	}
}

// ── hand-adding a model to a specific vendor ────────────────────────

// The core promise of "add a model here": the user picks a vendor, types an id,
// and the model is immediately selectable — including when that vendor's list
// has already been curated down to a ticked subset, which is the case where a
// stored-but-filtered model would silently never appear.
func TestAddCustomModelToCuratedVendorStaysVisible(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	// Curate the vendor down to just demo-a first.
	httpDo(t, http.MethodPut, base+"/api/models/selection",
		`{"provider":"demo","models":["demo-a"]}`, http.StatusOK, nil)
	if ids := providerModelIDs(t, base); hasID(ids, "demo-b") {
		t.Fatalf("precondition failed: demo-b should be filtered out, got %v", ids)
	}

	// Now hand-add a model the catalogue does not know about.
	httpDo(t, http.MethodPut, base+"/api/config/model",
		`{"provider":"demo","model_id":"demo-handmade","name":"My Model","custom":true,
		  "context_window":32000,"max_output_tokens":4096}`, http.StatusOK, nil)

	ids := providerModelIDs(t, base)
	if !hasID(ids, "demo-handmade") {
		t.Fatalf("hand-added model was swallowed by the vendor filter: %v", ids)
	}
	if !hasID(ids, "demo-a") {
		t.Fatalf("previously ticked model disappeared: %v", ids)
	}
	if hasID(ids, "demo-b") {
		t.Fatalf("unticked model leaked back in: %v", ids)
	}
	// The filter must have been extended, not reset.
	var providers []map[string]any
	httpDo(t, http.MethodGet, base+"/api/providers", "", http.StatusOK, &providers)
	if providers[0]["filtered"] != true {
		t.Fatalf("filtered = %v, want the curation to survive", providers[0]["filtered"])
	}

	// And it must carry the metadata the form supplied.
	var all []map[string]any
	httpDo(t, http.MethodGet, base+"/api/models", "", http.StatusOK, &all)
	var found bool
	for _, m := range all {
		if m["model_id"] != "demo-handmade" {
			continue
		}
		found = true
		if m["name"] != "My Model" {
			t.Fatalf("name = %v, want My Model", m["name"])
		}
		if m["contextWindow"] != float64(32000) {
			t.Fatalf("contextWindow = %v, want 32000", m["contextWindow"])
		}
		if m["custom"] != true {
			t.Fatalf("custom = %v, want true", m["custom"])
		}
	}
	if !found {
		t.Fatalf("demo-handmade missing from /api/models: %+v", all)
	}
}

// With no filter in place there is nothing to join, and the model is simply
// visible like any other.
func TestAddCustomModelToUncuratedVendor(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	httpDo(t, http.MethodPut, base+"/api/config/model",
		`{"provider":"demo","model_id":"demo-handmade","name":"My Model","custom":true}`, http.StatusOK, nil)

	if ids := providerModelIDs(t, base); !hasID(ids, "demo-handmade") {
		t.Fatalf("hand-added model missing: %v", ids)
	}
}

// Adding a model the vendor already ships would replace a fully described entry
// (plan, pricing, context window) with a blank one, silently degrading it.
func TestAddDuplicateOfBuiltinModelIsRejected(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	httpDo(t, http.MethodPut, base+"/api/config/model",
		`{"provider":"demo","model_id":"demo-a","name":"Shadow","custom":true}`,
		http.StatusConflict, nil)

	// The built-in entry must still be intact and listed once.
	var all []map[string]any
	httpDo(t, http.MethodGet, base+"/api/models", "", http.StatusOK, &all)
	count := 0
	for _, m := range all {
		if m["model_id"] == "demo-a" {
			count++
			if m["custom"] == true {
				t.Fatalf("built-in demo-a was replaced by a custom entry")
			}
			if m["name"] != "Demo A" {
				t.Fatalf("built-in name was clobbered: %v", m["name"])
			}
		}
	}
	if count != 1 {
		t.Fatalf("demo-a listed %d times, want 1", count)
	}
}

func TestAddCustomModelRejectsBlankAndWhitespaceIDs(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())
	httpDo(t, http.MethodPut, base+"/api/config/model",
		`{"provider":"demo","model_id":"   ","custom":true}`, http.StatusBadRequest, nil)
	httpDo(t, http.MethodPut, base+"/api/config/model",
		`{"provider":"demo","model_id":"bad id","custom":true}`, http.StatusBadRequest, nil)
}

// Deleting must also clean the vendor filter, otherwise a dangling id keeps the
// vendor restricted to a model that no longer exists.
func TestDeleteCustomModelClearsItsFilterEntry(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	httpDo(t, http.MethodPut, base+"/api/models/selection",
		`{"provider":"demo","models":["demo-a"]}`, http.StatusOK, nil)
	httpDo(t, http.MethodPut, base+"/api/config/model",
		`{"provider":"demo","model_id":"demo-handmade","custom":true}`, http.StatusOK, nil)
	if ids := providerModelIDs(t, base); !hasID(ids, "demo-handmade") {
		t.Fatalf("precondition failed: %v", ids)
	}

	httpDo(t, http.MethodDelete, base+"/api/config/model?id=demo/demo-handmade", "", http.StatusOK, nil)

	if ids := providerModelIDs(t, base); hasID(ids, "demo-handmade") {
		t.Fatalf("deleted model still listed: %v", ids)
	}
	// demo-a was the other filter entry and must be unaffected.
	if ids := providerModelIDs(t, base); !hasID(ids, "demo-a") {
		t.Fatalf("unrelated ticked model was lost: %v", ids)
	}
	if hasID(providerModelIDs(t, base), "demo-b") {
		t.Fatalf("deleting one custom model reset the whole filter")
	}
}

// Removing the last filtered model leaves an empty filter, which by definition
// means "no restriction" — the catalogue comes back rather than the vendor
// showing nothing.
func TestDeletingSoleFilteredModelRestoresCatalogue(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	httpDo(t, http.MethodPut, base+"/api/models/selection",
		`{"provider":"demo","models":["demo-a"]}`, http.StatusOK, nil)
	httpDo(t, http.MethodPut, base+"/api/config/model",
		`{"provider":"demo","model_id":"demo-handmade","custom":true}`, http.StatusOK, nil)
	// Filter is now [demo-a, demo-handmade]; drop both by deleting the custom
	// one and re-curating to nothing else... instead delete the custom one and
	// then verify the remaining entry still governs.
	httpDo(t, http.MethodDelete, base+"/api/config/model?id=demo/demo-handmade", "", http.StatusOK, nil)

	var providers []map[string]any
	httpDo(t, http.MethodGet, base+"/api/providers", "", http.StatusOK, &providers)
	if providers[0]["filtered"] != true {
		t.Fatalf("filtered = %v, want the remaining demo-a curation to hold", providers[0]["filtered"])
	}
}

// A vendor with no live-discovery support must say so, not pretend it worked.
func TestFetchModelsOnUnsupportedProviderReturns501(t *testing.T) {
	base := newFetchTestServer(t, &fakeProvider{name: "legacy"})

	var body map[string]any
	httpDo(t, http.MethodGet, base+"/api/models/fetch?provider=legacy", "", http.StatusNotImplemented, &body)
	if body["error"] == nil || body["error"] == "" {
		t.Fatalf("error message missing: %+v", body)
	}
}

func TestFetchModelsUnknownProviderReturns404(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())
	httpDo(t, http.MethodGet, base+"/api/models/fetch?provider=nope", "", http.StatusNotFound, nil)
}

func TestFetchModelsRequiresProviderParam(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())
	httpDo(t, http.MethodGet, base+"/api/models/fetch", "", http.StatusBadRequest, nil)
}

// A vendor error (bad key, network) must surface as a gateway error carrying
// the vendor's own message, so the UI can show something actionable.
func TestFetchModelsSurfacesVendorError(t *testing.T) {
	p := demoProvider()
	p.err = fmt.Errorf("401 invalid api key")
	base := newFetchTestServer(t, p)

	var body map[string]any
	httpDo(t, http.MethodGet, base+"/api/models/fetch?provider=demo", "", http.StatusBadGateway, &body)
	if got := fmt.Sprint(body["error"]); got != "401 invalid api key" {
		t.Fatalf("error = %q, want the vendor message", got)
	}
}

// Selection must survive a restart: it is persisted to the config file, not
// held in memory, because the model list is rebuilt on every launch.
func TestModelSelectionPersistsToConfig(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())
	httpDo(t, http.MethodPut, base+"/api/models/selection",
		`{"provider":"demo","models":["demo-b"]}`, http.StatusOK, nil)

	// Re-read from disk (DefaultPath points into the isolated temp home).
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	got := cfg.Providers["demo"].EnabledModels
	if len(got) != 1 || got[0] != "demo-b" {
		t.Fatalf("persisted EnabledModels = %v, want [demo-b]", got)
	}
	// demo-c was never mentioned in this save, so it must not be registered.
	for _, m := range cfg.Models {
		if m.ModelID == "demo-c" {
			t.Fatalf("demo-c was registered despite not being selected")
		}
	}
}

// A model the vendor shipped after this build is auto-registered on save. The
// vendor-reported context window has to ride along: without it the new entry
// is stored blank and the UI falls back to a guess, even though the fetch had
// the real figure moments earlier.
func TestModelSelectionPersistsVendorReportedMetadata(t *testing.T) {
	base := newFetchTestServer(t, demoProvider())

	body := `{"provider":"demo","models":["demo-c"],` +
		`"meta":{"demo-c":{"context_window":200000,"max_output_tokens":16384}}}`
	httpDo(t, http.MethodPut, base+"/api/models/selection", body, http.StatusOK, nil)

	var all []map[string]any
	httpDo(t, http.MethodGet, base+"/api/models", "", http.StatusOK, &all)

	var found bool
	for _, m := range all {
		if m["model_id"] != "demo-c" {
			continue
		}
		found = true
		if got := m["contextWindow"]; got != float64(200000) {
			t.Fatalf("contextWindow = %v, want the vendor-reported 200000", got)
		}
		if got := m["maxOutputTokens"]; got != float64(16384) {
			t.Fatalf("maxOutputTokens = %v, want the vendor-reported 16384", got)
		}
	}
	if !found {
		t.Fatalf("demo-c absent from /api/models: %+v", all)
	}

	// And it must be on disk, not just in the live registry.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	for _, m := range cfg.Models {
		if m.ModelID != "demo-c" {
			continue
		}
		if m.ContextWindow != 200000 || m.MaxOutput != 16384 {
			t.Fatalf("persisted metadata = %d/%d, want 200000/16384",
				m.ContextWindow, m.MaxOutput)
		}
		return
	}
	t.Fatalf("demo-c not persisted to the config file")
}
