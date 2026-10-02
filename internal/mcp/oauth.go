package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/ponygates/icode/internal/netsec"
	"github.com/ponygates/icode/internal/secure"
)

// OAuth authorization for the MCP HTTP/SSE transports, per the MCP spec
// (2025-03-10+) which reuses standard OAuth 2.x:
//
//   - a 401 carries a WWW-Authenticate challenge naming the resource / scope
//     and where to find the authorization server;
//   - RFC 8414 authorization-server metadata discovery
//     (/.well-known/oauth-authorization-server, falling back to
//     /.well-known/openid-configuration);
//   - RFC 7591 dynamic client registration to obtain a client_id;
//   - the authorization-code flow with PKCE (RFC 7636, S256) over a loopback
//     callback, plus refresh-token renewal.
//
// Discovered endpoints come from a server we do not fully trust, so every URL
// this layer dials is validated with netsec (strict by default) — an
// authorization server that points its token endpoint at the cloud-metadata
// plane is rejected before any request leaves. Tokens are persisted only in
// their secure.Encrypt (DPAPI / AES-GCM) form; plaintext never touches disk.

const (
	// oauthStoreFile is where encrypted tokens live, next to the config in
	// ~/.icode — the same at-rest posture as ProviderCfg{APIKey, APIKeyEnc}.
	oauthStoreFile = "mcp_oauth.json"
	// refreshWindow renews a token this close to expiry so an in-flight MCP
	// request never races the expiry edge.
	refreshWindow = 60 * time.Second
	// defaultCallbackTimeout bounds how long the interactive flow waits on the
	// browser round-trip. A flow with no TTY must not use it — it fails fast.
	defaultCallbackTimeout = 2 * time.Minute
)

// errNeedsAuth signals that no usable token exists and the interactive flow
// has not (yet) been run; callers surface it by driving authorize().
var errNeedsAuth = errors.New("mcp: oauth authorization required")

type oauthChallenge struct {
	// authorizationServer, when set, is the issuer to run discovery against.
	authorizationServer string
	// resourceMetadata is the RFC 9728 document URL the server advertised; we
	// use its origin as the issuer fallback when authorizationServer is empty.
	resourceMetadata string
	scope            string
	// errorHint is the WWW-Authenticate error= value, kept for messages.
	errorHint string
}

// parseWWWAuthenticate reads a `Bearer key="value", ...` challenge. Only the
// parameters MCP cares about (authorization server, resource metadata, scope)
// are extracted; unknown parameters are ignored rather than rejected, since
// servers add fields across revisions.
func parseWWWAuthenticate(header string) oauthChallenge {
	var out oauthChallenge
	header = strings.TrimSpace(header)
	if header == "" {
		return out
	}
	// The scheme is the first token; everything after it is a parameter list.
	if sp := strings.IndexByte(header, ' '); sp >= 0 {
		header = header[sp+1:]
	} else {
		return out // scheme only, no parameters
	}

	for _, part := range splitAuthParams(header) {
		k, v, ok := cutParam(part)
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "authorization_server", "authorization-server":
			out.authorizationServer = v
		case "resource_metadata", "resource-metadata":
			out.resourceMetadata = v
		case "scope":
			out.scope = v
		case "error":
			out.errorHint = v
		}
	}
	return out
}

// splitAuthParams splits `a="x", b="y", c=z` on commas that are not inside a
// quoted string, so a scope value containing a comma survives intact.
func splitAuthParams(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuotes := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			inQuotes = !inQuotes
			cur.WriteByte(c)
		case ',':
			if inQuotes {
				cur.WriteByte(c)
			} else {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func cutParam(part string) (key, val string, ok bool) {
	part = strings.TrimSpace(part)
	i := strings.IndexByte(part, '=')
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(part[:i])
	val = strings.TrimSpace(part[i+1:])
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	return key, val, true
}

// asMetadata is the subset of RFC 8414 authorization-server metadata we use.
type asMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

// dcrRequest / dcrResponse are the RFC 7591 dynamic-registration payloads. We
// register as a public client (loopback redirect, no client secret) since the
// CLI cannot keep a secret; a returned secret is still honoured.
type dcrRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	Scope                   string   `json:"scope,omitempty"`
}

type dcrResponse struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// oauthToken is a live token in memory. Access/refresh are only ever written
// to disk sealed; this struct holds plaintext and must not be marshaled to
// disk directly (see tokenStore.put).
type oauthToken struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresAt    time.Time
	Scope        string
	// Issuer is where this token came from, so a silent refresh can re-discover
	// metadata without waiting for a fresh 401 challenge.
	Issuer string
}

func (t oauthToken) valid(now time.Time) bool {
	if t.AccessToken == "" {
		return false
	}
	if t.ExpiresAt.IsZero() {
		return true
	}
	return now.Add(refreshWindow).Before(t.ExpiresAt)
}

// tokenClient is the registered client identity, kept with the token so a
// refresh can reuse the same client_id/secret.
type tokenClient struct {
	ClientID     string
	ClientSecret string
}

// tokenStore persists one entry per MCP server name. Values are sealed on save
// and opened on load; the file therefore contains only ciphertext.
type tokenStore struct {
	path string
	seal func(string) (string, error)
	open func(string) (string, error)

	mu      sync.Mutex
	loaded  bool
	entries map[string]storedToken
}

type storedToken struct {
	Issuer          string    `json:"issuer,omitempty"`
	ClientID        string    `json:"client_id,omitempty"`
	ClientSecretEnc string    `json:"client_secret_enc,omitempty"`
	AccessTokenEnc  string    `json:"access_token_enc,omitempty"`
	RefreshTokenEnc string    `json:"refresh_token_enc,omitempty"`
	TokenType       string    `json:"token_type,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	Scope           string    `json:"scope,omitempty"`
}

func newTokenStore(dir string, seal func(string) (string, error), open func(string) (string, error)) *tokenStore {
	if seal == nil {
		seal = secure.Encrypt
	}
	if open == nil {
		open = secure.Decrypt
	}
	return &tokenStore{
		path:    filepath.Join(dir, oauthStoreFile),
		seal:    seal,
		open:    open,
		entries: map[string]storedToken{},
	}
}

func (s *tokenStore) loadLocked() error {
	if s.loaded {
		return nil
	}
	s.loaded = true
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("mcp: 读取 token 存储失败: %w", err)
	}
	var disk map[string]storedToken
	if err := json.Unmarshal(data, &disk); err != nil {
		return fmt.Errorf("mcp: token 存储已损坏: %w", err)
	}
	s.entries = disk
	return nil
}

func (s *tokenStore) saveLocked() error {
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mcp: 创建 token 目录失败: %w", err)
		}
	}
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	// 0600: the file is ciphertext, but so is the master key — keep the same
	// tight mode the config writer uses.
	return os.WriteFile(s.path, data, 0o600)
}

// get returns the stored token/client for a server. Absent or undecryptable
// values are reported as missing so the caller re-authorizes rather than using
// garbage.
func (s *tokenStore) get(server string) (oauthToken, tokenClient, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return oauthToken{}, tokenClient{}, false
	}
	st, ok := s.entries[server]
	if !ok {
		return oauthToken{}, tokenClient{}, false
	}
	tok := oauthToken{TokenType: st.TokenType, ExpiresAt: st.ExpiresAt, Scope: st.Scope, Issuer: st.Issuer}
	if st.AccessTokenEnc != "" {
		if v, err := s.open(st.AccessTokenEnc); err == nil {
			tok.AccessToken = v
		}
	}
	if st.RefreshTokenEnc != "" {
		if v, err := s.open(st.RefreshTokenEnc); err == nil {
			tok.RefreshToken = v
		}
	}
	cli := tokenClient{ClientID: st.ClientID}
	if st.ClientSecretEnc != "" {
		if v, err := s.open(st.ClientSecretEnc); err == nil {
			cli.ClientSecret = v
		}
	}
	if tok.AccessToken == "" && tok.RefreshToken == "" {
		return oauthToken{}, tokenClient{}, false
	}
	return tok, cli, true
}

// put writes tok+cli for a server, sealing every secret before it reaches disk.
func (s *tokenStore) put(server string, tok oauthToken, cli tokenClient, issuer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	st := storedToken{Issuer: issuer, ClientID: cli.ClientID, TokenType: tok.TokenType, ExpiresAt: tok.ExpiresAt, Scope: tok.Scope}
	// A field the new token omitted falls back to the previously stored one, so
	// a refresh that returns no new refresh_token keeps the old ciphertext.
	prev := s.entries[server]
	var err error
	if st.AccessTokenEnc, err = sealOr(s.seal, prev.AccessTokenEnc, tok.AccessToken); err != nil {
		return err
	}
	if st.RefreshTokenEnc, err = sealOr(s.seal, prev.RefreshTokenEnc, tok.RefreshToken); err != nil {
		return err
	}
	if st.ClientSecretEnc, err = sealOr(s.seal, prev.ClientSecretEnc, cli.ClientSecret); err != nil {
		return err
	}
	if s.entries == nil {
		s.entries = map[string]storedToken{}
	}
	s.entries[server] = st
	return s.saveLocked()
}

func sealOr(seal func(string) (string, error), prev, plain string) (string, error) {
	if plain == "" {
		return prev, nil
	}
	return seal(plain)
}

// oauthOptions configures an OAuthManager. Every field except server/resource
// has a production default; tests inject a lenient client, a temp store dir, a
// fake browser hook and a fixed clock.
type oauthOptions struct {
	server      string
	resourceURL string
	storeDir    string
	seal        func(string) (string, error)
	open        func(string) (string, error)
	httpClient  *http.Client
	validate    func(string) error
	openBrowser func(string) error
	isTTY       func() bool
	out         io.Writer
	now         func() time.Time
	callbackTO  time.Duration
}

// OAuthManager runs the HTTP authorization layer for one MCP server.
type OAuthManager struct {
	oauthOptions
	store       *tokenStore
	http        *http.Client
	validate    func(string) error
	openBrowser func(string) error
	isTTY       func() bool
	out         io.Writer
	now         func() time.Time

	mu        sync.Mutex // serializes authorize/refresh — one flow per server
	bearer    string     // last access token, for non-blocking header injection
	metaCache map[string]asMetadata
}

func newOAuthManager(opt oauthOptions) *OAuthManager {
	if opt.validate == nil {
		opt.validate = netsec.ValidatePublicURL
	}
	if opt.httpClient == nil {
		opt.httpClient = netsec.GuardedClient(30*time.Second, true)
	}
	if opt.isTTY == nil {
		opt.isTTY = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	}
	if opt.out == nil {
		opt.out = os.Stderr
	}
	if opt.now == nil {
		opt.now = time.Now
	}
	if opt.callbackTO == 0 {
		opt.callbackTO = defaultCallbackTimeout
	}
	if opt.storeDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			opt.storeDir = filepath.Join(home, ".icode")
		} else {
			opt.storeDir = ".icode"
		}
	}
	return &OAuthManager{
		oauthOptions: opt,
		store:        newTokenStore(opt.storeDir, opt.seal, opt.open),
		http:         opt.httpClient,
		validate:     opt.validate,
		openBrowser:  opt.openBrowser,
		isTTY:        opt.isTTY,
		out:          opt.out,
		now:          opt.now,
		metaCache:    map[string]asMetadata{},
	}
}

// bearerToken returns the cached access token for header injection. It never
// performs I/O — refresh/authorize are explicit, called from the request path —
// so sseReadLoop can stamp the Authorization header from its own goroutine.
func (m *OAuthManager) bearerToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bearer
}

// EnsureValid loads the persisted token and silently refreshes it if it is
// missing/near-expiry but a refresh_token exists. When no interactive flow is
// possible it returns errNeedsAuth rather than blocking.
func (m *OAuthManager) EnsureValid(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureLocked(ctx)
}

func (m *OAuthManager) ensureLocked(ctx context.Context) error {
	tok, cli, ok := m.store.get(m.server)
	if ok && tok.valid(m.now()) {
		m.bearer = tok.AccessToken
		return nil
	}
	if ok && tok.RefreshToken != "" {
		meta, err := m.discoveryLocked(ctx, issuerFor(m, tok, cli))
		if err != nil {
			return err
		}
		refreshed, err := m.refreshLocked(ctx, meta, cli, tok)
		if err != nil {
			// A failed refresh must not drop the still-usable (if stale) token.
			if tok.AccessToken != "" {
				m.bearer = tok.AccessToken
			}
			return err
		}
		m.bearer = refreshed.AccessToken
		return nil
	}
	if tok.AccessToken != "" {
		m.bearer = tok.AccessToken
	}
	return errNeedsAuth
}

func issuerFor(m *OAuthManager, tok oauthToken, cli tokenClient) string {
	// The issuer was recorded at authorize time; absent that (older entry) fall
	// back to the resource origin, which is where discovery started anyway.
	if tok.Issuer != "" {
		return tok.Issuer
	}
	return originOf(m.resourceURL)
}

// Authorize runs the interactive authorization-code flow in response to a 401
// challenge. It is the entry point the transport calls when a request comes
// back unauthorized. A stored valid token short-circuits it.
func (m *OAuthManager) Authorize(ctx context.Context, ch oauthChallenge) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureLocked(ctx); err == nil {
		return m.bearer, nil
	} else if !errors.Is(err, errNeedsAuth) {
		return "", err
	}

	if !m.canInteract() {
		return "", m.headlessError()
	}

	issuer := strings.TrimSpace(ch.authorizationServer)
	if issuer == "" {
		issuer = originOf(ch.resourceMetadata)
	}
	if issuer == "" {
		issuer = originOf(m.resourceURL)
	}

	meta, err := m.discoveryLocked(ctx, issuer)
	if err != nil {
		return "", err
	}
	cli, err := m.clientLocked(ctx, meta, ch)
	if err != nil {
		return "", err
	}
	tok, err := m.codeFlowLocked(ctx, meta, cli, ch)
	if err != nil {
		return "", err
	}
	if err := m.store.put(m.server, tok, cli, meta.Issuer); err != nil {
		return "", err
	}
	m.bearer = tok.AccessToken
	return tok.AccessToken, nil
}

func (m *OAuthManager) canInteract() bool {
	return m.openBrowser != nil || m.isTTY()
}

func (m *OAuthManager) headlessError() error {
	return fmt.Errorf("该 MCP 服务器（%s）需要 OAuth 授权，但当前不是交互式终端，无法打开浏览器完成登录。请在有图形界面的机器上运行 `icode` 完成一次授权（token 会加密保存到 ~/.icode/%s），或为服务器 %q 在配置中设置静态 Authorization 头后重试",
		m.server, oauthStoreFile, m.server)
}

// discoveryLocked resolves authorization-server metadata for an issuer, trying
// RFC 8414 then OpenID Connect, and caches the result per issuer.
func (m *OAuthManager) discoveryLocked(ctx context.Context, issuer string) (asMetadata, error) {
	if issuer == "" {
		return asMetadata{}, errors.New("mcp: 无法确定授权服务器地址（401 挑战未提供 authorization_server/resource）")
	}
	if cached, ok := m.metaCache[issuer]; ok {
		return cached, nil
	}
	var lastErr error
	for _, cand := range wellKnownCandidates(issuer) {
		if err := m.validate(cand); err != nil {
			lastErr = fmt.Errorf("授权服务器发现地址被安全策略拒绝: %w", err)
			continue
		}
		var meta asMetadata
		if err := m.getJSON(ctx, cand, &meta); err != nil {
			lastErr = err
			continue
		}
		if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
			lastErr = fmt.Errorf("mcp: 授权服务器元数据缺少 authorization_endpoint/token_endpoint (%s)", cand)
			continue
		}
		// The endpoints come straight from the metadata doc — validate before
		// any client_id or code is ever sent to them.
		for _, ep := range []string{meta.AuthorizationEndpoint, meta.TokenEndpoint, meta.RegistrationEndpoint} {
			if ep == "" {
				continue
			}
			if err := m.validate(ep); err != nil {
				return asMetadata{}, fmt.Errorf("授权服务器上报的端点被安全策略拒绝: %w", err)
			}
		}
		if meta.Issuer == "" {
			meta.Issuer = issuer
		}
		m.metaCache[issuer] = meta
		return meta, nil
	}
	if lastErr == nil {
		lastErr = errors.New("mcp: 未找到授权服务器元数据")
	}
	return asMetadata{}, lastErr
}

// wellKnownCandidates builds the RFC 8414 path-insertion form plus the OpenID
// fallback, so an issuer with a base path (`/tenant`) is probed at
// `/.well-known/.../tenant` as the spec requires.
func wellKnownCandidates(issuer string) []string {
	cands := []string{
		joinWellKnown(issuer, "oauth-authorization-server", false),
		joinWellKnown(issuer, "openid-configuration", false),
	}
	// Path-insertion variants are only meaningful when the issuer has a path.
	cands = append(cands,
		joinWellKnown(issuer, "oauth-authorization-server", true),
		joinWellKnown(issuer, "openid-configuration", true),
	)
	return dedupeStrings(cands)
}

func joinWellKnown(issuer, name string, withPath bool) string {
	u, err := url.Parse(issuer)
	if err != nil {
		return ""
	}
	path := strings.Trim(u.Path, "/")
	wk := "/.well-known/" + name
	if withPath && path != "" {
		return u.Scheme + "://" + u.Host + wk + "/" + path
	}
	return u.Scheme + "://" + u.Host + wk
}

// clientLocked returns a registered client, using a stored client_id when
// present, otherwise RFC 7591 dynamic registration.
func (m *OAuthManager) clientLocked(ctx context.Context, meta asMetadata, ch oauthChallenge) (tokenClient, error) {
	if _, cli, ok := m.store.get(m.server); ok && cli.ClientID != "" {
		return cli, nil
	}
	if meta.RegistrationEndpoint == "" {
		return tokenClient{}, errors.New("mcp: 授权服务器未提供注册端点，无法动态获取 client_id")
	}
	port, _ := listenerPortPlaceholder()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	req := dcrRequest{
		ClientName:              "iCode MCP (" + m.server + ")",
		RedirectURIs:            []string{redirect},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		Scope:                   ch.scope,
	}
	var resp dcrResponse
	if err := m.postJSON(ctx, meta.RegistrationEndpoint, req, &resp); err != nil {
		return tokenClient{}, fmt.Errorf("mcp: 动态客户端注册失败: %w", err)
	}
	if resp.ClientID == "" {
		return tokenClient{}, errors.New("mcp: 动态注册未返回 client_id")
	}
	return tokenClient{ClientID: resp.ClientID, ClientSecret: resp.ClientSecret}, nil
}

// codeFlowLocked runs PKCE authorization-code grant over a loopback callback.
func (m *OAuthManager) codeFlowLocked(ctx context.Context, meta asMetadata, cli tokenClient, ch oauthChallenge) (oauthToken, error) {
	verifier, err := newCodeVerifier()
	if err != nil {
		return oauthToken{}, err
	}
	state := newState()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return oauthToken{}, fmt.Errorf("mcp: 无法监听本地回调端口: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	authURL, err := buildAuthorizeURL(meta.AuthorizationEndpoint, authorizeParams{
		ClientID:    cli.ClientID,
		RedirectURI: redirect,
		Scope:       ch.scope,
		State:       state,
		Challenge:   codeChallenge(verifier),
		Resource:    m.resourceURL,
	})
	if err != nil {
		return oauthToken{}, err
	}

	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			writeCallback(w, "授权被拒绝："+e)
			resCh <- result{err: fmt.Errorf("mcp: 授权失败: %s", e)}
			return
		}
		// Constant work then compare: a mismatched state means the response was
		// not in reply to our request (CSRF) — never trade it for a token.
		if !strings.EqualFold(q.Get("state"), state) {
			writeCallback(w, "state 校验失败，已忽略此次回调")
			resCh <- result{err: errors.New("mcp: OAuth state 校验失败，疑似 CSRF 或过期回调")}
			return
		}
		code := q.Get("code")
		if code == "" {
			writeCallback(w, "回调缺少 code")
			resCh <- result{err: errors.New("mcp: 授权回调缺少 code")}
			return
		}
		writeCallback(w, "授权成功，可以关闭此页面。")
		resCh <- result{code: code}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	m.publishAuthURL(authURL)

	timeout := time.NewTimer(m.callbackTO)
	defer timeout.Stop()

	var code string
	select {
	case r := <-resCh:
		if r.err != nil {
			return oauthToken{}, r.err
		}
		code = r.code
	case <-ctx.Done():
		return oauthToken{}, ctx.Err()
	case <-timeout.C:
		return oauthToken{}, errors.New("mcp: 等待浏览器授权超时")
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"client_id":     {cli.ClientID},
		"code_verifier": {verifier},
	}
	if cli.ClientSecret != "" {
		form.Set("client_secret", cli.ClientSecret)
	}
	if m.resourceURL != "" {
		form.Set("resource", m.resourceURL)
	}
	return m.requestToken(ctx, meta.TokenEndpoint, form)
}

func (m *OAuthManager) refreshLocked(ctx context.Context, meta asMetadata, cli tokenClient, tok oauthToken) (oauthToken, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tok.RefreshToken},
		"client_id":     {cli.ClientID},
	}
	if cli.ClientSecret != "" {
		form.Set("client_secret", cli.ClientSecret)
	}
	if m.resourceURL != "" {
		form.Set("resource", m.resourceURL)
	}
	nt, err := m.requestToken(ctx, meta.TokenEndpoint, form)
	if err != nil {
		return oauthToken{}, err
	}
	if nt.RefreshToken == "" {
		nt.RefreshToken = tok.RefreshToken
	}
	if err := m.store.put(m.server, nt, cli, meta.Issuer); err != nil {
		return oauthToken{}, err
	}
	return nt, nil
}

// publishAuthURL opens the browser when a hook is wired, otherwise — only ever
// on a TTY, which canInteract already guaranteed — prints it for manual use.
func (m *OAuthManager) publishAuthURL(authURL string) {
	if m.openBrowser != nil {
		if err := m.openBrowser(authURL); err == nil {
			return
		}
	}
	fmt.Fprintf(m.out, "请在浏览器打开以下地址完成 %s 的授权：\n  %s\n", m.server, authURL)
}

type authorizeParams struct {
	ClientID    string
	RedirectURI string
	Scope       string
	State       string
	Challenge   string
	Resource    string
}

func buildAuthorizeURL(endpoint string, p authorizeParams) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("mcp: 授权端点无效: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("state", p.State)
	q.Set("code_challenge", p.Challenge)
	q.Set("code_challenge_method", "S256")
	if p.Scope != "" {
		q.Set("scope", p.Scope)
	}
	if p.Resource != "" {
		q.Set("resource", p.Resource)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// requestToken POSTs a form to the token endpoint and normalizes the response.
func (m *OAuthManager) requestToken(ctx context.Context, endpoint string, form url.Values) (oauthToken, error) {
	if err := m.validate(endpoint); err != nil {
		return oauthToken{}, fmt.Errorf("token 端点被安全策略拒绝: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return oauthToken{}, fmt.Errorf("mcp: 请求 token 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return oauthToken{}, fmt.Errorf("mcp: token 端点返回 %d: %s", resp.StatusCode, snippet(body))
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return oauthToken{}, fmt.Errorf("mcp: 解析 token 响应失败: %w", err)
	}
	if tr.AccessToken == "" {
		return oauthToken{}, errors.New("mcp: token 响应缺少 access_token")
	}
	tok := oauthToken{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		Scope:        tr.Scope,
	}
	if tr.ExpiresIn > 0 {
		tok.ExpiresAt = m.now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return tok, nil
}

func (m *OAuthManager) getJSON(ctx context.Context, endpoint string, out any) error {
	if err := m.validate(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return json.Unmarshal(body, out)
}

func (m *OAuthManager) postJSON(ctx context.Context, endpoint string, in, out any) error {
	if err := m.validate(endpoint); err != nil {
		return err
	}
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, snippet(body))
	}
	return json.Unmarshal(body, out)
}

// parseChallengeFromResponse extracts the OAuth challenge from a 401 reply.
func parseChallengeFromResponse(resp *http.Response) oauthChallenge {
	return parseWWWAuthenticate(resp.Header.Get("WWW-Authenticate"))
}

// newCodeVerifier returns a 43-char RFC 7636 code_verifier from crypto/rand.
func newCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mcp: 生成 PKCE verifier 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// codeChallenge is the S256 transformation: BASE64URL(SHA256(verifier)).
func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newState() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func writeCallback(w io.Writer, msg string) {
	_, _ = io.WriteString(w, "<html><body>"+msg+"</body></html>")
}

func originOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// listenerPortPlaceholder returns a port for the pre-registration redirect URI.
// The real port is chosen when the callback listener opens; the value in the
// DCR request is only a client_name hint for servers that ignore it, so any
// ephemeral port is acceptable here.
func listenerPortPlaceholder() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
