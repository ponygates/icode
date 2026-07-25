package tokenopt

import (
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

// fakeImage returns an attachment whose base64 payload is n bytes long.
func fakeImage(n int) types.Attachment {
	return types.Attachment{
		Type:     "image",
		MIMEType: "image/png",
		Data:     strings.Repeat("A", n),
	}
}

func newTestOptimizer() *Optimizer {
	return New(Config{
		ModelInfo:    types.ModelInfo{ContextWindow: 128000},
		SystemPrompt: "sys",
	})
}

func TestEstimateAttachmentTokens(t *testing.T) {
	// Floor: tiny image still costs at least 85 tokens.
	if got := EstimateAttachmentTokens(fakeImage(100)); got != 85 {
		t.Errorf("tiny image = %d tokens, want floor 85", got)
	}
	// Mid-size: 400KB base64 → 300KB raw → 400 tokens.
	if got := EstimateAttachmentTokens(fakeImage(400_000)); got != 400 {
		t.Errorf("400KB base64 = %d tokens, want 400", got)
	}
	// Ceiling: huge image is clamped to 2000.
	if got := EstimateAttachmentTokens(fakeImage(10_000_000)); got != 2000 {
		t.Errorf("huge image = %d tokens, want ceiling 2000", got)
	}
}

func TestAttachmentEvictionKeepsRecent(t *testing.T) {
	o := newTestOptimizer()

	// Three turns each producing one image attachment.
	for i := 0; i < 3; i++ {
		o.AddMessage(types.Message{Role: types.RoleUser, Content: "draw"})
		o.AddMessage(types.Message{Role: types.RoleAssistant, Content: "done"})
		o.AddMessage(types.Message{
			Role:        types.RoleUser,
			Content:     "（附件）",
			Attachments: []types.Attachment{fakeImage(500_000)},
		})
	}

	// A new user turn triggers eviction of everything beyond the newest 2.
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "next"})

	total := 0
	evictedPlaceholders := 0
	for _, m := range o.messageLog {
		total += len(m.Attachments)
		if strings.Contains(m.Content, "附件已淘汰") {
			evictedPlaceholders++
		}
	}
	if total != 2 {
		t.Errorf("kept %d attachments, want 2", total)
	}
	if evictedPlaceholders != 1 {
		t.Errorf("placeholders = %d, want 1", evictedPlaceholders)
	}

	st := o.Stats()
	if st.AttachmentsEvicted != 1 {
		t.Errorf("AttachmentsEvicted = %d, want 1", st.AttachmentsEvicted)
	}
	if st.TokensSaved <= 0 {
		t.Errorf("TokensSaved = %d, want > 0", st.TokensSaved)
	}
}

func TestAttachmentEvictionDisabled(t *testing.T) {
	o := New(Config{
		ModelInfo:    types.ModelInfo{ContextWindow: 128000},
		SystemPrompt: "sys",
		Attachment:   AttachmentEvictionConfig{Enabled: false, MaxKeptAttachments: 1},
	})

	for i := 0; i < 3; i++ {
		o.AddMessage(types.Message{
			Role:        types.RoleUser,
			Content:     "img",
			Attachments: []types.Attachment{fakeImage(500_000)},
		})
	}
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "next"})

	total := 0
	for _, m := range o.messageLog {
		total += len(m.Attachments)
	}
	if total != 3 {
		t.Errorf("disabled eviction removed attachments: kept %d, want 3", total)
	}
}

func TestEstimateTokensIncludesAttachments(t *testing.T) {
	o := newTestOptimizer()
	base := o.EstimateTokens()

	o.AddMessage(types.Message{
		Role:        types.RoleUser,
		Content:     "x",
		Attachments: []types.Attachment{fakeImage(750_000)}, // ≈750 tokens
	})

	withAtt := o.EstimateTokens()
	if withAtt-base < 700 {
		t.Errorf("attachment weight = %d tokens, want >= 700", withAtt-base)
	}
}

func TestEvictionKeepsWholeMessageBudget(t *testing.T) {
	o := newTestOptimizer()

	// One message carrying 3 attachments exceeds the budget of 2 → all 3
	// evicted (budget applies per message, newest-first).
	o.AddMessage(types.Message{
		Role:        types.RoleUser,
		Content:     "multi",
		Attachments: []types.Attachment{fakeImage(1000), fakeImage(1000), fakeImage(1000)},
	})
	o.AddMessage(types.Message{
		Role:        types.RoleUser,
		Content:     "single",
		Attachments: []types.Attachment{fakeImage(1000)},
	})
	o.AddMessage(types.Message{Role: types.RoleUser, Content: "next"})

	// The newest message (1 attachment) fits the budget; the older 3-attachment
	// message does not and is fully evicted.
	var kept, evicted int
	for _, m := range o.messageLog {
		kept += len(m.Attachments)
		if strings.Contains(m.Content, "附件已淘汰") {
			evicted++
		}
	}
	if kept != 1 {
		t.Errorf("kept %d attachments, want 1", kept)
	}
	if evicted != 1 {
		t.Errorf("evicted messages = %d, want 1", evicted)
	}
}
