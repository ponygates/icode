// Package tokenopt — Attachment eviction layer (multimodal token saving).
//
// Since v0.16 tool-generated images (image_gen, screenshots) are ingested
// back into the conversation as base64 attachments so vision models can see
// them. A single image is typically 100KB–1MB of base64 — re-sent on EVERY
// subsequent request. Unlike text, attachments were never folded by the
// Volatile Scratch pass, making them the single largest token/bandwidth
// waste in multimodal sessions.
//
// This layer fixes that with two mechanisms:
//
//  1. Eviction — when a new user turn starts, only the most recent
//     MaxKeptAttachments attachments are kept in the message log; older
//     ones are replaced with a short text placeholder. The model has
//     already seen and described them, so the placeholder plus the
//     assistant's own analysis preserves the semantic context at ~30
//     tokens instead of ~1500.
//
//  2. Estimation — EstimateAttachmentTokens gives image attachments a
//     realistic token weight so ShouldCompact() no longer underestimates
//     multimodal context and compaction triggers on time.
package tokenopt

import (
	"fmt"

	"github.com/ponygates/icode/internal/types"
)

// AttachmentEvictionConfig controls multimodal attachment eviction.
type AttachmentEvictionConfig struct {
	// Enabled turns the eviction pass on. Default: true.
	Enabled bool

	// MaxKeptAttachments is how many of the most recent attachments stay
	// in context. Older ones are replaced with placeholders. Default: 2.
	MaxKeptAttachments int
}

// DefaultAttachmentEvictionConfig returns sensible defaults.
func DefaultAttachmentEvictionConfig() AttachmentEvictionConfig {
	return AttachmentEvictionConfig{
		Enabled:            true,
		MaxKeptAttachments: 2,
	}
}

// EstimateAttachmentTokens estimates the token cost of one attachment.
//
// Providers charge images by resolution tiles, which we cannot know from
// base64 alone. Compressed image size correlates well enough: we decode the
// base64 length to raw bytes and use ~750 raw bytes/token (a 500KB PNG ≈
// 1400 tokens under Anthropic's (w*h)/750 rule; empirically ~700–800 bytes
// per token for typical screenshots). Clamped to [85, 2000] — the floor
// matches the minimum image cost on OpenAI-style providers, the ceiling
// caps pathological inputs.
func EstimateAttachmentTokens(att types.Attachment) int {
	rawBytes := len(att.Data) * 3 / 4
	t := rawBytes / 750
	if t < 85 {
		t = 85
	}
	if t > 2000 {
		t = 2000
	}
	return t
}

// evictAttachmentsLocked walks the message log from newest to oldest and
// keeps only the most recent MaxKeptAttachments attachments. Older ones are
// dropped and a text placeholder is appended to the owning message so the
// model knows an attachment existed there.
//
// MUST be called with o.mu held.
func (o *Optimizer) evictAttachmentsLocked() {
	if !o.attachCfg.Enabled {
		return
	}

	kept := 0
	for i := len(o.messageLog) - 1; i >= 0; i-- {
		m := &o.messageLog[i]
		if len(m.Attachments) == 0 {
			continue
		}

		// Newest-first budget: keep whole messages until the budget is
		// spent, then evict everything older.
		if kept+len(m.Attachments) <= o.attachCfg.MaxKeptAttachments {
			kept += len(m.Attachments)
			continue
		}

		// Evict all attachments on this message.
		saved := 0
		for _, att := range m.Attachments {
			saved += EstimateAttachmentTokens(att)
			sizeKB := len(att.Data) * 3 / 4 / 1024
			m.Content += fmt.Sprintf(
				"\n[附件已淘汰以节省 token：%s ~%dKB，模型此前已阅览]",
				att.MIMEType, sizeKB,
			)
		}
		m.Attachments = nil
		o.stats.TokensSaved += saved
		o.stats.AttachmentsEvicted++
	}
}
