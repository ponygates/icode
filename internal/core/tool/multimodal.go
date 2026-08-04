// Package tool — image_gen / video_gen: multimodal generation tools.
//
// These tools call an OpenAI-compatible multimodal backend (images/videos
// generations API). Most providers expose this shape — OpenAI, Zhipu
// (cogview / cogvideox), volcengine, and WorkBuddy's multimodal gateway —
// so a single implementation covers them via configuration.
//
// When no backend is configured the tools return a friendly, actionable hint
// (not a hard error) so the model can explain the missing setup to the user
// instead of crashing the turn.
//
// Claude Code / WorkBuddy parity: rounds out iCode from a pure text/code agent
// into a "full assistant" that can also produce images and short videos.
package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// MultimodalOptions is the tool-package-local view of the multimodal backend
// config. app.go translates config.MultimodalCfg into this struct so the tool
// package stays decoupled from the config package.
type MultimodalOptions struct {
	ImageBaseURL string
	ImageModel   string
	VideoBaseURL string
	VideoModel   string
	APIKey       string
	OutputDir    string
}

func (o *MultimodalOptions) apiKey() string {
	if o != nil && strings.TrimSpace(o.APIKey) != "" {
		return o.APIKey
	}
	if k := strings.TrimSpace(os.Getenv("ICODE_MULTIMODAL_API_KEY")); k != "" {
		return k
	}
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
}

func (o *MultimodalOptions) outputDir() string {
	if o != nil && strings.TrimSpace(o.OutputDir) != "" {
		return o.OutputDir
	}
	return filepath.Join(".icode", "generated")
}

// ============================================================================
// image_gen
// ============================================================================

// ImageGenTool generates images from a text prompt via an OpenAI-compatible
// images/generations endpoint.
type ImageGenTool struct {
	opts *MultimodalOptions
}

// NewImageGenTool constructs the image generation tool. opts may be nil, in
// which case the tool reports "not configured" until SetMultimodalOptions runs.
func NewImageGenTool(opts *MultimodalOptions) *ImageGenTool { return &ImageGenTool{opts: opts} }

func (t *ImageGenTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "image_gen",
		Description: "Generate an image from a text description and save it locally. " +
			"Use for diagrams, illustrations, mockups, or any visual the user asks for. " +
			"Returns the saved file path. Requires a configured multimodal backend " +
			"(config: multimodal.image_base_url / image_model / api_key).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "Text description of the image to generate (required).",
				},
				"size": map[string]any{
					"type":        "string",
					"description": "Image size, e.g. '1024x1024' (default), '1792x1024', '1024x1792'.",
				},
				"output_path": map[string]any{
					"type":        "string",
					"description": "Optional path to save the image. Defaults to .icode/generated/<timestamp>.png",
				},
			},
			"required": []string{"prompt"},
		},
	}
}

type imageGenInput struct {
	Prompt     string `json:"prompt"`
	Size       string `json:"size"`
	OutputPath string `json:"output_path"`
}

func (t *ImageGenTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in imageGenInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("invalid args: %v", err)}, nil
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" {
		return &types.ToolResult{Success: false, Error: "prompt is required"}, nil
	}
	if in.Size == "" {
		in.Size = "1024x1024"
	}

	o := t.opts
	if o == nil || strings.TrimSpace(o.ImageBaseURL) == "" || strings.TrimSpace(o.ImageModel) == "" {
		return &types.ToolResult{
			Success: false,
			Error: "image_gen backend not configured. Set multimodal.image_base_url, " +
				"multimodal.image_model and multimodal.api_key in ~/.icode/config.yaml " +
				"(any OpenAI-compatible images API works: OpenAI, Zhipu cogview, volcengine, WorkBuddy).",
		}, nil
	}
	key := o.apiKey()
	if key == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "multimodal API key missing. Set multimodal.api_key or ICODE_MULTIMODAL_API_KEY / OPENAI_API_KEY.",
		}, nil
	}

	payload := map[string]any{
		"model":           o.ImageModel,
		"prompt":          in.Prompt,
		"size":            in.Size,
		"n":               1,
		"response_format": "b64_json",
	}
	endpoint := strings.TrimRight(o.ImageBaseURL, "/") + "/images/generations"
	respBody, err := postJSON(ctx, endpoint, key, payload, 120*time.Second)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("image generation request failed: %v", err)}, nil
	}

	var resp struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("parse response: %v (body: %s)", err, truncateStr(string(respBody), 200))}, nil
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return &types.ToolResult{Success: false, Error: "backend error: " + resp.Error.Message}, nil
	}
	if len(resp.Data) == 0 {
		return &types.ToolResult{Success: false, Error: "backend returned no image data"}, nil
	}

	outPath := in.OutputPath
	if outPath == "" {
		outPath = filepath.Join(o.outputDir(), fmt.Sprintf("image-%d.png", time.Now().Unix()))
	}
	var imgBytes []byte
	if d := resp.Data[0]; d.B64JSON != "" {
		imgBytes, err = base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("decode image: %v", err)}, nil
		}
	} else if resp.Data[0].URL != "" {
		imgBytes, err = downloadBytes(ctx, resp.Data[0].URL, 120*time.Second)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("download image: %v", err)}, nil
		}
	} else {
		return &types.ToolResult{Success: false, Error: "backend returned neither b64_json nor url"}, nil
	}

	if err := saveFile(outPath, imgBytes); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("save image: %v", err)}, nil
	}
	return &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Image generated and saved to: %s (%d bytes, model=%s)", outPath, len(imgBytes), o.ImageModel),
		// Feed the generated image back into the conversation as an inline
		// attachment so vision-capable models can reference it in later turns.
		Attachments: []types.Attachment{{
			Type:     "image",
			MIMEType: inferImageMIME(outPath),
			Data:     base64.StdEncoding.EncodeToString(imgBytes),
		}},
	}, nil
}

// ============================================================================
// video_gen
// ============================================================================

// VideoGenTool generates a short video from a text prompt. It supports both
// synchronous responses (direct url/b64) and async submit+poll flows (the
// common shape for CogVideoX / Sora-style backends).
type VideoGenTool struct {
	opts *MultimodalOptions
}

// NewVideoGenTool constructs the video generation tool.
func NewVideoGenTool(opts *MultimodalOptions) *VideoGenTool { return &VideoGenTool{opts: opts} }

func (t *VideoGenTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "video_gen",
		Description: "Generate a short video from a text description and save it locally. " +
			"Generation can take 1-3 minutes. Returns the saved file path. Requires a " +
			"configured multimodal backend (config: multimodal.video_base_url / video_model / api_key).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "Text description of the video to generate (required).",
				},
				"output_path": map[string]any{
					"type":        "string",
					"description": "Optional path to save the video. Defaults to .icode/generated/<timestamp>.mp4",
				},
			},
			"required": []string{"prompt"},
		},
	}
}

type videoGenInput struct {
	Prompt     string `json:"prompt"`
	OutputPath string `json:"output_path"`
}

func (t *VideoGenTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in videoGenInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("invalid args: %v", err)}, nil
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" {
		return &types.ToolResult{Success: false, Error: "prompt is required"}, nil
	}

	o := t.opts
	if o == nil || strings.TrimSpace(o.VideoBaseURL) == "" || strings.TrimSpace(o.VideoModel) == "" {
		return &types.ToolResult{
			Success: false,
			Error: "video_gen backend not configured. Set multimodal.video_base_url, " +
				"multimodal.video_model and multimodal.api_key in ~/.icode/config.yaml.",
		}, nil
	}
	key := o.apiKey()
	if key == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "multimodal API key missing. Set multimodal.api_key or ICODE_MULTIMODAL_API_KEY / OPENAI_API_KEY.",
		}, nil
	}

	base := strings.TrimRight(o.VideoBaseURL, "/")
	payload := map[string]any{"model": o.VideoModel, "prompt": in.Prompt}
	respBody, err := postJSON(ctx, base+"/videos/generations", key, payload, 60*time.Second)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("video submit failed: %v", err)}, nil
	}

	// Tolerant parse: some backends return the video directly, others return
	// an async task id to poll.
	var submit struct {
		ID         string `json:"id"`
		TaskID     string `json:"task_id"`
		RequestID  string `json:"request_id"`
		Status     string `json:"status"`
		TaskStatus string `json:"task_status"`
		VideoURL   string `json:"video_url"`
		Data       []struct {
			URL      string `json:"url"`
			VideoURL string `json:"video_url"`
		} `json:"data"`
		VideoResult []struct {
			URL string `json:"url"`
		} `json:"video_result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &submit); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("parse submit response: %v (body: %s)", err, truncateStr(string(respBody), 200))}, nil
	}
	if submit.Error != nil && submit.Error.Message != "" {
		return &types.ToolResult{Success: false, Error: "backend error: " + submit.Error.Message}, nil
	}

	videoURL := firstVideoURL(submit.VideoURL, submit.Data, submit.VideoResult)
	taskID := firstNonEmpty(submit.ID, submit.TaskID, submit.RequestID)

	// Poll if we got a task id but no direct url yet.
	if videoURL == "" && taskID != "" {
		videoURL, err = pollVideo(ctx, base, key, taskID, 3*time.Minute)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, nil
		}
	}
	if videoURL == "" {
		return &types.ToolResult{Success: false, Error: "backend returned no video url and no pollable task id"}, nil
	}

	vidBytes, err := downloadBytes(ctx, videoURL, 180*time.Second)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("download video: %v", err)}, nil
	}
	outPath := in.OutputPath
	if outPath == "" {
		outPath = filepath.Join(o.outputDir(), fmt.Sprintf("video-%d.mp4", time.Now().Unix()))
	}
	if err := saveFile(outPath, vidBytes); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("save video: %v", err)}, nil
	}
	return &types.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Video generated and saved to: %s (%d bytes, model=%s)", outPath, len(vidBytes), o.VideoModel),
	}, nil
}

// pollVideo polls an async video task until it completes or the deadline passes.
func pollVideo(ctx context.Context, base, key, taskID string, budget time.Duration) (string, error) {
	deadline := time.Now().Add(budget)
	// Try the two most common status endpoints.
	statusURLs := []string{
		base + "/videos/generations/" + taskID,
		base + "/async-result/" + taskID,
	}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
		for _, su := range statusURLs {
			body, err := getWithAuth(ctx, su, key, 30*time.Second)
			if err != nil {
				continue
			}
			var st struct {
				Status     string `json:"status"`
				TaskStatus string `json:"task_status"`
				VideoURL   string `json:"video_url"`
				Data       []struct {
					URL      string `json:"url"`
					VideoURL string `json:"video_url"`
				} `json:"data"`
				VideoResult []struct {
					URL string `json:"url"`
				} `json:"video_result"`
			}
			if json.Unmarshal(body, &st) != nil {
				continue
			}
			if u := firstVideoURL(st.VideoURL, st.Data, st.VideoResult); u != "" {
				return u, nil
			}
			s := strings.ToUpper(firstNonEmpty(st.Status, st.TaskStatus))
			if s == "FAILED" || s == "ERROR" {
				return "", fmt.Errorf("video generation failed (status=%s)", s)
			}
		}
	}
	return "", fmt.Errorf("video generation timed out after %s", budget)
}

// ============================================================================
// shared helpers
// ============================================================================

func firstVideoURL(direct string, data []struct {
	URL      string `json:"url"`
	VideoURL string `json:"video_url"`
}, videoResult []struct {
	URL string `json:"url"`
}) string {
	if strings.TrimSpace(direct) != "" {
		return direct
	}
	for _, d := range data {
		if d.VideoURL != "" {
			return d.VideoURL
		}
		if d.URL != "" {
			return d.URL
		}
	}
	for _, v := range videoResult {
		if v.URL != "" {
			return v.URL
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func postJSON(ctx context.Context, url, key string, payload any, timeout time.Duration) ([]byte, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateStr(string(out), 200))
	}
	return out, nil
}

func getWithAuth(ctx context.Context, url, key string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
}

func downloadBytes(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256*1024*1024))
}

func saveFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// inferImageMIME maps a file extension to a MIME type for the generated image
// attachment. Defaults to image/png which is what the b64_json response format
// produces.
func inferImageMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
