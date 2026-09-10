package modelmeta

import "testing"

func TestIsChatModelRejectsNonChatArtefacts(t *testing.T) {
	// /models is not a chat-model list — vendors publish everything they serve
	// there, and offering these as selectable coding models produces a model
	// that only fails once the user actually tries it.
	nonChat := []string{
		"text-embedding-3-large",
		"bge-m3-embedding",
		"rerank-multilingual-v3.0",
		"omni-moderation-latest",
		"whisper-1",
		"tts-1",
		"gpt-4o-mini-tts",
		"gpt-4o-transcribe",
		"dall-e-3",
		"gpt-image-1",
		"stable-diffusion-xl",
		"gpt-4o-realtime-preview",
		"gpt-4o-audio-preview",
	}
	for _, id := range nonChat {
		if IsChatModel(id) {
			t.Errorf("IsChatModel(%q) = true, want false", id)
		}
	}
}

func TestIsChatModelKeepsChatModels(t *testing.T) {
	// A false positive would hide a working model from the user, which is the
	// worse failure — so vision/reasoning chat models must survive.
	chat := []string{
		"gpt-4o",
		"gpt-4o-mini",
		"claude-3-5-sonnet-20241022",
		"deepseek-chat",
		"deepseek-reasoner",
		"glm-4-plus",
		"moonshot-v1-128k",
		"llama3:latest",
		"qwen2.5-coder-32b-instruct",
		"models/gemini-2.0-flash",
		"openai/gpt-4o",
	}
	for _, id := range chat {
		if !IsChatModel(id) {
			t.Errorf("IsChatModel(%q) = false, want true", id)
		}
	}
	if IsChatModel("") {
		t.Errorf("IsChatModel(\"\") = true, want false")
	}
}

func TestFilterChatModelsPreservesOrder(t *testing.T) {
	in := []Entry{
		{ID: "gpt-4o"},
		{ID: "text-embedding-3-small"},
		{ID: "gpt-4o-mini"},
	}
	got := FilterChatModels(in)
	if len(got) != 2 || got[0].ID != "gpt-4o" || got[1].ID != "gpt-4o-mini" {
		t.Fatalf("FilterChatModels = %+v, want [gpt-4o gpt-4o-mini]", got)
	}
}

func TestParseVendorListOpenAIShape(t *testing.T) {
	body := []byte(`{"object":"list","data":[
		{"id":"gpt-4o","context_window":128000,"max_output_tokens":16384},
		{"id":"gpt-4o-mini"}
	]}`)
	got := ParseVendorList(body)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].ID != "gpt-4o" || got[0].ContextWindow != 128000 || got[0].MaxOutputTokens != 16384 {
		t.Fatalf("first entry = %+v, want gpt-4o/128000/16384", got[0])
	}
	// A vendor that says nothing must not be given an invented figure.
	if got[1].ContextWindow != 0 || got[1].MaxOutputTokens != 0 {
		t.Fatalf("second entry = %+v, want zero metadata (vendor was silent)", got[1])
	}
}

// OpenRouter reports the output cap under top_provider and spells the window
// context_length; both used to be discarded.
func TestParseVendorListOpenRouterShape(t *testing.T) {
	body := []byte(`{"data":[{
		"id":"anthropic/claude-3.5-sonnet",
		"context_length":200000,
		"top_provider":{"max_completion_tokens":8192}
	}]}`)
	got := ParseVendorList(body)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].ContextWindow != 200000 || got[0].MaxOutputTokens != 8192 {
		t.Fatalf("entry = %+v, want 200000/8192", got[0])
	}
}

func TestParseVendorListOllamaShape(t *testing.T) {
	body := []byte(`{"models":[{"name":"llama3:latest"},{"name":"qwen2.5:7b"}]}`)
	got := ParseVendorList(body)
	if len(got) != 2 || got[0].ID != "llama3:latest" || got[1].ID != "qwen2.5:7b" {
		t.Fatalf("ParseVendorList = %+v, want llama3:latest + qwen2.5:7b", got)
	}
}

func TestParseVendorListBareArray(t *testing.T) {
	got := ParseVendorList([]byte(`["a-model","b-model"]`))
	if len(got) != 2 || got[0].ID != "a-model" || got[1].ID != "b-model" {
		t.Fatalf("ParseVendorList = %+v", got)
	}
}

// Gateways spell large windows as strings, sometimes with a "k" suffix.
func TestParseVendorListAcceptsStringAndKiloValues(t *testing.T) {
	body := []byte(`{"data":[
		{"id":"m1","context_length":"128000"},
		{"id":"m2","context_window":"128k"}
	]}`)
	got := ParseVendorList(body)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].ContextWindow != 128000 {
		t.Fatalf("string number = %d, want 128000", got[0].ContextWindow)
	}
	if got[1].ContextWindow != 128000 {
		t.Fatalf("kilo suffix = %d, want 128000", got[1].ContextWindow)
	}
}

func TestParseVendorListDropsDuplicatesAndBlanks(t *testing.T) {
	body := []byte(`{"data":[{"id":"m1"},{"id":"m1"},{"id":""},{"name":"m2"}]}`)
	got := ParseVendorList(body)
	if len(got) != 2 || got[0].ID != "m1" || got[1].ID != "m2" {
		t.Fatalf("ParseVendorList = %+v, want [m1 m2]", got)
	}
}

func TestParseVendorListGarbageReturnsEmpty(t *testing.T) {
	for _, body := range []string{"not json at all", `{"object":"list"}`, `{}`, ""} {
		if got := ParseVendorList([]byte(body)); len(got) != 0 {
			t.Errorf("ParseVendorList(%q) = %+v, want empty", body, got)
		}
	}
}
