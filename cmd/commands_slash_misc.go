package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/core/voice"
	"github.com/ponygates/icode/internal/tui"
)

// slashThinking implements /thinking <on|off|<tokens>> (extended thinking).
func (c *chatCallback) slashThinking(args []string) {
	if c.app == nil || c.app.Engine == nil {
		c.tui.AddMessage(tui.RoleSystem, "引擎未初始化。")
		return
	}
	if len(args) == 0 {
		if b := c.app.Engine.ThinkingBudget(); b > 0 {
			c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("当前 extended thinking 已开启，预算 %d tokens。用法: /thinking <on|off|<tokens>>", b))
		} else {
			c.tui.AddMessage(tui.RoleSystem, "当前 extended thinking 已关闭。用法: /thinking <on|off|<tokens>>（如 /thinking 4096，对 Anthropic 模型生效）")
		}
		return
	}
	budget := 0
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on":
		budget = 4096
	case "off", "0":
		budget = 0
	default:
		n, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || n < 1024 {
			c.tui.AddMessage(tui.RoleSystem, "无效预算（至少 1024 tokens）。用法: /thinking <on|off|<tokens>>")
			// NOTE: this break leaves the budget switch only — control then
			// continues into SetThinking(0) below, so an invalid budget both
			// prints the error AND turns thinking off (and persists it). The
			// pre-split code did exactly the same; kept verbatim, see report.
			break
		}
		budget = n
	}
	c.app.Engine.SetThinking(budget)
	if cfg, err := config.Load(); err == nil {
		cfg.Defaults.ThinkingTokens = budget
		_ = cfg.Save(config.DefaultPath())
	}
	if budget > 0 {
		c.tui.AddMessage(tui.RoleSystem, fmt.Sprintf("✓ extended thinking 已开启（预算 %d tokens，对 Anthropic 模型生效，已持久化）", budget))
	} else {
		c.tui.AddMessage(tui.RoleSystem, "✓ extended thinking 已关闭（已持久化）。")
	}
}

// slashVoice implements /voice: toggle mic capture, then transcribe via the
// configured ASR provider and submit the recognised text.
func (c *chatCallback) slashVoice() {
	// Toggle mic capture: first call starts recording, second stops and
	// transcribes via the configured ASR provider, then submits the text
	// as a message.
	if c.app == nil {
		c.tui.AddMessage(tui.RoleSystem, "语音输入暂不可用。")
		return
	}
	if c.voiceRec == nil {
		rec := voice.NewRecorder()
		if err := rec.Start(); err != nil {
			c.tui.AddMessage(tui.RoleSystem, "录音启动失败: "+err.Error())
			return
		}
		c.voiceRec = rec
		c.tui.AddMessage(tui.RoleSystem, "🎙 正在录音（最多30秒）…再次输入 /voice 结束并识别")
		return
	}
	wav, err := c.voiceRec.Stop()
	c.voiceRec = nil
	if err != nil {
		c.tui.AddMessage(tui.RoleSystem, "录音结束失败: "+err.Error())
		return
	}
	c.tui.AddMessage(tui.RoleSystem, "⏳ 正在识别语音…")

	// Determine provider from config
	provider := c.app.Cfg.Voice.Provider
	if provider == "" {
		provider = voice.ProviderZhipu
	}

	var text string
	var terr error

	switch provider {
	case voice.ProviderBaidu:
		apiKey := c.app.Cfg.Voice.BaiduAPIKey
		secretKey := c.app.Cfg.Voice.BaiduSecretKey
		text, terr = voice.TranscribeBaidu(context.Background(), apiKey, secretKey, wav)
	case voice.ProvideriFlytek:
		appID := c.app.Cfg.Voice.IFlytekAppID
		apiKey := c.app.Cfg.Voice.IFlytekAPIKey
		apiSecret := c.app.Cfg.Voice.IFlytekAPISecret
		text, terr = voice.TranscribeiFlytek(context.Background(), appID, apiKey, apiSecret, wav)
	default: // zhipu
		apiKey := c.app.Cfg.APIKey("zhipu")
		text, terr = voice.TranscribeZhipu(context.Background(), apiKey, wav, "voice.wav")
	}

	if terr != nil {
		c.tui.AddMessage(tui.RoleSystem, "识别失败: "+terr.Error())
		return
	}
	c.tui.AddMessage(tui.RoleSystem, "✅ 已识别:「"+text+"」")
	// Send immediately so the recognised text flows through the normal path.
	c.OnSend(text, nil)
}
