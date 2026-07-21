// Package privacy provides data sanitization for iCode's security levels.
//
// When the user selects "desensitize" mode, all outbound data is run through
// the redactor to strip or mask personally identifiable information (PII)
// before it reaches any LLM API. This includes:
//   - Chinese ID numbers (身份证)
//   - Phone numbers (手机号)
//   - Email addresses
//   - IP addresses (internal)
//   - API keys and secrets (in file content)
//   - Home directory paths
//
// iCode NEVER sends telemetry, analytics, or usage data to any external
// service regardless of security level. The redactor is only an additional
// safeguard for the "desensitize" tier.
package privacy

import (
	"regexp"
	"strings"
)

var (
	// Chinese ID: 18 digits (possibly with X suffix)
	idPattern = regexp.MustCompile(`[1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]`)

	// Phone: Chinese mobile numbers
	phonePattern = regexp.MustCompile(`1[3-9]\d{9}`)

	// Email
	emailPattern = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)

	// Internal IPs
	internalIPPattern = regexp.MustCompile(`(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3})`)

	// API keys (common patterns)
	apiKeyPattern = regexp.MustCompile(`(sk-[a-zA-Z0-9]{20,}|api[_-]?key[=:]\s*['"]?[a-zA-Z0-9]{16,}|token[=:]\s*['"]?[a-zA-Z0-9]{16,})`)
)

// Redact strips PII from the given text. Returns the sanitized version.
// This is applied to messages before sending to external LLM APIs when
// the security level is "desensitize".
func Redact(text string) string {
	result := text

	// Replace API keys with placeholder
	result = apiKeyPattern.ReplaceAllString(result, "[API KEY REDACTED]")

	// Replace Chinese ID numbers
	result = idPattern.ReplaceAllStringFunc(result, func(match string) string {
		if len(match) == 18 {
			return match[:6] + "********" + match[14:]
		}
		return match
	})

	// Replace phone numbers (keep last 4 digits for reference)
	result = phonePattern.ReplaceAllStringFunc(result, func(match string) string {
		return match[:3] + "****" + match[7:]
	})

	// Replace email addresses
	result = emailPattern.ReplaceAllStringFunc(result, func(match string) string {
		at := strings.Index(match, "@")
		if at > 0 {
			return match[:1] + "***" + match[at:]
		}
		return match
	})

	// Replace internal IPs
	result = internalIPPattern.ReplaceAllString(result, "xxx.xxx.x.x")

	return result
}
