package openai_compat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func newTestProvider() *BaseProvider {
	return New(Config{Name: "t", APIBase: "https://x.test/v1", APIKey: "k"})
}

func TestBuildRequestBody_ImageAttachment(t *testing.T) {
	p := newTestProvider()
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
	r, err := p.buildRequestBody(req, false)
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
	if len(body.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(body.Messages))
	}
	parts, ok := body.Messages[0].Content.([]any)
	if !ok {
		t.Fatalf("expected content array, got %T", body.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("want 2 parts (text + image), got %d", len(parts))
	}
	img, ok := parts[1].(map[string]any)
	if !ok {
		t.Fatalf("expected map for image part, got %T", parts[1])
	}
	iu, ok := img["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("expected image_url part, got %v", img)
	}
	url, _ := iu["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,ABC") {
		t.Errorf("unexpected image url: %s", url)
	}
}

func TestBuildRequestBody_PlainTextUnchanged(t *testing.T) {
	p := newTestProvider()
	req := types.ChatRequest{
		Model:    "m",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	r, _ := p.buildRequestBody(req, false)
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

func TestBuildRequestBody_ToolMessageIgnoresAttachment(t *testing.T) {
	p := newTestProvider()
	req := types.ChatRequest{
		Model: "m",
		Messages: []types.Message{
			{
				Role:    types.RoleTool,
				Content: "res",
				ToolID:  "call1",
				Attachments: []types.Attachment{
					{Type: "image", MIMEType: "image/png", Data: "ABC"},
				},
			},
		},
	}
	r, _ := p.buildRequestBody(req, false)
	var body struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(r).Decode(&body)
	if _, ok := body.Messages[0].Content.(string); !ok {
		t.Fatalf("tool message must stay plain text, got %T", body.Messages[0].Content)
	}
}
