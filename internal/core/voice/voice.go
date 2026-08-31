// Package voice provides speech-to-text (ASR) and microphone capture for the
// /voice input feature, shared by all three UIs (desktop, CLI, simpleui).
//
// Transcription supports multiple providers:
//   - Zhipu GLM-ASR (default, reuses existing zhipu key)
//   - Baidu Short Speech Recognition (access_token based)
//   - iFlytek Real-time Speech Recognition (WebSocket based)
//
// Microphone capture is implemented with the Windows waveIn API
// (see recorder_windows.go); non-Windows builds get a stub.
package voice

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- Provider identifiers ----------

const (
	ProviderZhipu   = "zhipu"
	ProviderBaidu   = "baidu"
	ProvideriFlytek = "xfyun"
)

// ---------- Shared constants ----------

// MaxAudioBytes caps the payload at the largest provider limit.
const MaxAudioBytes = 25 << 20

// ---------- Zhipu GLM-ASR ----------

const zhipuASRBase = "https://open.bigmodel.cn/api/paas/v4/audio/transcriptions"
const zhipuASRModel = "glm-asr"

type zhipuTranscribeResponse struct {
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
	_ = mw.WriteField("model", zhipuASRModel)
	_ = mw.WriteField("stream", "false")
	if err := mw.Close(); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, zhipuASRBase, &body)
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
	var out zhipuTranscribeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("智谱语音识别响应解析失败: %v", err)
	}
	text := out.Text
	if text == "" {
		return "", fmt.Errorf("智谱语音识别未返回文本（请检查麦克风是否正常）")
	}
	return text, nil
}

// ---------- Baidu Short Speech Recognition ----------

const (
	baiduTokenURL = "https://aip.baidubce.com/oauth/2.0/token"
	baiduASRURL   = "https://vop.baidu.com/server_api"
)

// baiduTokenCache caches the access_token with its expiry.
type baiduTokenCache struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

var baiduCache = &baiduTokenCache{}

// baiduGetToken retrieves or refreshes the Baidu access_token using API Key
// and Secret Key. The token is cached until 5 minutes before expiry. The HTTP
// round-trip runs OUTSIDE the cache mutex so a slow/hanging token refresh can
// never block concurrent transcriptions; the write-back re-checks under the
// lock so a racing refresh cannot regress a newer token.
func baiduGetToken(ctx context.Context, apiKey, secretKey string) (string, error) {
	// Fast path: cached token still valid — return under a brief lock only.
	baiduCache.mu.Lock()
	if baiduCache.token != "" && time.Now().Before(baiduCache.expires) {
		tok := baiduCache.token
		baiduCache.mu.Unlock()
		return tok, nil
	}
	baiduCache.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baiduTokenURL, nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("grant_type", "client_credentials")
	q.Set("client_id", apiKey)
	q.Set("client_secret", secretKey)
	req.URL.RawQuery = q.Encode()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("百度 token 请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", fmt.Errorf("百度 token 解析失败: %v", err)
	}
	if tok.Error != "" {
		return "", fmt.Errorf("百度认证失败: %s — %s", tok.Error, tok.ErrorDesc)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("百度 token 为空，请检查 API Key 和 Secret Key")
	}

	// Write back under the lock; keep whichever token expires later so a
	// concurrent refresh never clobbers a fresher result.
	newExp := time.Now().Add(time.Duration(tok.ExpiresIn-300) * time.Second)
	baiduCache.mu.Lock()
	if baiduCache.token == "" || baiduCache.expires.Before(newExp) {
		baiduCache.token = tok.AccessToken
		baiduCache.expires = newExp
	}
	result := baiduCache.token
	baiduCache.mu.Unlock()
	return result, nil
}

// baiduTranscribeResponse is the Baidu ASR JSON response.
type baiduTranscribeResponse struct {
	ErrNo  int      `json:"err_no"`
	ErrMsg string   `json:"err_msg"`
	Result []string `json:"result"`
}

// TranscribeBaidu sends an audio blob to Baidu Short Speech Recognition and
// returns the recognised text. audio should be pcm/wav, 16kHz 16bit mono.
func TranscribeBaidu(ctx context.Context, apiKey, secretKey string, audio []byte) (string, error) {
	if apiKey == "" || secretKey == "" {
		return "", fmt.Errorf("百度语音 API Key/Secret Key 未配置：请通过「设置 → 语音识别」添加百度的 API Key 和 Secret Key")
	}
	if len(audio) == 0 {
		return "", fmt.Errorf("没有捕获到音频")
	}
	if len(audio) > MaxAudioBytes {
		return "", fmt.Errorf("音频过大（%d bytes，上限 25MB），请缩短录音", len(audio))
	}

	token, err := baiduGetToken(ctx, apiKey, secretKey)
	if err != nil {
		return "", err
	}

	// Baidu accepts raw PCM in body with query params, or JSON with base64.
	// We use the JSON+base64 approach. The recorder produces a 44-byte
	// RIFF/WAVE-wrapped buffer (BuildWAV), so format must be "wav" — declaring
	// "pcm" here would make Baidu parse the WAV header bytes as audio and fail.
	payload := map[string]interface{}{
		"format":  "wav",
		"rate":    16000,
		"channel": 1,
		"cuid":    "icode-desktop",
		"token":   token,
		"speech":  base64.StdEncoding.EncodeToString(audio),
		"len":     len(audio),
	}
	body, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baiduASRURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("百度语音识别请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var out baiduTranscribeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("百度语音识别响应解析失败: %v", err)
	}
	if out.ErrNo != 0 {
		return "", fmt.Errorf("百度语音识别失败 (err_no=%d): %s", out.ErrNo, out.ErrMsg)
	}
	if len(out.Result) == 0 || out.Result[0] == "" {
		return "", fmt.Errorf("百度语音识别未返回文本（请检查麦克风是否正常）")
	}
	return out.Result[0], nil
}

// ---------- iFlytek Real-time Speech Recognition (HTTP REST wrapper) ----------

// iFlytek uses a signed REST API. We call the /api/prepare → /api/upload →
// /api/merge → /api/getProgress → /api/getResult flow for audio ≤ 60s.
const (
	iflytekBaseURL = "https://raasr.xfyun.cn/api"
	iflytekV2Base  = "https://raasr.xfyun.cn/v2/api"
)

// iFlytekSign generates the HMAC-SHA1 signature for iFlytek API calls.
func iFlytekSign(secret, ts, signaStr string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(signaStr))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// iFlytekPrepareResponse is the /api/prepare response.
type iFlytekPrepareResponse struct {
	Ok   int    `json:"ok"`
	Data string `json:"data"` // task_id
}

// iFlytekGetResultResponse is the /api/getResult response.
type iFlytekGetResultResponse struct {
	Ok     int    `json:"ok"`
	Data   string `json:"data"` // JSON string of results
	ErrNo  int    `json:"err_no"`
	Failed string `json:"failed"`
}

// iFlytekResultItem represents one sentence in the transcription result.
type iFlytekResultItem struct {
	OneBest string `json:"onebest"`
}

// TranscribeiFlytek sends an audio blob to iFlytek REST API and returns the
// recognised text. audio should be WAV/PCM, 16kHz 16bit mono, ≤60s.
func TranscribeiFlytek(ctx context.Context, appID, apiKey, apiSecret string, audio []byte) (string, error) {
	if appID == "" || apiKey == "" || apiSecret == "" {
		return "", fmt.Errorf("讯飞语音 App ID / API Key / API Secret 未配置：请通过「设置 → 语音识别」添加讯飞的密钥")
	}
	if len(audio) == 0 {
		return "", fmt.Errorf("没有捕获到音频")
	}
	if len(audio) > MaxAudioBytes {
		return "", fmt.Errorf("音频过大（%d bytes，上限 25MB），请缩短录音", len(audio))
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	fileLen := strconv.Itoa(len(audio))
	sliceNum := "1"

	// Step 1: /api/prepare
	signStr := appID + ts
	sign := iFlytekSign(apiSecret, ts, signStr)

	prepareBody := map[string]string{
		"app_id":    appID,
		"signa":     sign,
		"ts":        ts,
		"file_len":  fileLen,
		"file_name": "voice.wav",
		"slice_num": sliceNum,
	}
	prepareJSON, _ := json.Marshal(prepareBody)

	ctx1, cancel1 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel1()
	req1, err := http.NewRequestWithContext(ctx1, http.MethodPost, iflytekBaseURL+"/prepare", bytes.NewReader(prepareJSON))
	if err != nil {
		return "", err
	}
	req1.Header.Set("Content-Type", "application/json")

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		return "", fmt.Errorf("讯飞预处理请求失败: %w", err)
	}
	defer resp1.Body.Close()
	raw1, _ := io.ReadAll(io.LimitReader(resp1.Body, 1<<20))

	var prepResp iFlytekPrepareResponse
	if err := json.Unmarshal(raw1, &prepResp); err != nil {
		return "", fmt.Errorf("讯飞预处理响应解析失败: %v", err)
	}
	if prepResp.Ok != 0 {
		return "", fmt.Errorf("讯飞预处理失败 (ok=%d): %s", prepResp.Ok, truncateStr(string(raw1), 300))
	}
	taskID := prepResp.Data

	// Step 2: /api/upload — send the audio data
	ts2 := strconv.FormatInt(time.Now().Unix(), 10)
	sign2Str := appID + ts2 + taskID + "1" + fileLen
	sign2 := iFlytekSign(apiSecret, ts2, sign2Str)

	uploadURL := fmt.Sprintf("%s/upload?appid=%s&signa=%s&ts=%s&task_id=%s&slice_id=aaaaaaaaaa&content_size=%s&block=1", iflytekBaseURL, appID, sign2, ts2, taskID, fileLen)
	ctx2, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel2()
	req2, err := http.NewRequestWithContext(ctx2, http.MethodPost, uploadURL, bytes.NewReader(audio))
	if err != nil {
		return "", err
	}
	req2.Header.Set("Content-Type", "application/octet-stream")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", fmt.Errorf("讯飞上传请求失败: %w", err)
	}
	defer resp2.Body.Close()
	raw2, _ := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))

	var uploadResp struct {
		Ok   int    `json:"ok"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw2, &uploadResp); err != nil {
		return "", fmt.Errorf("讯飞上传响应解析失败: %v", err)
	}
	if uploadResp.Ok != 0 {
		return "", fmt.Errorf("讯飞上传失败 (ok=%d): %s", uploadResp.Ok, truncateStr(string(raw2), 300))
	}

	// Step 3: /api/merge
	ts3 := strconv.FormatInt(time.Now().Unix(), 10)
	sign3Str := appID + ts3 + taskID
	sign3 := iFlytekSign(apiSecret, ts3, sign3Str)

	mergeBody := map[string]string{
		"app_id":  appID,
		"signa":   sign3,
		"ts":      ts3,
		"task_id": taskID,
	}
	mergeJSON, _ := json.Marshal(mergeBody)

	ctx3, cancel3 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel3()
	req3, err := http.NewRequestWithContext(ctx3, http.MethodPost, iflytekBaseURL+"/merge", bytes.NewReader(mergeJSON))
	if err != nil {
		return "", err
	}
	req3.Header.Set("Content-Type", "application/json")

	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		return "", fmt.Errorf("讯飞合并请求失败: %w", err)
	}
	defer resp3.Body.Close()

	// Step 4: Poll /api/getProgress until done, then /api/getResult
	for i := 0; i < 30; i++ { // max 30 iterations, ~30s
		// Honor caller cancellation (Esc / stop) so an interrupted session
		// never keeps polling for up to 30s.
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("讯飞识别被取消: %w", ctx.Err())
		case <-time.After(1 * time.Second):
		}

		ts4 := strconv.FormatInt(time.Now().Unix(), 10)
		sign4Str := appID + ts4 + taskID
		sign4 := iFlytekSign(apiSecret, ts4, sign4Str)

		progressBody := map[string]string{
			"app_id":  appID,
			"signa":   sign4,
			"ts":      ts4,
			"task_id": taskID,
		}
		progressJSON, _ := json.Marshal(progressBody)

		ctx4, cancel4 := context.WithTimeout(ctx, 10*time.Second)
		req4, err := http.NewRequestWithContext(ctx4, http.MethodPost, iflytekBaseURL+"/getProgress", bytes.NewReader(progressJSON))
		if err != nil {
			cancel4()
			continue
		}
		req4.Header.Set("Content-Type", "application/json")

		resp4, err := http.DefaultClient.Do(req4)
		if err != nil {
			cancel4()
			continue
		}
		raw4, _ := io.ReadAll(io.LimitReader(resp4.Body, 1<<20))
		resp4.Body.Close()
		cancel4()

		var progressResp struct {
			Ok   int    `json:"ok"`
			Data string `json:"data"` // JSON string with status info
		}
		if err := json.Unmarshal(raw4, &progressResp); err != nil {
			continue
		}

		// Parse the nested data JSON
		var progressInfo struct {
			Status int `json:"status"`
		}
		if err := json.Unmarshal([]byte(progressResp.Data), &progressInfo); err != nil {
			continue
		}

		// status 4 = completed
		if progressInfo.Status == 4 || progressResp.Ok != 0 {
			break
		}
	}

	// Step 5: /api/getResult
	ts5 := strconv.FormatInt(time.Now().Unix(), 10)
	sign5Str := appID + ts5 + taskID
	sign5 := iFlytekSign(apiSecret, ts5, sign5Str)

	resultBody := map[string]string{
		"app_id":  appID,
		"signa":   sign5,
		"ts":      ts5,
		"task_id": taskID,
	}
	resultJSON, _ := json.Marshal(resultBody)

	ctx5, cancel5 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel5()
	req5, err := http.NewRequestWithContext(ctx5, http.MethodPost, iflytekBaseURL+"/getResult", bytes.NewReader(resultJSON))
	if err != nil {
		return "", err
	}
	req5.Header.Set("Content-Type", "application/json")

	resp5, err := http.DefaultClient.Do(req5)
	if err != nil {
		return "", fmt.Errorf("讯飞获取结果请求失败: %w", err)
	}
	defer resp5.Body.Close()
	raw5, _ := io.ReadAll(io.LimitReader(resp5.Body, 1<<20))

	var resultResp iFlytekGetResultResponse
	if err := json.Unmarshal(raw5, &resultResp); err != nil {
		return "", fmt.Errorf("讯飞获取结果响应解析失败: %v", err)
	}
	if resultResp.Ok != 0 {
		return "", fmt.Errorf("讯飞获取结果失败 (ok=%d): %s", resultResp.Ok, resultResp.Failed)
	}

	// Parse the result data — it's an array of sentence items
	var items []iFlytekResultItem
	if err := json.Unmarshal([]byte(resultResp.Data), &items); err != nil {
		return "", fmt.Errorf("讯飞转写结果解析失败: %v", err)
	}

	var text strings.Builder
	for _, item := range items {
		text.WriteString(item.OneBest)
	}
	result := text.String()
	if result == "" {
		return "", fmt.Errorf("讯飞语音识别未返回文本（请检查麦克风是否正常）")
	}
	return result, nil
}

// ---------- Helpers ----------

// truncateStr limits s to n runes for error messages.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// GenerateCUID returns a simple unique identifier for API calls.
func GenerateCUID() string {
	return fmt.Sprintf("icode-%d-%d", time.Now().UnixNano(), rand.Int31())
}
