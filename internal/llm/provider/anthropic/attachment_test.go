package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func TestBuildMessagesBody_ImageAttachment(t *testing.T) {
	p := New("k", "https://x.test")
	req := types.ChatRequest{
		Model: "m",
		Messages: []types.Message{
			{
				Role:    types.RoleUser,
				Content: "look",
				Attachments: []types.Attachment{
					{Type: "image", MIMEType: "image/png", Data: "ABC"},
				},
			},
		},
	}
	r, err := p.buildMessagesBody(req, false)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		t.Fatal(err)
	}
	blocks, ok := body.Messages[0].Content.([]any)
	if !ok {
		t.Fatalf("expected content blocks array, got %T", body.Messages[0].Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks (text + image), got %d", len(blocks))
	}
	img, ok := blocks[1].(map[string]any)
	if !ok {
		t.Fatalf("expected map for image block, got %T", blocks[1])
	}
	if img["type"] != "image" {
		t.Errorf("expected image block type, got %v", img["type"])
	}
	src, ok := img["source"].(map[string]any)
	if !ok {
		t.Fatalf("expected image source block, got %v", img["source"])
	}
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "ABC" {
		t.Errorf("unexpected image source: %v", src)
	}
}

func TestBuildMessagesBody_PlainTextUnchanged(t *testing.T) {
	p := New("k", "https://x.test")
	req := types.ChatRequest{
		Model:    "m",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	r, _ := p.buildMessagesBody(req, false)
	var body struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(r).Decode(&body)
	if _, ok := body.Messages[0].Content.(string); !ok {
		t.Fatalf("plain text content must stay a string, got %T", body.Messages[0].Content)
	}
}
