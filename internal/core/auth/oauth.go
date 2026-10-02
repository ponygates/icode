package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/netsec"
)

// OAuth 2.0 authorization-code + PKCE login for subscription accounts, mirroring
// what Claude Code / Codex do with a browser. The flow is deliberately generic
// and configuration-driven: the official Anthropic/OpenAI documentation does not
// publish fixed authorize/token endpoints or a client_id (the Claude Code auth
// docs describe only the browser flow and where credentials land, and its modern
// path is RFC 8414 discovery + RFC 7591 dynamic registration), so iCode refuses
// to hardcode guessed values. A vendor is enabled by filling in its own
// authorization-server details under providers.<name>.oauth in config.yaml.
//
// Nothing here dials a real vendor during tests: every outbound request goes
// through the guarded client and, in unit tests, only to a local httptest
// loopback. The loopback callback listener is our own server, never dialed.

// Tokens is the outcome of a successful code exchange or refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time // zero when the vendor reported no expiry
	Account      string    // vendor-reported identity (email), for status
}

// flowConfig is the OAuthConfig resolved to concrete, dial-ready values after
// discovery and (if needed) dynamic client registration.
type flowConfig struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	Scopes       string
	RedirectBase string // loopback origin, e.g. http://127.0.0.1
}

// oauthTimeout bounds how long we wait for the browser callback. Long enough to
// complete a login, short enough that an abandoned flow never leaves a stuck
// listener.
const oauthTimeout = 5 * time.Minute

// refreshSkew triggers a proactive refresh this long before expiry, so a request
// never races the token's actual death.
const refreshSkew = 60 * time.Second

// httpOK reads a bounded body and asserts a 2xx status.
func httpOK(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("HTTP %d — %s", resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

// guardedGet issues a validated GET against a user-configured endpoint. The URL
// is checked for the metadata plane before dialing and dialed through the SSRF
// guard, so a config.yaml value can never reach 169.254.169.254.
func guardedGet(ctx context.Context, raw string) ([]byte, error) {
	if err := netsec.ValidateConfiguredURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	client := netsec.GuardedClient(20*time.Second, false)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return httpOK(resp)
}

// guardedForm posts a urlencoded form to a validated token endpoint.
func guardedForm(ctx context.Context, raw string, form url.Values) ([]byte, error) {
	if err := netsec.ValidateConfiguredURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, raw, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := netsec.GuardedClient(20*time.Second, false)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return httpOK(resp)
}

// guardedJSON posts a JSON body to a validated registration endpoint (RFC 7591).
func guardedJSON(ctx context.Context, raw string, payload any) ([]byte, error) {
	if err := netsec.ValidateConfiguredURL(raw); err != nil {
		return nil, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, raw, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := netsec.GuardedClient(20*time.Second, false)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return httpOK(resp)
}

// oauthMetadata is the subset of RFC 8414 authorization-server metadata iCode
// consumes.
type oauthMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	ScopesSupported               []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// resolveEndpoints turns a config OAuthCfg into dial-ready flow settings,
// consulting the discovery document and performing DCR only when the config
// leaves the corresponding values blank.
func resolveEndpoints(ctx context.Context, oc *config.OAuthCfg) (flowConfig, error) {
	if oc == nil {
		return flowConfig{}, fmt.Errorf("该提供商未配置 oauth 登录项（providers.<name>.oauth）")
	}
	fc := flowConfig{
		AuthorizeURL: strings.TrimSpace(oc.AuthorizeURL),
		TokenURL:     strings.TrimSpace(oc.TokenURL),
		ClientID:     strings.TrimSpace(oc.ClientID),
		Scopes:       strings.Join(oc.Scopes, " "),
		RedirectBase: strings.TrimSpace(oc.RedirectBase),
	}
	if fc.RedirectBase == "" {
		fc.RedirectBase = "http://127.0.0.1"
	}
	if u, err := url.Parse(fc.RedirectBase); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() != "127.0.0.1" {
		return flowConfig{}, fmt.Errorf("oauth redirect_base 必须是 http://127.0.0.1 形式的本地回环地址")
	}

	// Discovery fills only what is still missing, so an explicit config always
	// wins over a metadata document.
	var meta *oauthMetadata
	if (fc.AuthorizeURL == "" || fc.TokenURL == "" || fc.Scopes == "") && strings.TrimSpace(oc.DiscoveryURL) != "" {
		body, err := guardedGet(ctx, strings.TrimSpace(oc.DiscoveryURL))
		if err != nil {
			return flowConfig{}, fmt.Errorf("读取 oauth 发现文档失败: %w", err)
		}
		meta = &oauthMetadata{}
		if err := json.Unmarshal(body, meta); err != nil {
			return flowConfig{}, fmt.Errorf("解析 oauth 发现文档失败: %w", err)
		}
		if fc.AuthorizeURL == "" {
			fc.AuthorizeURL = meta.AuthorizationEndpoint
		}
		if fc.TokenURL == "" {
			fc.TokenURL = meta.TokenEndpoint
		}
		if fc.Scopes == "" && len(meta.ScopesSupported) > 0 {
			fc.Scopes = strings.Join(meta.ScopesSupported, " ")
		}
		// PKCE is not optional for a public client; refuse a server that does
		// not advertise S256 rather than silently fall back to plain.
		if !s256Supported(meta) {
			return flowConfig{}, fmt.Errorf("厂商授权服务器未声明支持 PKCE S256，iCode 拒绝发起不安全登录")
		}
	}
	if fc.AuthorizeURL == "" || fc.TokenURL == "" {
		return flowConfig{}, fmt.Errorf("oauth 配置缺少 authorize_url 或 token_url（请填写厂商官方端点，或设置 discovery_url 由 iCode 自动发现）")
	}

	// Dynamic registration only when there is no client id yet.
	if fc.ClientID == "" {
		regURL := strings.TrimSpace(oc.RegisterURL)
		if regURL == "" && meta != nil {
			regURL = meta.RegistrationEndpoint
		}
		if regURL == "" {
			return flowConfig{}, fmt.Errorf("oauth 配置缺少 client_id，且没有可用的注册端点（RFC 7591）")
		}
		cid, err := registerClient(ctx, regURL, fc.RedirectBase)
		if err != nil {
			return flowConfig{}, fmt.Errorf("动态注册客户端失败: %w", err)
		}
		fc.ClientID = cid
	}
	return fc, nil
}

func s256Supported(meta *oauthMetadata) bool {
	if meta == nil {
		return true // explicit endpoints: assume PKCE, the flow still sends S256
	}
	if len(meta.CodeChallengeMethodsSupported) == 0 {
		return false
	}
	for _, m := range meta.CodeChallengeMethodsSupported {
		if strings.EqualFold(m, "S256") {
			return true
		}
	}
	return false
}

// registerClient performs an RFC 7591 dynamic registration for a public PKCE
// client and returns the issued client_id. redirect_uris are built against the
// loopback origin the callback listener will use; the vendor accepts any port on
// 127.0.0.1 per RFC 8252.
func registerClient(ctx context.Context, registerURL, redirectBase string) (string, error) {
	payload := map[string]any{
		"client_name":                "iCode",
		"redirect_uris":              []string{redirectBase + "/callback"},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	body, err := guardedJSON(ctx, registerURL, payload)
	if err != nil {
		return "", err
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("解析注册响应失败: %w", err)
	}
	if strings.TrimSpace(out.ClientID) == "" {
		return "", fmt.Errorf("注册响应没有 client_id")
	}
	return out.ClientID, nil
}

// PKCE code_verifier is 43-128 chars of unreserved set (RFC 7636 §4.1); 32
// random bytes base64url (no padding) is 43 chars — the minimum length.
func newVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func challengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// callbackResult carries one authorization response off the loopback handler.
type callbackResult struct {
	code  string
	state string
	err   string
}

// LoginOAuth runs the full authorization-code + PKCE loopback flow for provider
// name, using the oauth block configured for it. openURL is invoked with the
// built authorize URL so each surface decides how to surface it (launch a
// browser, print it, etc.). It blocks until the callback arrives or the context
// / five-minute budget fires — never forever.
func LoginOAuth(ctx context.Context, name string, openURL func(string) error) (Tokens, error) {
	var none Tokens
	cfg, err := config.Load()
	if err != nil {
		return none, fmt.Errorf("读取配置失败: %w", err)
	}
	pc := cfg.Providers[name]
	fc, err := resolveEndpoints(ctx, pc.OAuth)
	if err != nil {
		return none, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return none, fmt.Errorf("无法监听本地回调端口: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("%s:%d/callback", fc.RedirectBase, port)

	verifier, err := newVerifier()
	if err != nil {
		return none, fmt.Errorf("生成 PKCE verifier 失败: %w", err)
	}
	state, err := newState()
	if err != nil {
		return none, fmt.Errorf("生成 state 失败: %w", err)
	}

	result := make(chan callbackResult, 1)
	srv := &http.Server{Handler: callbackHandler(result, state)}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {fc.ClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challengeS256(verifier)},
		"code_challenge_method": {"S256"},
	}
	if fc.Scopes != "" {
		q.Set("scope", fc.Scopes)
	}
	authorizeURL := appendQuery(fc.AuthorizeURL, q)
	if openURL != nil {
		if err := openURL(authorizeURL); err != nil {
			return none, fmt.Errorf("打开授权页面失败: %w", err)
		}
	}

	timer := time.NewTimer(oauthTimeout)
	defer timer.Stop()
	var cb callbackResult
	select {
	case cb = <-result:
	case <-ctx.Done():
		return none, fmt.Errorf("等待登录回调被取消: %w", ctx.Err())
	case <-timer.C:
		return none, fmt.Errorf("等待登录回调超时（5 分钟内未完成授权），请重新执行登录")
	}
	if cb.err != "" {
		return none, fmt.Errorf("厂商拒绝了授权: %s", cb.err)
	}
	if cb.state != state {
		return none, fmt.Errorf("state 校验失败，可能是 CSRF，已丢弃此次回调")
	}
	if strings.TrimSpace(cb.code) == "" {
		return none, fmt.Errorf("回调没有携带授权码")
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {cb.code},
		"redirect_uri":  {redirectURI},
		"client_id":     {fc.ClientID},
		"code_verifier": {verifier},
	}
	body, err := guardedForm(ctx, fc.TokenURL, form)
	if err != nil {
		return none, fmt.Errorf("用授权码换取令牌失败: %w", err)
	}
	return parseTokens(body)
}

// appendQuery adds params to a base URL without clobbering an existing query.
func appendQuery(base string, q url.Values) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + q.Encode()
}

func callbackHandler(out chan<- callbackResult, wantState string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := callbackResult{state: q.Get("state")}
		switch {
		case q.Get("error") != "":
			res.err = q.Get("error")
			if d := q.Get("error_description"); d != "" {
				res.err += "：" + d
			}
		default:
			res.code = q.Get("code")
		}
		// Nonce: any response consumes the one-shot listener.
		select {
		case out <- res:
		default:
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != "" || res.code == "" || res.state != wantState {
			_, _ = io.WriteString(w, "<h3>登录未完成</h3><p>请回到终端查看错误信息，可关闭此窗口。</p>")
			return
		}
		_, _ = io.WriteString(w, "<h3>登录完成</h3><p>可以关闭此窗口，回到 iCode 继续。</p>")
	})
}

// tokenResponse is the shared shape of an OAuth token / refresh response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
}

func parseTokens(body []byte) (Tokens, error) {
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Tokens{}, fmt.Errorf("解析令牌响应失败: %w", err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return Tokens{}, fmt.Errorf("令牌响应缺少 access_token")
	}
	t := Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Account:      accountFromIDToken(tr.IDToken),
	}
	if tr.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().UTC().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return t, nil
}

// accountFromIDToken decodes the email claim from a JWT id_token. It never
// verifies the signature — the id_token came straight from the token endpoint
// over TLS and is used only for a human-readable status label.
func accountFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	if claims.Email != "" {
		return claims.Email
	}
	return claims.Sub
}

// grantLocks guards refreshes per provider so concurrent requesters cannot each
// fire a refresh round-trip and race on the stored token.
var grantLocks sync.Map // provider name -> *sync.Mutex

func lockFor(name string) *sync.Mutex {
	m, _ := grantLocks.LoadOrStore(name, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// SaveGrant persists a successful subscription login: the access token rides the
// existing encrypted api_key path (so any Bearer provider can use it), while the
// refresh token and expiry land in the encrypted grant.
func SaveGrant(name string, t Tokens) (Result, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Result{}, fmt.Errorf("未指定提供商")
	}
	if strings.TrimSpace(t.AccessToken) == "" {
		return Result{}, fmt.Errorf("令牌缺少 access_token")
	}
	cfg, err := config.Load()
	if err != nil {
		return Result{}, fmt.Errorf("读取配置失败: %w", err)
	}
	pc := cfg.Providers[name]
	pc.APIKey = t.AccessToken
	pc.Grant = &config.OAuthGrant{RefreshToken: t.RefreshToken, Account: t.Account}
	if !t.ExpiresAt.IsZero() {
		pc.Grant.ExpiresAt = t.ExpiresAt.UTC().Unix()
	}
	cfg.Providers[name] = pc
	path := config.DefaultPath()
	if err := cfg.Save(path); err != nil {
		return Result{}, fmt.Errorf("写入配置失败: %w", err)
	}
	return Result{Provider: name, Path: path, Masked: Masked(t.AccessToken)}, nil
}

// Refresh renews the access token when it is expired or about to be, using the
// stored refresh token. It returns the access token plus whether it is an
// OAuth bearer token (false for API-key-only providers), which is what tells
// a provider like Anthropic whether the credential travels in
// `Authorization: Bearer` or in x-api-key. The hot path calls it after a 401;
// only one refresh runs per provider at a time, and racers reuse the rotated
// token instead of firing a second round-trip.
func Refresh(ctx context.Context, name string) (string, bool, error) {
	mu := lockFor(name)
	mu.Lock()
	defer mu.Unlock()

	cfg, err := config.Load()
	if err != nil {
		return "", false, fmt.Errorf("读取配置失败: %w", err)
	}
	pc := cfg.Providers[name]
	if pc.Grant == nil || strings.TrimSpace(pc.Grant.RefreshToken) == "" || pc.OAuth == nil {
		return pc.APIKey, false, nil // not an OAuth login, or nothing to refresh with
	}
	if pc.Grant.ExpiresAt > 0 && time.Now().UTC().Add(refreshSkew).Unix() < pc.Grant.ExpiresAt {
		return pc.APIKey, true, nil // still comfortably fresh
	}
	if err := netsec.ValidateConfiguredURL(pc.OAuth.TokenURL); err != nil {
		return "", false, err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {pc.Grant.RefreshToken},
		"client_id":     {strings.TrimSpace(pc.OAuth.ClientID)},
	}
	body, err := guardedForm(ctx, pc.OAuth.TokenURL, form)
	if err != nil {
		return "", false, fmt.Errorf("刷新令牌失败: %w", err)
	}
	t, err := parseTokens(body)
	if err != nil {
		return "", false, err
	}
	if t.RefreshToken == "" {
		t.RefreshToken = pc.Grant.RefreshToken // vendors may omit a rotated one
	}
	if _, err := SaveGrant(name, t); err != nil {
		return "", false, err
	}
	return t.AccessToken, true, nil
}

// Status renders one line per provider's credential state for /status and
// keyStatus, distinguishing a subscription login (with expiry) from a bare key.
func Status(name string) string {
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	pc := cfg.Providers[name]
	exp, account, ok := pc.Subscription()
	if !ok {
		return ""
	}
	if exp.IsZero() {
		return fmt.Sprintf("已用订阅账号登录（%s，无到期时间）", firstNonEmpty(account, name))
	}
	return fmt.Sprintf("已用订阅账号登录（%s，到期 %s）", firstNonEmpty(account, name), exp.Local().Format("2006-01-02 15:04"))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// truncate clips long vendor error bodies before they reach the user.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// OpenBrowser launches the system browser at URL. It is best-effort: a failure
// to spawn (headless host, no DISPLAY) is returned so the caller can fall back
// to printing the URL for the user to paste.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
