// Voice transcription endpoint backing the desktop / simpleui voice buttons.
// The browser captures a short WAV with the MediaRecorder API and POSTs it
// here; the handler runs it through the same Zhipu GLM-ASR model the rest of
// the app already uses, so no extra API key is required.
package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/ponygates/icode/internal/core/voice"
)

type voiceResponse struct {
	Text string `json:"text"`
}

type voiceErrorResponse struct {
	Error string `json:"error"`
}

// handleVoice accepts a multipart audio upload and returns recognised text.
func (s *Server) handleVoice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, voice.MaxAudioBytes+1<<20)

	f, _, err := r.FormFile("file")
	if err != nil {
		writeVoiceError(w, "缺少音频文件（file 字段）: "+err.Error())
		return
	}
	defer f.Close()

	audio, err := io.ReadAll(io.LimitReader(f, voice.MaxAudioBytes+1))
	if err != nil {
		writeVoiceError(w, "读取音频失败: "+err.Error())
		return
	}

	apiKey := s.cfg.APIKey("zhipu")
	text, err := voice.TranscribeZhipu(r.Context(), apiKey, audio, "voice.wav")
	if err != nil {
		writeVoiceError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(voiceResponse{Text: text})
}

func writeVoiceError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(voiceErrorResponse{Error: msg})
}
