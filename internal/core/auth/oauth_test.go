package auth

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

	"github.com/ponygates/icode/internal/config"
)

// setOAuth writes an oauth block for a provider so LoginOAuth/Refresh can find
// it via config.Load.
func setOAuth(t *testing.T, provider string, oc *config.OAuthCfg, grant *config.OAuthGrant, access string) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pc := cfg.Providers[provider]
	pc.OAuth = oc
	pc.Grant = grant
	pc.APIKey = access
	cfg.Providers[provider] = pc
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// authAndTokenServers builds an authorize server (captures the PKCE challenge,
// redirects to the loopback callback) and a token server (validates the
// verifier, mints tokens). It is the whole fake vendor for one end-to-end run.
func authAndTokenServers(t *testing.T, accessToken, refreshToken string, expiresIn int64) (authorizeURL, tokenURL string, seen *sync.Map) {
	t.Helper()
	seen = &sync.Map{}
	var auth *httptest.Server
	var tok *httptest.Server

	auth = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seen.Store("code_challenge", q.Get("code_challenge"))
		seen.Store("code_challenge_method", q.Get("code_challenge_method"))
		seen.Store("state", q.Get("state"))
		seen.Store("client_id", q.Get("client_id"))
		seen.Store("redirect_uri", q.Get("redirect_uri"))
		seen.Store("scope", q.Get("scope"))
		seen.Store("response_type", q.Get("response_type"))
		cu, _ := url.Parse(q.Get("redirect_uri"))
		loc := fmt.Sprintf("%s?code=THE-CODE&state=%s", cu.String(), q.Get("state"))
		http.Redirect(w, r, loc, http.StatusFound)
		_ = auth
	}))
	tok = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen.Store("grant_type", r.FormValue("grant_type"))
		seen.Store("code_verifier", r.FormValue("code_verifier"))
		seen.Store("code", r.FormValue("code"))
		seen.Store("token_client_id", r.FormValue("client_id"))
		// RFC 7636 §4.2: verifier must hash to the challenge we were sent.
		verifier := r.FormValue("code_verifier")
		chal, _ := seen.Load("code_challenge")
		sum := sha256.Sum256([]byte(verifier))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != chal.(string) {
			http.Error(w, "invalid PKCE verifier", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"expires_in":    expiresIn,
		})
	}))
	t.Cleanup(auth.Close)
	t.Cleanup(tok.Close)
	return auth.URL + "/authorize", tok.URL + "/token", seen
}

func TestLoginOAuthEndToEnd(t *testing.T) {
	isolate(t)
	acc, ref := "oauth-access-token-xyz", "oauth-refresh-token-abc"
	authURL, tokenURL, seen := authAndTokenServers(t, acc, ref, 3600)
	setOAuth(t, "vendorx", &config.OAuthCfg{
		AuthorizeURL: authURL,
		TokenURL:     tokenURL,
		ClientID:     "cid-123",
		Scopes:       []string{"read", "write"},
	}, nil, "")

	// The test's "browser" is a plain HTTP GET that follows the authorize
	// server's redirect into iCode's loopback listener — no real browser, no
	// network beyond 127.0.0.1.
	open := func(u string) error {
		c := &http.Client{}
		resp, err := c.Get(u)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	tok, err := LoginOAuth(context.Background(), "vendorx", open)
	if err != nil {
		t.Fatalf("LoginOAuth: %v", err)
	}
	if tok.AccessToken != acc || tok.RefreshToken != ref {
		t.Fatalf("unexpected tokens: %+v", tok)
	}
	if tok.ExpiresAt.IsZero() {
		t.Fatal("expected an expiry from expires_in")
	}
	// PKCE + request correctness.
	if m, _ := seen.Load("code_challenge_method"); m != "S256" {
		t.Errorf("authorize must send S256, got %v", m)
	}
	if rt, _ := seen.Load("response_type"); rt != "code" {
		t.Errorf("response_type must be code, got %v", rt)
	}
	if gt, _ := seen.Load("grant_type"); gt != "authorization_code" {
		t.Errorf("token grant_type wrong: %v", gt)
	}
	if cid, _ := seen.Load("client_id"); cid != "cid-123" {
		t.Errorf("client_id not forwarded: %v", cid)
	}
	if sc, _ := seen.Load("scope"); sc != "read write" {
		t.Errorf("scopes not joined: %v", sc)
	}
	// Loopback redirect URI uses the dynamically bound port.
	ru, _ := seen.Load("redirect_uri")
	if !strings.HasPrefix(ru.(string), "http://127.0.0.1:") || !strings.HasSuffix(ru.(string), "/callback") {
		t.Errorf("redirect_uri not loopback callback: %v", ru)
	}

	// Persisted grant keeps the token encrypted on disk.
	if _, err := SaveGrant("vendorx", tok); err != nil {
		t.Fatalf("save grant: %v", err)
	}
	data, _ := os.ReadFile(config.DefaultPath())
	if strings.Contains(string(data), acc) || strings.Contains(string(data), ref) {
		t.Fatalf("plaintext token leaked to disk:\n%s", data)
	}
	cfg, _ := config.Load()
	if cfg.Providers["vendorx"].APIKey != acc {
		t.Fatal("access token should round-trip through api_key")
	}
	if cfg.Providers["vendorx"].Grant.RefreshToken != ref {
		t.Fatal("refresh token should round-trip through the encrypted grant")
	}
}

func TestLoginOAuthStateMismatchRejected(t *testing.T) {
	isolate(t)
	// Authorize server sends back a DIFFERENT state than requested.
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cu, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
		http.Redirect(w, r, cu.String()+"?code=THE-CODE&state=TAMPERED", http.StatusFound)
	}))
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("token endpoint must not be reached when state mismatches")
	}))
	t.Cleanup(auth.Close)
	t.Cleanup(tok.Close)
	setOAuth(t, "vendorx", &config.OAuthCfg{AuthorizeURL: auth.URL + "/a", TokenURL: tok.URL + "/t", ClientID: "c"}, nil, "")

	open := func(u string) error {
		c := &http.Client{}
		resp, err := c.Get(u)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	_, err := LoginOAuth(context.Background(), "vendorx", open)
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("expected state rejection, got %v", err)
	}
}

func TestLoginOAuthTimeoutDoesNotHang(t *testing.T) {
	isolate(t)
	// Authorize server never redirects, so no callback ever arrives.
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(auth.Close)
	setOAuth(t, "vendorx", &config.OAuthCfg{AuthorizeURL: auth.URL + "/a", TokenURL: "http://127.0.0.1:1/t", ClientID: "c"}, nil, "")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := LoginOAuth(ctx, "vendorx", func(string) error { return nil })
	if err == nil {
		t.Fatal("expected a timeout/cancel error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("LoginOAuth blocked too long: %v", time.Since(start))
	}
}

func TestSaveGrantEncryptsRefreshToken(t *testing.T) {
	isolate(t)
	acc, ref := "at-secret-1234567890", "rt-secret-abcdefghij"
	if _, err := SaveGrant("vendorx", Tokens{
		AccessToken:  acc,
		RefreshToken: ref,
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		Account:      "me@example.com",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, _ := os.ReadFile(config.DefaultPath())
	if strings.Contains(string(data), acc) || strings.Contains(string(data), ref) {
		t.Fatalf("plaintext token present on disk:\n%s", data)
	}
	if !strings.Contains(string(data), "api_key_enc") || !strings.Contains(string(data), "refresh_token_enc") {
		t.Fatalf("expected encrypted token fields:\n%s", data)
	}
	cfg, _ := config.Load()
	g := cfg.Providers["vendorx"].Grant
	if g == nil || g.RefreshToken != ref || cfg.Providers["vendorx"].APIKey != acc {
		t.Fatalf("round-trip failed: %+v", g)
	}
}

func TestRefreshProactiveAndSkipsFreshToken(t *testing.T) {
	isolate(t)
	var posts int64
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&posts, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "renewed-access-token", "refresh_token": "renewed-refresh", "expires_in": 3600,
		})
	}))
	t.Cleanup(tok.Close)
	oc := &config.OAuthCfg{TokenURL: tok.URL + "/t", ClientID: "c"}

	// About to expire (inside the 60s skew) → refresh runs.
	setOAuth(t, "vendorx", oc, &config.OAuthGrant{
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().UTC().Add(30 * time.Second).Unix(),
	}, "old-access")
	got, bearer, err := Refresh(context.Background(), "vendorx")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !bearer {
		t.Fatal("an OAuth login refresh must report a bearer credential")
	}
	if got != "renewed-access-token" || atomic.LoadInt64(&posts) != 1 {
		t.Fatalf("expected a proactive refresh, got token=%q posts=%d", got, posts)
	}

	// Comfortably fresh → no network, existing token returned.
	setOAuth(t, "vendorx", oc, &config.OAuthGrant{
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().UTC().Add(2 * time.Hour).Unix(),
	}, "still-valid-access")
	got, bearer, err = Refresh(context.Background(), "vendorx")
	if err != nil {
		t.Fatalf("refresh fresh: %v", err)
	}
	if !bearer {
		t.Fatal("a fresh OAuth grant is still a bearer credential")
	}
	if got != "still-valid-access" || atomic.LoadInt64(&posts) != 1 {
		t.Fatalf("a fresh token must not trigger a refresh: token=%q posts=%d", got, posts)
	}
}

func TestRefreshConcurrentOnlyOnce(t *testing.T) {
	isolate(t)
	var posts int64
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&posts, 1)
		// Slow enough that racing goroutines would each fire without a lock.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "renewed", "refresh_token": "rotated", "expires_in": 3600,
		})
	}))
	t.Cleanup(tok.Close)
	setOAuth(t, "vendorx", &config.OAuthCfg{TokenURL: tok.URL + "/t", ClientID: "c"}, &config.OAuthGrant{
		RefreshToken: "old",
		ExpiresAt:    time.Now().UTC().Add(-time.Minute).Unix(),
	}, "old-access")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := Refresh(context.Background(), "vendorx"); err != nil {
				t.Errorf("refresh: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt64(&posts); n != 1 {
		t.Fatalf("concurrent refresh must fire exactly once, saw %d", n)
	}
}

func TestRefreshSSRFRejected(t *testing.T) {
	isolate(t)
	// A token endpoint on the cloud-metadata plane must be refused before dial.
	setOAuth(t, "vendorx", &config.OAuthCfg{TokenURL: "http://169.254.169.254/token", ClientID: "c"}, &config.OAuthGrant{
		RefreshToken: "r",
		ExpiresAt:    time.Now().UTC().Add(-time.Minute).Unix(),
	}, "a")
	_, _, err := Refresh(context.Background(), "vendorx")
	if err == nil || !strings.Contains(err.Error(), "netsec") {
		t.Fatalf("expected netsec rejection, got %v", err)
	}
}

func TestLoginOAuthDiscoveryAndDCR(t *testing.T) {
	isolate(t)
	acc, ref := "dcr-access-token", "dcr-refresh-token"
	_, tokenURL, _ := authAndTokenServers(t, acc, ref, 3600)

	// One mux serves discovery, registration and authorize/token from the
	// previous helper's token URL host? Simpler: a dedicated fake AS.
	var codeChallenge string
	var as *httptest.Server
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint":           as.URL + "/authorize",
				"token_endpoint":                   as.URL + "/token",
				"registration_endpoint":            as.URL + "/register",
				"scopes_supported":                 []string{"openid", "email"},
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/register":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["token_endpoint_auth_method"] != "none" {
				t.Errorf("DCR must register a public client")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "issued-dcr-client"})
		case "/authorize":
			q := r.URL.Query()
			codeChallenge = q.Get("code_challenge")
			if q.Get("client_id") != "issued-dcr-client" {
				t.Errorf("discovered+DCR client_id not used: %v", q.Get("client_id"))
			}
			cu, _ := url.Parse(q.Get("redirect_uri"))
			http.Redirect(w, r, cu.String()+"?code=THE-CODE&state="+q.Get("state"), http.StatusFound)
		case "/token":
			_ = r.ParseForm()
			sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != codeChallenge {
				http.Error(w, "bad verifier", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": acc, "refresh_token": ref, "expires_in": 3600,
			})
		}
	}))
	t.Cleanup(as.Close)

	setOAuth(t, "vendorx", &config.OAuthCfg{
		DiscoveryURL: as.URL + "/.well-known/oauth-authorization-server",
		UsesDCR:      true,
	}, nil, "")
	open := func(u string) error {
		resp, err := (&http.Client{}).Get(u)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	tok, err := LoginOAuth(context.Background(), "vendorx", open)
	if err != nil {
		t.Fatalf("LoginOAuth discovery/DCR: %v", err)
	}
	if tok.AccessToken != acc {
		t.Fatalf("wrong token: %+v", tok)
	}
	_ = tokenURL
}

func TestOpenBrowserUnconfiguredErrors(t *testing.T) {
	isolate(t)
	// No oauth block → LoginOAuth must refuse, not spin up a listener.
	_, err := LoginOAuth(context.Background(), "novendor", nil)
	if err == nil || !strings.Contains(err.Error(), "oauth") {
		t.Fatalf("expected missing-oauth error, got %v", err)
	}
}
