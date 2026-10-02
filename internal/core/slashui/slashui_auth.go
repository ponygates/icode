package slashui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/auth"
)

// cmdLogin stores a provider credential from the chat surface and proves it
// works against the vendor.
//
// The desktop and VS Code surfaces have a settings form for keys, so a bare
// `/login` lists what is configured and points there. Passing the key inline
// (`/login <provider> <key>`) does the whole job without leaving chat — the
// path that matters for a headless session or a remote machine.
func cmdLogin(b *Backend, args []string, ctx context.Context) Result {
	if len(args) > 0 && strings.EqualFold(args[0], "oauth") {
		return cmdLoginOAuth(b, args[1:], ctx)
	}
	if len(args) == 0 {
		return ok("用法: /login <provider> <api-key>\n用法: /login oauth <provider>（订阅账号登录）\n\n" + keyStatus())
	}
	if len(args) < 2 {
		return errf("用法: /login %s <api-key>", args[0])
	}
	provider := args[0]
	key := strings.Join(args[1:], " ")
	res, err := auth.Save(provider, key)
	if err != nil {
		return errf("保存失败: %v", err)
	}
	// Push the new key into the live provider so the next message uses it
	// without a restart (optional — surfaces without a registry reload on next
	// config load instead).
	var pushed string
	if b != nil && b.SetCredentials != nil {
		pushed = b.SetCredentials(provider, key)
	}
	v := auth.Verify(ctx, provider)
	var bld strings.Builder
	fmt.Fprintf(&bld, "✓ 已保存 %s 的 API Key（%s）→ %s\n", res.Provider, res.Masked, res.Path)
	if pushed != "" {
		fmt.Fprintf(&bld, "· 实时更新提供商失败: %s\n", pushed)
	}
	switch {
	case v.Skipped != "":
		fmt.Fprintf(&bld, "· 未验证: %s", v.Skipped)
	case v.OK:
		fmt.Fprintf(&bld, "✓ 连通性验证通过：%d 个模型可用（%dms），首个: %s", v.ModelN, v.LatencyMS, v.Sample)
	default:
		fmt.Fprintf(&bld, "✗ 连通性验证失败: %s\nKey 已保存；请核对 Key 与 API 地址（/doctor 查看状态）。", v.Err)
	}
	return ok(bld.String())
}

// cmdLogout removes a stored credential. With no argument it clears the
// session's current provider, since that is the one a user typing `/logout`
// after a failed login almost always means.
func cmdLogout(b *Backend, args []string, st *State) Result {
	provider := ""
	if len(args) > 0 {
		provider = strings.TrimSpace(args[0])
	} else if st != nil {
		provider = strings.TrimSpace(st.Provider)
	}
	if provider == "" {
		return errf("用法: /logout <provider>")
	}
	res, err := auth.Clear(provider)
	if err != nil {
		return errf("清除失败: %v", err)
	}
	if b != nil && b.SetCredentials != nil {
		b.SetCredentials(provider, "")
	}
	if !res.Cleared {
		return ok(provider + " 没有已保存的 API Key，无需清除。")
	}
	return ok("✓ 已清除 " + provider + " 的 API Key → " + res.Path)
}

// keyStatus renders the per-vendor credential state for /login's help text, so
// the command that sets keys also shows which ones are already set.
func keyStatus() string {
	cfg, err := config.Load()
	if err != nil {
		return "（配置文件读取失败: " + err.Error() + "）"
	}
	names := map[string]bool{}
	for name := range cfg.Providers {
		names[name] = true
	}
	for _, name := range auth.BuiltinNames() {
		names[name] = true
	}
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	var b strings.Builder
	set := 0
	for _, name := range list {
		mark := "·"
		if strings.TrimSpace(cfg.Providers[name].APIKey) != "" {
			mark = "✓"
			set++
		}
		fmt.Fprintf(&b, "  %s %s", mark, name)
		if sub := auth.Status(name); sub != "" {
			fmt.Fprintf(&b, " — %s", sub)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n已配置 %d 个，未配置 %d 个。", set, len(list)-set)
	return b.String()
}

// cmdLoginOAuth performs a subscription-account login (`/login oauth <provider>`)
// from the chat surface. It opens the vendor's authorize page in the local
// browser and waits on iCode's loopback callback, then stores the tokens
// encrypted (access token via the api_key path, refresh token in the grant).
// A headless surface that cannot launch a browser is told to use the CLI.
func cmdLoginOAuth(b *Backend, args []string, ctx context.Context) Result {
	provider := ""
	if len(args) > 0 {
		provider = strings.TrimSpace(args[0])
	}
	if provider == "" {
		return errf("用法: /login oauth <provider>")
	}
	cfg, err := config.Load()
	if err != nil {
		return errf("读取配置失败: %v", err)
	}
	if cfg.Providers[provider].OAuth == nil {
		return errf("%s 未配置订阅登录：请在 config.yaml 的 providers.%s.oauth 下填写厂商官方的 authorize_url / token_url（或 discovery_url）", provider, provider)
	}
	openErr := ""
	open := func(u string) error {
		if err := auth.OpenBrowser(u); err != nil {
			openErr = fmt.Sprintf("无法自动打开浏览器，请在本地终端运行: icode login --oauth %s\n授权地址：%s", provider, u)
			return err
		}
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	tok, err := auth.LoginOAuth(runCtx, provider, open)
	if err != nil {
		if openErr != "" {
			return errf("%s", openErr)
		}
		return errf("订阅登录失败: %v", err)
	}
	res, err := auth.SaveGrant(provider, tok)
	if err != nil {
		return errf("保存令牌失败: %v", err)
	}
	if b != nil && b.SetCredentials != nil {
		b.SetCredentials(provider, tok.AccessToken)
	}
	v := auth.Verify(ctx, provider)
	var bld strings.Builder
	fmt.Fprintf(&bld, "✓ 已用订阅账号登录 %s（%s）→ %s\n", res.Provider, res.Masked, res.Path)
	if s := auth.Status(provider); s != "" {
		fmt.Fprintf(&bld, "· %s\n", s)
	}
	switch {
	case v.Skipped != "":
		fmt.Fprintf(&bld, "· 未验证: %s", v.Skipped)
	case v.OK:
		fmt.Fprintf(&bld, "✓ 连通性验证通过：%d 个模型可用（%dms），首个: %s", v.ModelN, v.LatencyMS, v.Sample)
	default:
		fmt.Fprintf(&bld, "✗ 连通性验证失败: %s\n订阅令牌仅对使用 Authorization: Bearer 的兼容网关即时生效。", v.Err)
	}
	return ok(bld.String())
}
