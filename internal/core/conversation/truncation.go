// Package conversation — Output Truncation Recovery.
//
// Claude Code 核心机制：当模型输出被截断时（finish_reason="length"），
// 自动升级 max_tokens 并注入续写提示，让模型从中断处继续。
//
// 流程：
//  1. 检测截断（输出在 max_tokens 处被切断、JSON 不完整、工具调用被截断）
//  2. 升级 max_tokens（8K → 16K → 32K → 64K）
//  3. 注入续写提示（不道歉、不回顾、直接继续）
//  4. 最多重试 3 次

package conversation

import (
	"strings"
)

// TruncationRecoveryConfig controls the truncation retry behavior.
type TruncationRecoveryConfig struct {
	// MaxRetries is the maximum number of retry attempts. Default: 3.
	MaxRetries int

	// InitialTokens is the starting max_tokens. Default: 8192.
	InitialTokens int

	// MaxTokens is the maximum allowed max_tokens after escalation. Default: 65536.
	MaxTokens int

	// RetryPrompt is injected when retrying a truncated response.
	// The {} placeholder is replaced with the already-generated content.
	RetryPrompt string
}

// DefaultTruncationRecoveryConfig returns sensible defaults.
func DefaultTruncationRecoveryConfig() TruncationRecoveryConfig {
	return TruncationRecoveryConfig{
		MaxRetries:    3,
		InitialTokens: 8192,
		MaxTokens:     65536,
		RetryPrompt: `之前的内容已经被截断。请直接从中断处继续，不要道歉，不要回顾，不要重新开始。

已生成的内容末尾：
{}
(已保存的变量和逻辑不变，继续即可)`,
	}
}

// TruncationDetector checks if a response was truncated.
type TruncationDetector struct {
	config TruncationRecoveryConfig
}

// NewTruncationDetector creates a new detector.
func NewTruncationDetector(cfg TruncationRecoveryConfig) *TruncationDetector {
	return &TruncationDetector{config: cfg}
}

// IsTruncated checks if the assistant response was truncated based on
// finish_reason or content analysis.
func (d *TruncationDetector) IsTruncated(finishReason string, content string) bool {
	if finishReason == "length" {
		return true
	}

	// Check for truncated JSON (unbalanced braces/brackets)
	if isTruncatedJSON(content) {
		return true
	}

	// Check for truncated tool call
	if isTruncatedToolCall(content) {
		return true
	}

	// Check for content ending mid-sentence
	if isMidSentenceTruncation(content) {
		return true
	}

	return false
}

// NextTokens returns the next max_tokens value in the escalation chain.
func (d *TruncationRecoveryConfig) NextTokens(current int) int {
	levels := []int{8192, 16384, 32768, 65536}
	for _, l := range levels {
		if l > current {
			if l > d.MaxTokens {
				return d.MaxTokens
			}
			return l
		}
	}
	return d.MaxTokens
}

// BuildRetryPrompt creates the continuation prompt for truncated output.
func (d *TruncationRecoveryConfig) BuildRetryPrompt(partialContent string) string {
	// Keep only the last portion for context
	suffix := partialContent
	if len(suffix) > 2000 {
		suffix = suffix[len(suffix)-2000:]
	}
	return strings.Replace(d.RetryPrompt, "{}", suffix, 1)
}

// ============================================================================
// Truncation detection helpers
// ============================================================================

func isTruncatedJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	openBraces := strings.Count(s, "{")
	closeBraces := strings.Count(s, "}")
	openBrackets := strings.Count(s, "[")
	closeBrackets := strings.Count(s, "]")

	// Unbalanced braces indicate truncation
	if openBraces > closeBraces || openBrackets > closeBrackets {
		return true
	}

	// Ends with comma (more content expected)
	if strings.HasSuffix(s, ",") {
		return true
	}

	// Unbalanced quotes
	inString := false
	escape := false
	for _, r := range s {
		if escape {
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		if r == '"' {
			inString = !inString
		}
	}

	return inString
}

func isTruncatedToolCall(s string) bool {
	// Tool calls should end with proper JSON or with content
	s = strings.TrimSpace(s)

	// If the content ends with a backtick (unclosed code block)
	if strings.Count(s, "```")%2 != 0 {
		return true
	}

	// If content ends with an incomplete function signature or tool call pattern
	incompletePatterns := []string{
		`"arguments": "`,
		`"arguments": {`,
		`{"name": "`,
		`<function=`,
		`<tool_call>`,
	}
	for _, p := range incompletePatterns {
		if strings.Contains(s, p) {
			// Check if the pattern's structure completes
			switch {
			case strings.Contains(s, `"arguments": "`):
				// Needs closing quote
				if !strings.Contains(s, `"`+`"`) {
					return true
				}
			}
		}
	}

	return false
}

func isMidSentenceTruncation(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	// Content ending mid-sentence (last char is not sentence-ending punctuation)
	lastChar := s[len(s)-1]
	endingChars := []byte{'.', '!', '?', '\n', '}', ')', ']', '"', '`'}
	for _, ec := range endingChars {
		if lastChar == ec {
			return false
		}
	}

	// If the last "word" is very short and incomplete
	words := strings.Fields(s)
	if len(words) > 0 {
		lastWord := words[len(words)-1]
		// If last word doesn't end with punctuation and isn't a known short word
		if len(lastWord) < 3 && !isPunctuation(lastWord) {
			return true
		}
		// If last word is cut off (ends mid-character — uncommon in English but
		// possible in log output)
		if len(lastWord) > 0 {
			lastRune := []rune(lastWord)
			if len(lastRune) > 0 {
				// Check if it looks like it was cut (no vowel in last few chars)
				lastThree := lastWord
				if len(lastWord) > 3 {
					lastThree = lastWord[len(lastWord)-3:]
				}
				if !containsVowel(lastThree) && len(lastThree) >= 2 {
					return false // Probably an abbreviation, not truncation
				}
			}
		}
	}

	return false
}

func isPunctuation(s string) bool {
	if len(s) != 1 {
		return false
	}
	return strings.ContainsRune(".,!?;:)}]", rune(s[0]))
}

func containsVowel(s string) bool {
	return strings.ContainsAny(strings.ToLower(s), "aeiou")
}
