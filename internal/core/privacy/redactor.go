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
	idPattern = regexp.MustCompile(`[1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]`)

	phonePattern = regexp.MustCompile(`1[3-9]\d{9}`)

	emailPattern = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)

	internalIPPattern = regexp.MustCompile(`(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3})`)

	apiKeyPattern = regexp.MustCompile(`(sk-[a-zA-Z0-9]{20,}|sk-ant-[a-zA-Z0-9]{20,}|api[_-]?key[=:]\s*['"]?[a-zA-Z0-9_\-]{16,}|token[=:]\s*['"]?[a-zA-Z0-9_\-]{16,})`)

	creditCardPattern = regexp.MustCompile(`\b4\d{12,18}\b|\b5[1-5]\d{14}\b|\b3[47]\d{13}\b|\b6(?:011|5\d{2})\d{12}\b`)

	ssnPattern = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)

	jwtPattern = regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{10,}\.`)

	privateKeyPattern = regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA )?PRIVATE KEY-----`)

	winHomePattern = regexp.MustCompile(`[A-Z]:\\Users\\[^\x00\\/:"*?<>|]+\\`)
)

// Redact strips PII from the given text. Returns the sanitized version.
// This is applied to messages before sending to external LLM APIs when
// the security level is "desensitize".
func Redact(text string) string {
	result := text

	result = apiKeyPattern.ReplaceAllString(result, "[API KEY REDACTED]")

	result = creditCardPattern.ReplaceAllString(result, "[CC REDACTED]")

	result = ssnPattern.ReplaceAllString(result, "[SSN REDACTED]")

	result = jwtPattern.ReplaceAllString(result, "[JWT REDACTED]")

	result = privateKeyPattern.ReplaceAllString(result, "[PRIVATE KEY REDACTED]")

	result = winHomePattern.ReplaceAllStringFunc(result, func(match string) string {
		idx := strings.Index(match[3:], "\\")
		if idx < 0 {
			return "[USER PATH REDACTED]"
		}
		return match[:3] + "[USER]" + match[3+idx:]
	})

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
