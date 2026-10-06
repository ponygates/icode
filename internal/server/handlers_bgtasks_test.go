package server

// Endpoint tests for the desktop Agents view: GET /api/bgtasks (read-only
// listing) and POST /api/bgtasks/{id}/cancel (token-gated mutation).
// Content behaviour (running/cancelled states, tails) is covered by the
// tool package's own tests; here we verify routing, auth and shapes.

import (
	"net/http"
	"testing"
)

func TestBgTasksListEndpoint(t *testing.T) {
	_, base := newSecurityTestServer(t)

	var body struct {
		Tasks []map[string]any `json:"tasks"`
	}
	// Read-only GET keeps loopback trust: no Bearer token needed.
	httpDo(t, http.MethodGet, base+"/api/bgtasks", "", http.StatusOK, &body)
	// The field must exist (JSON null decodes to a nil slice, which is fine —
	// a fresh process has no background tasks yet).
	if body.Tasks == nil {
		t.Fatalf("GET /api/bgtasks missing tasks field")
	}
}

func TestBgTasksListRejectsPost(t *testing.T) {
	_, base := newSecurityTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, base+"/api/bgtasks", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/bgtasks status = %d, want 405", resp.StatusCode)
	}
}

func TestBgTaskCancelRequiresToken(t *testing.T) {
	_, base := newSecurityTestServer(t)

	// No Bearer token → 401, even from loopback (mutating endpoint).
	req, _ := http.NewRequest(http.MethodPost, base+"/api/bgtasks/agt-999/cancel", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cancel without token status = %d, want 401", resp.StatusCode)
	}
}

func TestBgTaskCancelUnknownIDs(t *testing.T) {
	srv, base := newSecurityTestServer(t)

	// Both id families route to their manager and report 404 when the id
	// is unknown — proving the dispatch reached CancelAgentTask /
	// CancelShellTask rather than falling through the generic mux.
	for _, id := range []string{"agt-999999", "bg-999999"} {
		req, _ := http.NewRequest(http.MethodPost, base+"/api/bgtasks/"+id+"/cancel", nil)
		req.Header.Set("Authorization", "Bearer "+srv.APIToken())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do %s: %v", id, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("cancel %s status = %d, want 404", id, resp.StatusCode)
		}
	}
}

func TestBgTaskCancelBadIDShape(t *testing.T) {
	srv, base := newSecurityTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, base+"/api/bgtasks/whatever/cancel", nil)
	req.Header.Set("Authorization", "Bearer "+srv.APIToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cancel bad id status = %d, want 400", resp.StatusCode)
	}
}

func TestBgTaskCancelRejectsGet(t *testing.T) {
	_, base := newSecurityTestServer(t)

	// GET bypasses the token gate (read-only methods pass through), so the
	// handler itself must reject the method.
	resp, err := http.Get(base + "/api/bgtasks/bg-1/cancel")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET cancel status = %d, want 405", resp.StatusCode)
	}
}
