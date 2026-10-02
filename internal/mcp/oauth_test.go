package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ponygates/icode/internal/netsec"
)

// The whole suite runs on net/http/httptest loopback servers only — no request
// ever leaves the machine, and nothing talks to a real vendor or auth provider.

// testSeal / testOpen are a stand-in for secure.Encrypt that still proves the
// "plaintext never reaches disk" property: the file stores an "enc:"-prefixed
// base64 blob, so the raw token is not a substring of what lands on disk.
func testSeal(s string) (string, error) {
	return "enc:" + base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func testOpen(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "enc:"))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// fakeAS is a scripted authorization server: discovery, DCR, token exchange,
// with counters and captured request bodies for assertions.
type fakeAS struct {
	srv         *httptest.Server
	metadataHit atomic.Int32
	tokenHit    atomic.Int32
	regHit      atomic.Int32

	mu            sync.Mutex
	regBody       dcrRequest
	capturedChall string
	capturedState string

	// tokenHandler overrides the /token reply when set (for refresh tests).
	tokenHandler func(w http.ResponseWriter, r *http.Request)
}

func newFakeAS(t *testing.T) *fakeAS {
	t.Helper()
	as := &fakeAS{}
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	serveMeta := func(w http.ResponseWriter, r *http.Request) {
		as.metadataHit.Add(1)
		writeJSON(w, asMetadata{
			Issuer:                as.srv.URL,
			AuthorizationEndpoint: as.srv.URL + "/authorize",
			TokenEndpoint:         as.srv.URL + "/token",
			RegistrationEndpoint:  as.srv.URL + "/register",
		})
	}
	mux.HandleFunc("/.well-known/oauth-authorization-server", serveMeta)
	mux.HandleFunc("/.well-known/openid-configuration", serveMeta)
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		as.regHit.Add(1)
		var req dcrRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		as.mu.Lock()
		as.regBody = req
		as.mu.Unlock()
		writeJSON(w, dcrResponse{ClientID: "client-abc"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		as.tokenHit.Add(1)
		if as.tokenHandler != nil {
			as.tokenHandler(w, r)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" {
			http.Error(w, "unsupported grant", http.StatusBadRequest)
			return
		}
		// PKCE correctness: the verifier must hash to the challenge the client
		// put on the authorize URL. Reject otherwise — this is what makes the
		// "challenge = S256(verifier)" assertion meaningful end-to-end.
		sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		want := base64.RawURLEncoding.EncodeToString(sum[:])
		as.mu.Lock()
		captured := as.capturedChall
		as.mu.Unlock()
		if captured == "" || want != captured {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"access_token":  "ACCESS-" + r.FormValue("code"),
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "REFRESH-rt",
		})
	})
	as.srv = httptest.NewServer(mux)
	t.Cleanup(as.srv.Close)
	return as
}

// browserHook returns an openBrowser func that reads the authorize URL's state
// and code_challenge, then completes the loop the way a user's browser would.
// overrideState, when non-empty, is sent instead of the real state (CSRF test).
func (as *fakeAS) browserHook(t *testing.T, overrideState, overrideCode string) func(string) error {
	return func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		as.mu.Lock()
		as.capturedChall = q.Get("code_challenge")
		as.capturedState = q.Get("state")
		as.mu.Unlock()
		if got := q.Get("code_challenge_method"); got != "S256" {
			t.Errorf("authorize URL code_challenge_method = %q, want S256", got)
		}
		state := q.Get("state")
		if overrideState != "" {
			state = overrideState
		}
		code := "AUTHCODE"
		if overrideCode != "" {
			code = overrideCode
		}
		cb := q.Get("redirect_uri") + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
		// Fire the callback in the background; the flow is blocked waiting on it.
		go func() { _, _ = http.Get(cb) }()
		return nil
	}
}

func newTestManager(t *testing.T, as *fakeAS) *OAuthManager {
	t.Helper()
	return newTestManagerOpts(t, as, oauthOptions{})
}

func newTestManagerOpts(t *testing.T, as *fakeAS, base oauthOptions) *OAuthManager {
	t.Helper()
	if base.server == "" {
		base.server = "srv"
	}
	if base.resourceURL == "" {
		base.resourceURL = "http://127.0.0.1:9/mcp"
	}
	if base.storeDir == "" {
		base.storeDir = t.TempDir()
	}
	if base.seal == nil {
		base.seal = testSeal
	}
	if base.open == nil {
		base.open = testOpen
	}
	if base.httpClient == nil {
		base.httpClient = &http.Client{} // plain: allowed to dial loopback httptest
	}
	if base.validate == nil {
		base.validate = netsec.ValidateConfiguredURL // loopback ok, 169.254 blocked
	}
	if base.isTTY == nil {
		base.isTTY = func() bool { return true }
	}
	if base.now == nil {
		base.now = time.Now
	}
	return newOAuthManager(base)
}

func challenge(as *fakeAS) oauthChallenge {
	return oauthChallenge{authorizationServer: as.srv.URL, scope: "read write"}
}

// 1. 401 challenge parsing.
func TestParseWWWAuthenticate(t *testing.T) {
	h := `Bearer error="invalid_token", error_description="missing token", scope="read write", authorization_server="https://auth.example.com", resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`
	ch := parseWWWAuthenticate(h)
	if ch.authorizationServer != "https://auth.example.com" {
		t.Errorf("authorization_server = %q", ch.authorizationServer)
	}
	if ch.resourceMetadata != "https://mcp.example.com/.well-known/oauth-protected-resource" {
		t.Errorf("resource_metadata = %q", ch.resourceMetadata)
	}
	if ch.scope != "read write" {
		t.Errorf("scope = %q", ch.scope)
	}
	if ch.errorHint != "invalid_token" {
		t.Errorf("error = %q", ch.errorHint)
	}
	// A scheme-only header yields an empty (but non-panicking) challenge.
	if got := parseWWWAuthenticate("Bearer"); got.scope != "" || got.authorizationServer != "" {
		t.Errorf("scheme-only header parsed to %+v", got)
	}
	// An unquoted value must survive too.
	if got := parseWWWAuthenticate(`Bearer realm=mcp, scope=openid`); got.scope != "openid" {
		t.Errorf("unquoted scope = %q", got.scope)
	}
}

// 2. PKCE math + state randomness.
func TestPKCEPrimitives(t *testing.T) {
	v, err := newCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) < 43 || len(v) > 128 {
		t.Fatalf("verifier length %d out of RFC 7636 range", len(v))
	}
	sum := sha256.Sum256([]byte(v))
	if got, want := codeChallenge(v), base64.RawURLEncoding.EncodeToString(sum[:]); got != want {
		t.Errorf("codeChallenge not S256(verifier): %q != %q", got, want)
	}
	// Challenge must not equal the verifier (plain would be the bug).
	if codeChallenge(v) == v {
		t.Error("challenge equals verifier — not S256")
	}
	if a, b := newState(), newState(); a == b {
		t.Error("two states collided — state must be random")
	}
	if len(newState()) != 32 {
		t.Error("state should be 32 hex chars")
	}
}

// 3a. Metadata discovery via the primary RFC 8414 path.
func TestDiscoveryOAuthServerPath(t *testing.T) {
	as := newFakeAS(t)
	m := newTestManager(t, as)
	meta, err := m.discoveryLocked(context.Background(), as.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if meta.TokenEndpoint != as.srv.URL+"/token" {
		t.Errorf("token_endpoint = %q", meta.TokenEndpoint)
	}
	if as.metadataHit.Load() != 1 {
		t.Errorf("expected 1 metadata fetch, got %d", as.metadataHit.Load())
	}

	// 3b. Second discovery is served from cache, not the wire.
	if _, err := m.discoveryLocked(context.Background(), as.srv.URL); err != nil {
		t.Fatal(err)
	}
	if as.metadataHit.Load() != 1 {
		t.Errorf("metadata not cached: %d fetches", as.metadataHit.Load())
	}
}

// 3c. OpenID Connect fallback when the RFC 8414 document is absent.
func TestDiscoveryOpenIDFallback(t *testing.T) {
	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r) // primary path missing
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": "http://localhost/authorize",
			"token_endpoint":         "http://localhost/token",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := newTestManager(t, nil)
	m.openBrowser = nil
	meta, err := m.discoveryLocked(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthorizationEndpoint != "http://localhost/authorize" {
		t.Errorf("fallback metadata not read: %+v", meta)
	}
}

// 4+5. Full PKCE authorization-code flow → token, DCR exercised, PKCE verified
// by the AS, and the token sealed on disk with no plaintext.
func TestInteractiveFlowIssuesAndStoresToken(t *testing.T) {
	as := newFakeAS(t)
	dir := t.TempDir()
	m := newTestManagerOpts(t, as, oauthOptions{storeDir: dir, openBrowser: as.browserHook(t, "", "")})

	tok, err := m.Authorize(context.Background(), challenge(as))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if tok != "ACCESS-AUTHCODE" {
		t.Fatalf("access token = %q", tok)
	}

	// DCR ran and registered a public loopback client.
	as.mu.Lock()
	reg := as.regBody
	as.mu.Unlock()
	if !strings.Contains(reg.ClientName, "srv") {
		t.Errorf("client_name = %q, want it to mention the server", reg.ClientName)
	}
	if len(reg.RedirectURIs) == 0 || !strings.HasPrefix(reg.RedirectURIs[0], "http://127.0.0.1:") {
		t.Errorf("redirect_uris = %v", reg.RedirectURIs)
	}
	if !strings.Contains(strings.Join(reg.GrantTypes, ","), "refresh_token") {
		t.Errorf("grant_types = %v", reg.GrantTypes)
	}
	if reg.TokenEndpointAuthMethod != "none" {
		t.Errorf("auth method = %q, want none (public client)", reg.TokenEndpointAuthMethod)
	}

	// Ciphertext on disk, plaintext nowhere in the file.
	data, err := os.ReadFile(m.store.path)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	if strings.Contains(raw, "ACCESS-AUTHCODE") || strings.Contains(raw, "REFRESH-rt") {
		t.Fatalf("plaintext token found on disk: %s", raw)
	}
	if !strings.Contains(raw, "enc:") {
		t.Fatalf("no sealed values on disk: %s", raw)
	}

	// A fresh manager reading the same store returns the same decrypted token.
	m2 := newTestManagerOpts(t, as, oauthOptions{storeDir: dir})
	got, _, ok := m2.store.get("srv")
	if !ok || got.AccessToken != "ACCESS-AUTHCODE" || got.RefreshToken != "REFRESH-rt" {
		t.Fatalf("round-trip failed: %+v ok=%v", got, ok)
	}
}

// 5b. State mismatch is refused and never traded for a token.
func TestFlowRejectsStateMismatch(t *testing.T) {
	as := newFakeAS(t)
	m := newTestManagerOpts(t, as, oauthOptions{
		openBrowser: as.browserHook(t, "EVIL-STATE", ""),
	})
	_, err := m.Authorize(context.Background(), challenge(as))
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("expected state validation error, got %v", err)
	}
}

// 5c. A flow that never receives a callback times out instead of blocking.
func TestFlowCallbackTimeout(t *testing.T) {
	as := newFakeAS(t)
	m := newTestManagerOpts(t, as, oauthOptions{
		openBrowser: func(string) error { return nil }, // browser opens, user never acts
		callbackTO:  80 * time.Millisecond,
	})
	start := time.Now()
	_, err := m.Authorize(context.Background(), challenge(as))
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("authorize blocked for %v", time.Since(start))
	}
}

// 6. Headless (no TTY, no browser) fails fast with an actionable Chinese error.
func TestHeadlessFailsFast(t *testing.T) {
	as := newFakeAS(t)
	m := newTestManagerOpts(t, as, oauthOptions{
		openBrowser: nil,
		isTTY:       func() bool { return false },
	})
	start := time.Now()
	_, err := m.Authorize(context.Background(), challenge(as))
	if err == nil {
		t.Fatal("expected error in headless mode")
	}
	if !strings.Contains(err.Error(), "交互式终端") {
		t.Errorf("error not the actionable Chinese message: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("headless path blocked for %v", time.Since(start))
	}
}

// 7. Silent refresh renews a token that is near expiry, exactly once.
func TestRefreshBeforeExpiry(t *testing.T) {
	as := newFakeAS(t)
	dir := t.TempDir()

	// Seed a near-expiry token + refresh token through a first manager.
	seed := newTestManagerOpts(t, as, oauthOptions{storeDir: dir})
	future := time.Now().Add(30 * time.Second) // inside the 60s refresh window
	seedTok := oauthToken{AccessToken: "OLD", RefreshToken: "RT", ExpiresAt: future, TokenType: "Bearer"}
	if err := seed.store.put("srv", seedTok, tokenClient{ClientID: "client-abc"}, as.srv.URL); err != nil {
		t.Fatal(err)
	}

	var refreshGrantSeen atomic.Bool
	as.tokenHandler = func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "RT" {
			http.Error(w, "bad refresh", http.StatusBadRequest)
			return
		}
		refreshGrantSeen.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "NEW",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}

	// A clock set just past expiry forces the refresh branch.
	m := newTestManagerOpts(t, as, oauthOptions{
		storeDir:    dir,
		now:         func() time.Time { return time.Now().Add(time.Minute) },
		openBrowser: nil,
	})

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = m.EnsureValid(context.Background()) }(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("EnsureValid %d: %v", i, err)
		}
	}
	if !refreshGrantSeen.Load() {
		t.Fatal("no refresh performed for a near-expiry token")
	}
	if got := m.bearerToken(); got != "NEW" {
		t.Fatalf("bearer = %q, want NEW", got)
	}
	// Concurrent callers must collapse to a single token round-trip (the mutex
	// serializes, and once valid the second finds nothing to do).
	if as.tokenHit.Load() != 1 {
		t.Errorf("token endpoint hit %d times, want 1 (no concurrent double-refresh)", as.tokenHit.Load())
	}
	// Refreshed token is persisted sealed.
	data, _ := os.ReadFile(m.store.path)
	if strings.Contains(string(data), "NEW") {
		t.Error("refreshed token leaked to disk in plaintext")
	}
}

// 8a. Discovered URLs in blocked ranges are refused before any dial.
func TestSSRFDiscoveredLoopbackRejectedStrict(t *testing.T) {
	as := newFakeAS(t)
	// Production validator (strict): refuses loopback/private/metadata.
	m := newTestManagerOpts(t, as, oauthOptions{
		validate:    netsec.ValidatePublicURL,
		openBrowser: nil,
	})
	// The httptest AS itself is loopback, so discovery must reject it outright.
	_, err := m.Authorize(context.Background(), challenge(as))
	if err == nil || !strings.Contains(err.Error(), "安全策略") {
		t.Fatalf("expected netsec rejection of loopback auth server, got %v", err)
	}
}

// 8b. A token endpoint the AS points at the cloud-metadata plane is refused.
func TestSSRFMetadataIPTokenEndpointRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": "http://127.0.0.1/authorize",
			"token_endpoint":         "http://169.254.169.254/latest/meta-data/token",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Lenient validator allows the loopback AS so we can reach the malicious
	// metadata token endpoint, then discovery must reject it.
	m := newTestManagerOpts(t, nil, oauthOptions{
		validate:    netsec.ValidateConfiguredURL,
		openBrowser: nil,
	})
	_, err := m.discoveryLocked(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "安全策略") {
		t.Fatalf("expected metadata-IP endpoint rejection, got %v", err)
	}
}

// 8c. netsec itself refuses the addresses the spec forbids for OAuth URLs.
func TestNetsecRejectsOAuthRanges(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:8080/token",
		"http://169.254.169.254/token",
		"http://10.0.0.5/token",
		"http://192.168.1.10/token",
	} {
		if err := netsec.ValidatePublicURL(u); err == nil {
			t.Errorf("ValidatePublicURL accepted %q", u)
		}
	}
	if err := netsec.ValidateConfiguredURL("http://169.254.169.254/token"); err == nil {
		t.Error("ValidateConfiguredURL accepted a metadata IP")
	}
	if err := netsec.ValidateConfiguredURL("http://127.0.0.1/mcp"); err != nil {
		t.Errorf("ValidateConfiguredURL should allow loopback MCP: %v", err)
	}
}

// 9. End-to-end transport integration: an SSE server that 401s until a bearer
// is present; Connect drives the flow once and retries once.
func TestConnectSSEBearerInjectionAndReauthOnce(t *testing.T) {
	as := newFakeAS(t)
	dir := t.TempDir()

	var sawBearer atomic.Bool
	// Fake MCP SSE server: 401 without the right bearer, else SSE endpoint.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.Header.Get("Authorization") == "Bearer ACCESS-AUTHCODE" {
				sawBearer.Store(true)
			} else {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer scope="read", authorization_server=%q`, as.srv.URL))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: endpoint\r\ndata: /messages?sid=x\r\n\r\n")
	})
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		<-r.Context().Done() // hold the stream open until the client closes it
	})
	mcpSrv := httptest.NewServer(mux)
	t.Cleanup(mcpSrv.Close)

	c := NewClient(ServerConfig{Name: "oauth-srv", Type: TransportSSE, URL: mcpSrv.URL, Enabled: true})
	c.SetOAuthOptions(oauthOptions{
		storeDir:    dir,
		httpClient:  &http.Client{},
		validate:    netsec.ValidateConfiguredURL,
		openBrowser: as.browserHook(t, "", ""),
		isTTY:       func() bool { return true },
		seal:        testSeal,
		open:        testOpen,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	if !sawBearer.Load() {
		t.Fatal("MCP server never received an Authorization: Bearer header")
	}
	if c.oauth.bearerToken() != "ACCESS-AUTHCODE" {
		t.Errorf("client bearer = %q", c.oauth.bearerToken())
	}
}

// 9b. A server that stays 401 even after authorization must be retried once and
// then error — never an authorization loop.
func TestReauthBoundedOnPersistent401(t *testing.T) {
	as := newFakeAS(t)
	dir := t.TempDir()

	var authorizeCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Always reject, bearer present or not.
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer authorization_server=%q`, as.srv.URL))
		w.WriteHeader(http.StatusUnauthorized)
	})
	mcpSrv := httptest.NewServer(mux)
	t.Cleanup(mcpSrv.Close)

	c := NewClient(ServerConfig{Name: "stubborn", Type: TransportSSE, URL: mcpSrv.URL, Enabled: true})
	c.SetOAuthOptions(oauthOptions{
		storeDir:    dir,
		httpClient:  &http.Client{},
		validate:    netsec.ValidateConfiguredURL,
		seal:        testSeal,
		open:        testOpen,
		isTTY:       func() bool { return true },
		openBrowser: func(u string) error { as.browserHook(t, "", "OK")(u); authorizeCalls.Add(1); return nil },
	})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected connect to fail on persistent 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
	if authorizeCalls.Load() > 1 {
		t.Fatalf("authorize ran %d times, want at most 1", authorizeCalls.Load())
	}
}
