package openai_compat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestParseVendorModelIDs_OpenAIShape(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`)
	got := parseVendorModelIDs(body)
	want := []string{"gpt-4o", "gpt-4o-mini"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseVendorModelIDs_OllamaShape(t *testing.T) {
	// Ollama's native /api/tags reports "name" rather than "id".
	body := []byte(`{"models":[{"name":"llama3.1:latest"},{"name":"qwen2.5-coder:7b"}]}`)
	got := parseVendorModelIDs(body)
	if len(got) != 2 || got[0] != "llama3.1:latest" || got[1] != "qwen2.5-coder:7b" {
		t.Fatalf("got %v", got)
	}
}

func TestParseVendorModelIDs_BareArray(t *testing.T) {
	got := parseVendorModelIDs([]byte(`["a","b","c"]`))
	if len(got) != 3 || got[2] != "c" {
		t.Fatalf("got %v", got)
	}
}

func TestParseVendorModelIDs_DedupesAndPreservesOrder(t *testing.T) {
	body := []byte(`{"data":[{"id":"b"},{"id":"a"},{"id":"b"},{"id":""}]}`)
	got := parseVendorModelIDs(body)
	if len(got) != 2 {
		t.Fatalf("want 2 unique ids, got %v", got)
	}
	// Vendor order wins over any sorting.
	if got[0] != "b" || got[1] != "a" {
		t.Fatalf("order not preserved: %v", got)
	}
}

func TestParseVendorModelIDs_Garbage(t *testing.T) {
	if got := parseVendorModelIDs([]byte("not json at all")); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
	if got := parseVendorModelIDs([]byte(`{"object":"list"}`)); len(got) != 0 {
		t.Fatalf("want empty for a catalog with no entries, got %v", got)
	}
}

func TestParseVendorModelIDs_ModelAndSlugFallback(t *testing.T) {
	// Some gateways use "model" or "slug" instead of "id"/"name".
	got := parseVendorModelIDs([]byte(`{"data":[{"model":"m1"},{"slug":"s1"}]}`))
	if len(got) != 2 || got[0] != "m1" || got[1] != "s1" {
		t.Fatalf("got %v", got)
	}
}

func TestFetchModels_LiveCatalogue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("unexpected path %q, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want the configured key", got)
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"known-model"},{"id":"brand-new-model"}]}`)
	}))
	defer srv.Close()

	// "known-model" is in the built-in catalogue and must keep its metadata.
	known := types.ModelInfo{
		ID: "known-model", Name: "Known Model", Provider: "fetch-test",
		ContextWindow: 999999, MaxOutputTokens: 4242,
	}
	p := New(Config{
		Name: "fetch-test", APIKey: "sk-test", APIBase: srv.URL,
		Models: []types.ModelInfo{known},
	})

	got, err := p.FetchModels(context.Background())
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 models, got %d (%v)", len(got), got)
	}
	if got[0].ContextWindow != 999999 || got[0].MaxOutputTokens != 4242 {
		t.Errorf("known model lost its catalogue metadata: %+v", got[0])
	}
	if got[1].ID != "brand-new-model" || got[1].Provider != "fetch-test" {
		t.Errorf("new model not shaped correctly: %+v", got[1])
	}
	if got[1].ContextWindow != defaultFetchedContextWindow {
		t.Errorf("new model context = %d, want the conservative default", got[1].ContextWindow)
	}
}

func TestFetchModels_RequiresKey(t *testing.T) {
	p := New(Config{Name: "nokey", APIBase: "http://127.0.0.1:1"})
	if _, err := p.FetchModels(context.Background()); err == nil {
		t.Fatal("expected an error when no API key is configured")
	}
}

func TestFetchModels_SurfacesVendorError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()

	p := New(Config{Name: "badkey", APIKey: "sk-x", APIBase: srv.URL})
	_, err := p.FetchModels(context.Background())
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
}

func TestFetchModels_EmptyCatalogueIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":"list","data":[]}`)
	}))
	defer srv.Close()

	p := New(Config{Name: "empty", APIKey: "sk-x", APIBase: srv.URL})
	if _, err := p.FetchModels(context.Background()); err == nil {
		t.Fatal("an empty catalogue should be reported as an error, not as success with zero models")
	}
}
