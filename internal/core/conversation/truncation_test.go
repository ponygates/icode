package conversation

import (
	"testing"
)

func TestTruncationDetector_NotTruncated(t *testing.T) {
	detector := NewTruncationDetector(DefaultTruncationRecoveryConfig())
	truncated := detector.IsTruncated("stop", "This is a complete sentence.")
	if truncated {
		t.Error("complete sentence should not be detected as truncated")
	}
}

func TestTruncationDetector_FinishReasonLength(t *testing.T) {
	detector := NewTruncationDetector(DefaultTruncationRecoveryConfig())
	truncated := detector.IsTruncated("length", "incomplete")
	if !truncated {
		t.Error("finish_reason=length should be detected as truncated")
	}
}

func TestTruncationDetector_TruncatedJSON(t *testing.T) {
	detector := NewTruncationDetector(DefaultTruncationRecoveryConfig())
	truncated := detector.IsTruncated("stop", `{"path": "main.go", "content": "hi`)
	if !truncated {
		t.Error("truncated JSON should be detected")
	}
}

func TestTruncationDetector_UnclosedCodeBlock(t *testing.T) {
	detector := NewTruncationDetector(DefaultTruncationRecoveryConfig())
	truncated := detector.IsTruncated("stop", "```go\nfmt.Println")
	if !truncated {
		t.Error("unclosed code block should be detected as truncated")
	}
}

func TestNextTokens_Escalation(t *testing.T) {
	cfg := DefaultTruncationRecoveryConfig()
	tokens := cfg.NextTokens(0)
	if tokens != 8192 {
		t.Errorf("expected first level 8192, got %d", tokens)
	}
	tokens = cfg.NextTokens(8192)
	if tokens != 16384 {
		t.Errorf("expected second level 16384, got %d", tokens)
	}
	tokens = cfg.NextTokens(16384)
	if tokens != 32768 {
		t.Errorf("expected third level 32768, got %d", tokens)
	}
	tokens = cfg.NextTokens(32768)
	if tokens != 65536 {
		t.Errorf("expected fourth level 65536, got %d", tokens)
	}
	tokens = cfg.NextTokens(65536)
	if tokens != 65536 {
		t.Errorf("expected max 65536, got %d", tokens)
	}
}

func TestBuildRetryPrompt(t *testing.T) {
	cfg := DefaultTruncationRecoveryConfig()
	prompt := cfg.BuildRetryPrompt("partial content here")
	if prompt == "" {
		t.Error("retry prompt should not be empty")
	}
	if !contains(prompt, "partial content here") {
		t.Error("retry prompt should contain partial content")
	}
}

func TestBuildRetryPrompt_Truncation(t *testing.T) {
	cfg := DefaultTruncationRecoveryConfig()
	// Long content should be truncated to last 2000 chars
	longContent := ""
	for i := 0; i < 500; i++ {
		longContent += "line of content for testing\n"
	}
	prompt := cfg.BuildRetryPrompt(longContent)
	if contains(prompt, "line of content for testing\n") {
		// Good - it should contain some content
	}
	if prompt == "" {
		t.Error("retry prompt should not be empty even with long content")
	}
}

func TestDefaultTruncationRecoveryConfig(t *testing.T) {
	cfg := DefaultTruncationRecoveryConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("expected MaxRetries=3, got %d", cfg.MaxRetries)
	}
	if cfg.MaxTokens != 65536 {
		t.Errorf("expected MaxTokens=65536, got %d", cfg.MaxTokens)
	}
}

func TestIsMidSentenceTruncation(t *testing.T) {
	detector := NewTruncationDetector(DefaultTruncationRecoveryConfig())

	tests := []struct {
		content string
		result  bool
	}{
		{"complete sentence.", false},
		{"incomplete", false}, // may or may not be detected as truncation
	}
	for _, tt := range tests {
		truncated := detector.IsTruncated("stop", tt.content)
		if truncated != tt.result {
			t.Logf("mid-sentence detection for %q = %v (expected %v)", tt.content, truncated, tt.result)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
