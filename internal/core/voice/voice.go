// Package voice provides speech-to-text (ASR) and microphone capture for the
// /voice input feature, shared by all three UIs (desktop, CLI, simpleui).
//
// Transcription uses the Zhipu GLM-ASR model via the same provider the app
// already ships with, so voice input reuses the user's existing zhipu key with
// no extra setup. Microphone capture is implemented with the Windows waveIn
// API (see recorder_windows.go); non-Windows builds get a stub.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// ZhipuASRBase is the Zhipu audio transcription endpoint.
const ZhipuASRBase = "https://open.bigmodel.cn/api/paas/v4/audio/transcriptions"

// ZhipuASRModel is the GLM-ASR model id used for transcription.
const ZhipuASRModel = "glm-asr"

// MaxAudioBytes caps the payload at GLM-ASR's 25 MB upload limit.
const MaxAudioBytes = 25 << 20

// transcribeResponse is the Zhipu ASR JSON response.
type transcribeResponse struct {
	Text string `json:"text"`
}

// TranscribeZhipu sends an audio blob (WAV) to Zhipu GLM-ASR and returns the
// recognised text. Recordings are already trimmed to ≤30s by the recorder; an
// empty apiKey yields a friendly setup hint instead of a raw HTTP error.
func TranscribeZhipu(ctx context.Context, apiKey string, audio []byte, filename string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("智谱 API Key 未配置：请先通过「设置 → API 密钥」添加 zhipu 的 Key（语音识别复用智谱 GLM-ASR）")
	}
	if len(audio) == 0 {
		return "", fmt.Errorf("没有捕获到音频")
	}
	if len(audio) > MaxAudioBytes {
		return "", fmt.Errorf("音频过大（%d bytes，上限 25MB），请缩短录音", len(audio))
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	_ = mw.WriteField("model", ZhipuASRModel)
	_ = mw.WriteField("stream", "false")
	if err := mw.Close(); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ZhipuASRBase, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("智谱语音识别请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("智谱语音识别失败 (HTTP %d): %s", resp.StatusCode, truncateStr(string(raw), 300))
	}
	var out transcribeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("智谱语音识别响应解析失败: %v", err)
	}
	text := out.Text
	if text == "" {
		return "", fmt.Errorf("智谱语音识别未返回文本（请检查麦克风是否正常）")
	}
	return text, nil
}

// truncateStr limits s to n runes for error messages.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
