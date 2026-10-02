package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/auth"
)

// promptState is a modal one-line question drawn over the input box. It exists
// because /login needs to read a secret: routing keys through a dedicated
// handler keeps the API key out of the input buffer, out of shell history and
// out of the conversation log, while the echo is masked.
type promptState struct {
	label    string
	secret   bool
	buf      string
	onDone   func(text string)
	onCancel func()
}

// promptActive reports whether the modal prompt is waiting for input.
func (t *TUI) promptActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.prompt != nil
}

// startPrompt opens the modal prompt. Any text already on the input line is
// parked and restored on cancel, mirroring the search overlay.
func (t *TUI) startPrompt(label string, secret bool, onDone func(string), onCancel func()) {
	t.mu.Lock()
	p := &promptState{label: label, secret: secret, onDone: onDone, onCancel: onCancel}
	if t.prompt == nil {
		t.restoreInput = t.inputBuf
	}
	t.prompt = p
	t.inputBuf = ""
	t.cursor = 0
	t.acOpen = false
	t.acItems = nil
	t.mu.Unlock()
	t.scheduleRender()
}

// resolvePrompt closes the prompt and hands the answer to its callback.
func (t *TUI) resolvePrompt(text string, cancelled bool) {
	t.mu.Lock()
	p := t.prompt
	t.prompt = nil
	restore := t.restoreInput
	t.mu.Unlock()
	if p == nil {
		return
	}
	if cancelled {
		if p.onCancel != nil {
			p.onCancel()
			return
		}
		t.mu.Lock()
		t.inputBuf = restore
		t.cursor = len([]rune(restore))
		t.restoreInput = ""
		t.mu.Unlock()
		t.scheduleRender()
		return
	}
	p.onDone(text)
}

// handlePromptKey routes every key while the modal prompt is open.
func (t *TUI) handlePromptKey(r rune) bool {
	switch r {
	case 0x1b, 0x03: // Esc / Ctrl+C — cancel
		t.resolvePrompt("", true)
		return true
	case '\r', '\n':
		t.mu.Lock()
		p := t.prompt
		var text string
		if p != nil {
			text = strings.TrimSpace(p.buf)
		}
		t.mu.Unlock()
		if p == nil || text == "" {
			return true
		}
		t.resolvePrompt(text, false)
		return true
	case 0x7f, 0x08: // Backspace / DEL
		t.mu.Lock()
		if p := t.prompt; p != nil && len([]rune(p.buf)) > 0 {
			p.buf = string([]rune(p.buf)[:len([]rune(p.buf))-1])
		}
		t.mu.Unlock()
		t.scheduleRender()
		return true
	case 0x15: // Ctrl+U — clear the line
		t.mu.Lock()
		if p := t.prompt; p != nil {
			p.buf = ""
		}
		t.mu.Unlock()
		t.scheduleRender()
		return true
	}
	if r < 0x20 {
		return true // swallow other control characters
	}
	t.mu.Lock()
	if p := t.prompt; p != nil {
		p.buf += string(r)
	}
	t.mu.Unlock()
	t.scheduleRender()
	return true
}

// drawPromptBox renders the modal prompt anchored to the bottom rows, the same
// geometry the search overlay uses so the two never fight over the input line.
func (t *TUI) drawPromptBox(W, H int) string {
	t.mu.Lock()
	p := t.prompt
	var label, shown string
	if p != nil {
		label = p.label
		if p.secret {
			// Echo the count, never the characters.
			shown = strings.Repeat("*", len([]rune(p.buf)))
		} else {
			shown = p.buf
		}
	}
	t.mu.Unlock()
	if p == nil {
		return ""
	}

	topRow := H
	if t.statusVisible {
		topRow = H - 1
	}
	if topRow < 1 {
		topRow = 1
	}
	innerW := W - 4
	if innerW < 8 {
		innerW = 8
	}
	if visibleWidth(shown) > innerW {
		shown = truncVisible(shown, innerW)
	}

	var b strings.Builder
	for row := topRow; row <= H; row++ {
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", row))
	}
	b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow))
	b.WriteString(t.paint("yellow", label) + t.paint("cyan", shown))
	if t.statusVisible && topRow+1 <= H {
		b.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[K", topRow+1))
		b.WriteString(t.paint("dim", "  Enter 确认 · Esc 取消"))
	}
	return b.String()
}

// knownProviders is the union of vendors iCode ships with and vendors the user
// has configured, sorted — the suggestion list /login prints and completes.
func knownProviders() []string {
	set := map[string]bool{}
	if cfg, err := config.Load(); err == nil {
		for name := range cfg.Providers {
			set[name] = true
		}
	}
	for _, name := range auth.BuiltinNames() {
		set[name] = true
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// providerHasKey reports whether a vendor has a stored credential.
func providerHasKey(name string) bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	return strings.TrimSpace(cfg.Providers[name].APIKey) != ""
}

// scrubHistory drops every persisted prompt that mentions prefix — including
// one that embeds it, such as a ↑-recall edited and resubmitted — so
// `/login <provider> <key>` cannot be recalled with ↑ or read back off
// ~/.icode/input_history.json.
func (t *TUI) scrubHistory(prefix string) {
	t.mu.Lock()
	kept := make([]string, 0, len(t.history))
	for _, s := range t.history {
		if strings.Contains(s, prefix) {
			continue
		}
		kept = append(kept, s)
	}
	t.history = kept
	t.histIdx = -1
	snapshot := append([]string(nil), kept...)
	t.mu.Unlock()
	saveInputHistory(snapshot)
}

// loginCommand implements /login: store a credential and prove it works.
//
//	/login                      → interactive provider + masked key prompt
//	/login <provider>           → interactive masked key prompt for that vendor
//	/login <provider> <key>     → non-interactive (scripting, line mode)
func (t *TUI) loginCommand(args []string) {
	if len(args) > 0 && strings.EqualFold(args[0], "oauth") {
		// Subscription-account login (authorization-code + PKCE via browser),
		// the counterpart to the inline `<provider> <key>` path below.
		t.loginOAuth(args[1:])
		return
	}
	if len(args) >= 2 {
		// The typed line already went into the (persisted) prompt history; drop
		// it so an inline key cannot be recalled with ↑ or read off disk.
		t.scrubHistory("/login")
		t.saveAndVerifyKey(args[0], strings.Join(args[1:], " "))
		return
	}
	if !t.rawMode {
		// Line mode has no modal key prompt; the interactive flow would have to
		// echo the key, which is exactly what this command must not do.
		t.add(RoleSystem, "终端行模式无法安全隐藏输入，请用：/login <provider> <key>\n可用提供商: "+strings.Join(knownProviders(), ", "))
		return
	}
	if len(args) == 1 {
		t.askForKey(args[0])
		return
	}
	def := t.provider
	hint := "输入提供商名称（回车默认 " + def + "）"
	if def != "" && !providerHasKey(def) {
		hint += " · " + def + " 尚未配置 Key"
	}
	t.startPrompt(hint+": ", false, func(text string) {
		provider := text
		if provider == "" {
			provider = def
		}
		t.askForKey(provider)
	}, nil)
}

// credPusher is implemented by backends that can inject a freshly saved key
// into their live providers. Asserting it optionally keeps every other
// Callback implementation (tests, simple UIs) compiling unchanged; those
// surfaces pick the new key up on the next config load instead.
type credPusher interface {
	OnCredentialsSaved(provider, apiKey string)
}

// pushCredentials hands the new key to the backend so the change takes effect
// without restarting the session.
func (t *TUI) pushCredentials(provider, apiKey string) {
	if cp, ok := t.callback.(credPusher); ok {
		cp.OnCredentialsSaved(provider, apiKey)
	}
}

// askForKey opens the masked key prompt for a provider. The answer goes
// straight into the encrypted config file — never into the conversation.
func (t *TUI) askForKey(provider string) {
	t.startPrompt("API Key for "+provider+"（输入不会显示）: ", true, func(key string) {
		t.saveAndVerifyKey(provider, key)
	}, func() {
		t.add(RoleSystem, "已取消，未保存凭据。")
	})
}

// saveAndVerifyKey writes the credential and then probes the vendor, so a
// mistyped key is reported now rather than at the first failed chat.
func (t *TUI) saveAndVerifyKey(provider, key string) {
	res, err := auth.Save(provider, key)
	if err != nil {
		t.add(RoleError, "保存失败: "+err.Error())
		return
	}
	t.pushCredentials(provider, key)
	t.add(RoleSystem, fmt.Sprintf("✓ 已保存 %s 的 API Key（%s）→ %s\n正在验证连通性…", res.Provider, res.Masked, res.Path))
	go func() {
		v := auth.Verify(context.Background(), provider)
		switch {
		case v.Skipped != "":
			t.add(RoleSystem, "已保存，但未验证: "+v.Skipped)
		case v.OK:
			t.add(RoleSystem, fmt.Sprintf("✓ 连通性验证通过：%d 个模型可用（%dms），首个: %s", v.ModelN, v.LatencyMS, v.Sample))
		default:
			t.add(RoleError, "✗ 连通性验证失败: "+v.Err+"\nKey 已保存；可用 /keys 查看状态，或用 /login 重新输入。")
		}
	}()
}

// logoutCommand implements /logout: remove a stored credential behind an
// explicit confirmation, since a mistyped logout would silently break chat.
func (t *TUI) logoutCommand(args []string) {
	provider := strings.TrimSpace(t.provider)
	if len(args) > 0 {
		provider = strings.TrimSpace(args[0])
	}
	if provider == "" {
		t.add(RoleError, "未指定提供商。用法: /logout <provider>")
		return
	}
	key := ""
	if cfg, err := config.Load(); err == nil {
		key = cfg.Providers[provider].APIKey
	}
	if strings.TrimSpace(key) == "" {
		t.add(RoleSystem, provider+" 没有已保存的 API Key，无需清除。")
		return
	}
	confirm := fmt.Sprintf("确认清除 %s（%s）的 API Key？输入 yes: ", provider, auth.Masked(key))
	t.startPrompt(confirm, false, func(text string) {
		if !strings.EqualFold(text, "yes") {
			t.add(RoleSystem, "未确认，已保留凭据。")
			return
		}
		res, err := auth.Clear(provider)
		if err != nil {
			t.add(RoleError, "清除失败: "+err.Error())
			return
		}
		t.pushCredentials(provider, "")
		if res.Cleared {
			t.add(RoleSystem, "✓ 已清除 "+provider+" 的 API Key → "+res.Path)
		} else {
			t.add(RoleSystem, provider+" 没有已保存的 API Key。")
		}
	}, func() {
		t.add(RoleSystem, "已取消，凭据未改动。")
	})
}

// loginOAuth drives the subscription-account branch of /login: `/login oauth
// [provider]`. The provider is chosen through the same modal prompt the API-key
// flow uses, so no new input channel is introduced.
func (t *TUI) loginOAuth(args []string) {
	if len(args) >= 1 {
		t.startOAuthLogin(args[0])
		return
	}
	def := t.provider
	hint := "用哪个提供商进行订阅登录（回车默认 " + def + "）"
	t.startPrompt(hint+": ", false, func(text string) {
		provider := text
		if provider == "" {
			provider = def
		}
		t.startOAuthLogin(provider)
	}, func() {
		t.add(RoleSystem, "已取消，未登录。")
	})
}

// startOAuthLogin runs the browser + loopback-callback flow off the UI thread,
// printing the authorize URL as a system message so the user can open it
// manually if the automatic launch fails.
func (t *TUI) startOAuthLogin(provider string) {
	if strings.TrimSpace(provider) == "" {
		t.add(RoleError, "未指定提供商。用法: /login oauth <provider>")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		t.add(RoleError, "读取配置失败: "+err.Error())
		return
	}
	if cfg.Providers[provider].OAuth == nil {
		t.add(RoleError, provider+" 未配置订阅登录：请在 config.yaml 的 providers."+provider+".oauth 下填写厂商官方的 authorize_url / token_url（或 discovery_url）")
		return
	}
	t.add(RoleSystem, "正在发起 "+provider+" 的订阅登录…")
	go func() {
		open := func(u string) error {
			t.add(RoleSystem, "请在浏览器中打开以下地址完成授权：\n"+u)
			_ = auth.OpenBrowser(u)
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		tok, err := auth.LoginOAuth(ctx, provider, open)
		if err != nil {
			t.add(RoleError, "✗ 订阅登录失败: "+err.Error())
			return
		}
		res, err := auth.SaveGrant(provider, tok)
		if err != nil {
			t.add(RoleError, "保存令牌失败: "+err.Error())
			return
		}
		t.pushCredentials(provider, tok.AccessToken)
		status := auth.Status(provider)
		if status != "" {
			t.add(RoleSystem, "✓ "+status)
		}
		t.add(RoleSystem, fmt.Sprintf("✓ 已用订阅账号登录 %s（%s）→ %s\n正在验证连通性…", res.Provider, res.Masked, res.Path))
		v := auth.Verify(context.Background(), provider)
		switch {
		case v.Skipped != "":
			t.add(RoleSystem, "已登录，但未验证: "+v.Skipped)
		case v.OK:
			t.add(RoleSystem, fmt.Sprintf("✓ 连通性验证通过：%d 个模型可用（%dms），首个: %s", v.ModelN, v.LatencyMS, v.Sample))
		default:
			t.add(RoleError, "✗ 连通性验证失败: "+v.Err+"\n令牌已保存；订阅令牌仅对使用 Authorization: Bearer 的兼容网关即时生效。")
		}
	}()
}
