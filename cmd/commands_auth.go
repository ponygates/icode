package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/auth"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// loginCmd is the interactive entry point for credentials: it asks for the
// vendor, then reads the key with echo disabled, saves it encrypted, and
// verifies it against the vendor before reporting success.
//
// It exists because `icode auth set --key <k>` puts the secret in the shell
// history and in any CI log, and because nothing used to tell the user that
// the key they just entered was wrong.
var loginCmd = &cobra.Command{
	Use:   "login [provider] [key]",
	Short: "配置提供商 API Key（交互输入，不回显）",
	Long: `Save a provider API key and verify it.

  icode login                     ask for provider, then for the key (masked)
  icode login <provider>          ask for the key for that provider (masked)
  icode login <provider> <key>    non-interactive (scripting; prefer the masked
                                  prompt when a terminal is available)

For automation set ICODE_API_KEY instead of passing the key as an argument —
argv is visible in shell history and in process listings.

The key is stored encrypted at rest in the user config file.

Use --oauth to sign in with a subscription account instead of an API key
(authorization-code + PKCE through your browser). This requires the vendor's
official OAuth endpoints to be configured under providers.<name>.oauth — iCode
does not ship guessed values, because Anthropic/OpenAI do not publish them.`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := ""
		if len(args) > 0 {
			provider = args[0]
		}
		key := ""
		if len(args) > 1 {
			key = args[1]
		}
		noVerify, _ := cmd.Flags().GetBool("no-verify")
		envKey := strings.TrimSpace(os.Getenv("ICODE_API_KEY"))

		if useOAuth, _ := cmd.Flags().GetBool("oauth"); useOAuth {
			return oauthLogin(cmd, provider)
		}

		if provider == "" {
			p, err := promptLine(cmd, "提供商 (provider，可用: "+providerHint()+"): ")
			if err != nil {
				return err
			}
			provider = p
		}
		if key == "" {
			if envKey != "" {
				// Non-interactive callers pass the secret through the
				// environment, never through argv (which lands in shell history
				// and in `ps`).
				key = envKey
			} else {
				k, err := promptSecret(cmd, fmt.Sprintf("%s 的 API Key（输入不回显）: ", provider))
				if err != nil {
					return err
				}
				key = k
			}
		}

		res, err := auth.Save(provider, key)
		if err != nil {
			return err
		}
		fmt.Printf("✓ 已保存 %s 的 API Key（%s）→ %s\n", res.Provider, res.Masked, res.Path)
		if noVerify {
			return nil
		}
		return verifyAndReport(provider)
	},
}

// logoutCmd removes a stored credential.
var logoutCmd = &cobra.Command{
	Use:   "logout [provider]",
	Short: "清除某个提供商的 API Key",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := ""
		if len(args) > 0 {
			provider = args[0]
		} else {
			p, err := promptLine(cmd, "要清除哪个提供商? ")
			if err != nil {
				return err
			}
			provider = p
		}
		res, err := auth.Clear(provider)
		if err != nil {
			return err
		}
		if !res.Cleared {
			fmt.Println(provider + " 没有已保存的 API Key。")
			return nil
		}
		fmt.Println("✓ 已清除 " + provider + " 的 API Key → " + res.Path)
		return nil
	},
}

// verifyAndReport asks the vendor what the saved key can actually use. A
// network round-trip is bounded by auth.Verify's own deadline.
func verifyAndReport(provider string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	v := auth.Verify(ctx, provider)
	switch {
	case v.Skipped != "":
		fmt.Println("· 未验证: " + v.Skipped)
	case v.OK:
		fmt.Printf("✓ 连通性验证通过：%d 个模型可用（%dms），首个: %s\n", v.ModelN, v.LatencyMS, v.Sample)
	default:
		fmt.Printf("✗ 连通性验证失败: %s\n", v.Err)
		fmt.Println("  Key 已保存；可用 `icode auth -L` 查看状态。")
		return fmt.Errorf("provider %s 验证失败", provider)
	}
	return nil
}

// promptSecret reads a credential with terminal echo disabled. When stdin is
// not a terminal there is no safe way to prompt, so it refuses rather than
// asking the user to paste a secret into a logged stream.
func promptSecret(cmd *cobra.Command, label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("当前不是交互终端：请作为参数传入 Key，或设置环境变量 ICODE_API_KEY")
	}
	fmt.Fprint(cmd.OutOrStderr(), label)
	buf, err := term.ReadPassword(fd)
	fmt.Fprintln(cmd.OutOrStderr())
	if err != nil {
		return "", fmt.Errorf("读取 Key 失败: %w", err)
	}
	return strings.TrimSpace(string(buf)), nil
}

// promptLine reads a non-secret answer (provider name) with echo on. It pulls
// one byte at a time so it never consumes past the newline — a buffered read
// here would swallow the following masked key prompt's input.
func promptLine(cmd *cobra.Command, label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("当前不是交互终端：请把提供商作为参数传入")
	}
	fmt.Fprint(cmd.OutOrStderr(), label)
	var buf []byte
	one := make([]byte, 1)
	for len(buf) <= 256 {
		n, err := os.Stdin.Read(one)
		if err != nil || n == 0 {
			if len(buf) == 0 {
				return "", fmt.Errorf("读取输入失败: %v", err)
			}
			break
		}
		switch one[0] {
		case '\n':
			return strings.TrimSpace(string(buf)), nil
		case '\r':
			continue
		default:
			buf = append(buf, one[0])
		}
	}
	return strings.TrimSpace(string(buf)), nil
}

// providerHint lists configured + built-in vendors for the interactive prompt.
func providerHint() string {
	set := map[string]bool{}
	if cfg, err := config.Load(); err == nil {
		for name := range cfg.Providers {
			set[name] = true
		}
	}
	for _, n := range auth.BuiltinNames() {
		set[n] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// oauthLogin runs the subscription-account (authorization-code + PKCE) flow for
// a provider whose providers.<name>.oauth block is configured. It needs a
// terminal because it opens a browser and waits on a local callback; on a
// non-TTY it refuses and points at the token-import path instead of hanging.
func oauthLogin(cmd *cobra.Command, provider string) error {
	fd := int(os.Stdin.Fd())
	if provider == "" {
		if !term.IsTerminal(fd) {
			return fmt.Errorf("非交互终端下无法进行订阅登录：请显式指定提供商")
		}
		p, err := promptLine(cmd, "用哪个提供商进行订阅登录 (可用: "+providerHint()+"): ")
		if err != nil {
			return err
		}
		provider = p
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}
	if cfg.Providers[provider].OAuth == nil {
		return fmt.Errorf("%s 未配置订阅登录：请在 config.yaml 的 providers.%s.oauth 下填写厂商官方的 authorize_url / token_url（或 discovery_url）", provider, provider)
	}
	if !term.IsTerminal(fd) {
		return fmt.Errorf("订阅登录需要打开浏览器并等待本地回调，非交互终端下无法进行；请在交互终端运行，或用 `icode login %s <access-token>` 导入已有访问令牌", provider)
	}
	open := func(u string) error {
		fmt.Fprintf(cmd.OutOrStderr(), "请在浏览器中打开以下地址完成授权：\n  %s\n", u)
		if err := auth.OpenBrowser(u); err != nil {
			fmt.Fprintln(cmd.OutOrStderr(), "（无法自动打开浏览器，请手动复制上面的地址）")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	tok, err := auth.LoginOAuth(ctx, provider, open)
	if err != nil {
		return err
	}
	res, err := auth.SaveGrant(provider, tok)
	if err != nil {
		return err
	}
	fmt.Printf("✓ 已用订阅账号登录 %s（%s）→ %s\n", res.Provider, res.Masked, res.Path)
	return verifyAndReport(provider)
}
