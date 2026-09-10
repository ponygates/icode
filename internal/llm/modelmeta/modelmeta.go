// Package modelmeta centralises the rules for interpreting a vendor's
// /models response, so the two ingestion paths share one implementation:
//
//   - live discovery (openai_compat.BaseProvider.FetchModels), driven by the
//     desktop "获取模型" action;
//   - the refresh pipeline (pkg/modelupdate), driven by the "刷新" button.
//
// Two problems motivated extracting this:
//
//  1. /models is not a chat-model list. Vendors publish every artefact they
//     serve there — embeddings, speech, image and moderation endpoints too.
//     iCode can only call chat completions, so those entries must be filtered
//     out at ingestion; otherwise they appear as ordinary selectable models
//     and fail only when the user actually tries one.
//
//  2. Vendors that do report per-model metadata (OpenRouter and several
//     gateways expose context_length / max_completion_tokens) were being
//     ignored, so a freshly discovered model showed either a blank context
//     window or a stale built-in default instead of what the vendor says.
package modelmeta

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Entry is one model as reported by a vendor's /models endpoint, with whatever
// metadata that vendor chose to include (zero when absent).
type Entry struct {
	ID              string
	ContextWindow   int
	MaxOutputTokens int
}

// nonChatMarkers are substrings that identify a model id which cannot serve a
// chat completion. Kept deliberately narrow: a false negative merely leaves a
// useless model in the list, whereas a false positive would hide a working
// model from the user, which is the worse failure.
var nonChatMarkers = []string{
	"embed",        // text-embedding-3-large, bge-m3-embedding, …
	"rerank",       // rerank-multilingual-v3.0
	"moderation",   // omni-moderation-latest
	"whisper",      // whisper-1
	"-tts", "tts-", // gpt-4o-mini-tts, tts-1
	"text-to-speech", // vendor-agnostic spelling
	"transcribe",     // gpt-4o-transcribe
	"dall-e", "dalle",
	"stable-diffusion",
	"gpt-image", // gpt-image-1
	"-audio", "audio-",
	"realtime", // *-realtime-preview is a WebRTC session, not a completion
	"computer-use",
	"-ocr", "ocr-",
	"bge-", "clip-",
	"veo-", "sora",
	"colpali", "jina-clip",
}

// IsChatModel reports whether a model id can plausibly serve a chat/tool
// completion. It is a heuristic on the id alone, which is all a /models
// response reliably offers.
func IsChatModel(id string) bool {
	if id == "" {
		return false
	}
	l := strings.ToLower(id)
	for _, marker := range nonChatMarkers {
		if strings.Contains(l, marker) {
			return false
		}
	}
	return true
}

// FilterChatModels returns only the entries iCode can actually use, preserving
// vendor order.
func FilterChatModels(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if IsChatModel(e.ID) {
			out = append(out, e)
		}
	}
	return out
}

// flexInt accepts a JSON number or a numeric string. Vendor /models payloads
// are inconsistent about this: OpenRouter uses numbers, several gateways send
// strings, and some omit the field entirely.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	s = strings.Trim(s, `"`)
	if s == "" {
		return nil
	}
	// Some gateways spell large windows as "128k".
	if n, err := strconv.ParseFloat(strings.TrimSuffix(strings.ToLower(s), "k"), 64); err == nil {
		if strings.HasSuffix(strings.ToLower(s), "k") {
			n *= 1000
		}
		*f = flexInt(int(n))
	}
	return nil
}

// rawEntry mirrors the union of the key names seen in the wild.
type rawEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model"`
	Slug  string `json:"slug"`

	ContextLength   flexInt `json:"context_length"`
	ContextWindow   flexInt `json:"context_window"`
	MaxOutputTokens flexInt `json:"max_output_tokens"`
	MaxCompletion   flexInt `json:"max_completion_tokens"`

	TopProvider struct {
		MaxCompletionTokens flexInt `json:"max_completion_tokens"`
		ContextLength       flexInt `json:"context_length"`
	} `json:"top_provider"`
}

func (e rawEntry) id() string {
	for _, cand := range []string{e.ID, e.Model, e.Name, e.Slug} {
		if strings.TrimSpace(cand) != "" {
			return strings.TrimSpace(cand)
		}
	}
	return ""
}

func (e rawEntry) contextWindow() int {
	if e.ContextWindow > 0 {
		return int(e.ContextWindow)
	}
	if e.ContextLength > 0 {
		return int(e.ContextLength)
	}
	return int(e.TopProvider.ContextLength)
}

func (e rawEntry) maxOutput() int {
	if e.MaxOutputTokens > 0 {
		return int(e.MaxOutputTokens)
	}
	if e.MaxCompletion > 0 {
		return int(e.MaxCompletion)
	}
	return int(e.TopProvider.MaxCompletionTokens)
}

// ParseVendorList extracts model entries from a /models response, tolerating
// every shape seen in the wild:
//
//	{"data":[{"id":"gpt-4o","context_window":128000}]}   OpenAI, DeepSeek, Moonshot, …
//	{"models":[{"name":"llama3:latest"}]}                Ollama's native /api/tags
//	["gpt-4o","gpt-4o-mini"]                             a bare array of ids
//
// Duplicates and blank ids are dropped; vendor order is preserved. Metadata is
// only ever taken from the vendor — nothing is inferred here, so a zero field
// means "the vendor did not say", never a guess.
func ParseVendorList(body []byte) []Entry {
	var wrapper struct {
		Data   []rawEntry `json:"data"`
		Models []rawEntry `json:"models"`
	}

	var entries []rawEntry
	if err := json.Unmarshal(body, &wrapper); err == nil {
		entries = wrapper.Data
		if len(entries) == 0 {
			entries = wrapper.Models
		}
	}

	seen := make(map[string]bool)
	out := make([]Entry, 0, len(entries)+8)
	for _, e := range entries {
		id := e.id()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Entry{
			ID:              id,
			ContextWindow:   e.contextWindow(),
			MaxOutputTokens: e.maxOutput(),
		})
	}

	if len(out) > 0 {
		return out
	}

	// Fall back to a bare array of ids.
	var ids []string
	if err := json.Unmarshal(body, &ids); err == nil {
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, Entry{ID: id})
		}
	}
	return out
}
