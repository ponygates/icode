package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageGenNotConfigured(t *testing.T) {
	tool := NewImageGenTool(nil)
	res, err := tool.Execute(context.Background(), `{"prompt":"a red circle"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure when backend not configured")
	}
	if !strings.Contains(res.Error, "not configured") {
		t.Fatalf("expected 'not configured' hint, got: %s", res.Error)
	}
}

func TestImageGenSuccess(t *testing.T) {
	// 1x1 transparent PNG bytes, base64-encoded as the backend would return.
	pngB64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/images/generations") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": pngB64}},
		})
	}))
	defer srv.Close()

	outDir := t.TempDir()
	tool := NewImageGenTool(&MultimodalOptions{
		ImageBaseURL: srv.URL,
		ImageModel:   "test-image-model",
		APIKey:       "test-key",
		OutputDir:    outDir,
	})
	res, err := tool.Execute(context.Background(), `{"prompt":"a red circle"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	// Verify a file was actually written and is a valid PNG.
	matches, _ := filepath.Glob(filepath.Join(outDir, "*.png"))
	if len(matches) == 0 {
		t.Fatal("expected a saved .png file")
	}
	data, _ := os.ReadFile(matches[0])
	want, _ := base64.StdEncoding.DecodeString(pngB64)
	if len(data) != len(want) {
		t.Fatalf("saved file size mismatch: got %d want %d", len(data), len(want))
	}
	// The generated image must be fed back as an inline attachment so
	// vision-capable models can reference it in later turns.
	if len(res.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(res.Attachments))
	}
	att := res.Attachments[0]
	if att.Type != "image" || att.MIMEType != "image/png" {
		t.Errorf("unexpected attachment metadata: %+v", att)
	}
	if att.Data != pngB64 {
		t.Errorf("attachment data should equal the backend b64_json payload")
	}
}

func TestVideoGenNotConfigured(t *testing.T) {
	tool := NewVideoGenTool(nil)
	res, _ := tool.Execute(context.Background(), `{"prompt":"a cat playing"}`)
	if res.Success || !strings.Contains(res.Error, "not configured") {
		t.Fatalf("expected 'not configured' hint, got: %+v", res)
	}
}
