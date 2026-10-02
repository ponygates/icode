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

// secretPatterns are credential shapes that are never needed verbatim in a
// diagnostic log or a bug report, no matter which security level the user
// picked. They are deliberately narrow (known vendor prefixes plus key=value
// assignments with a credential-looking name) so redaction cannot mangle
// ordinary code, stack traces or file paths.
var secretPatterns = []*regexp.Regexp{
	// \b keeps identifiers like "task-…" / "disk-…" from matching the sk-
	// shapes: without it every ULID-style token in a log line would be eaten.
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`\bsk-proj-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}`),
	// AWS temporary-credential access keys (STS) share the 20-char shape.
	regexp.MustCompile(`\bASIA[0-9A-Z]{16}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`\b(github_pat_)[A-Za-z0-9_]{16,}`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`),
	// Incoming webhook URLs carry the secret in the path; the service name and
	// app/id prefix stay visible so the operator knows which hook leaked.
	regexp.MustCompile(`(?i)(hooks\.slack\.com/services/)[A-Za-z0-9/_-]{6,}`),
	regexp.MustCompile(`(?i)(discord(?:app)?\.com/api/webhooks/\d+/)[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`),
	// "Bearer <token>" / "Basic <credentials>" — the scheme name is kept, the
	// value is not, so a log line still says which auth mode was in play.
	regexp.MustCompile(`(?i)((?:bearer|basic|token)\s+)[A-Za-z0-9._~+/=\-]{8,}`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|secret|token|password|passwd|access[_-]?key)["']?\s*[:=]\s*)["']?[^\s"',;)}]{6,}`),
	// Runs last: a PEM block truncated by a crash never reaches its END line,
	// so anything after a lone BEGIN header is treated as key material.
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----[\s\S]+`),
}

// RedactSecrets masks credentials only — no PII, no paths. Safe and cheap to
// run over every log line, tool echo and error string, which is exactly why it
// exists: Redact is opt-in per security level, so before this the only place a
// leaked `sk-...` was scrubbed was the outbound message body, while cli.log,
// desktop.log and provider error text carried it out to disk in the clear.
func RedactSecrets(text string) string {
	if text == "" {
		return text
	}
	for _, re := range secretPatterns {
		text = re.ReplaceAllStringFunc(text, func(match string) string {
			// Keep any `key =` prefix so the operator can still tell which
			// setting leaked; drop only the value.
			if sub := re.FindStringSubmatchIndex(match); len(sub) >= 4 && sub[2] >= 0 {
				return match[:sub[3]] + "[REDACTED]"
			}
			// Otherwise keep the vendor prefix (sk-ant-, AKIA, xoxb-) as a
			// fingerprint, grown to the next separator so the prefix stays
			// whole — "sk-ant-", not "sk-ant".
			cut := 6
			for i := 6; i < len(match) && i <= 10; i++ {
				if match[i] == '-' || match[i] == '_' {
					cut = i + 1
					break
				}
			}
			if len(match) <= cut {
				return "[REDACTED]"
			}
			return match[:cut] + "[REDACTED]"
		})
	}
	return text
}

// RedactSecretsBytes is the io.Writer-friendly form of RedactSecrets.
func RedactSecretsBytes(p []byte) []byte {
	if len(p) == 0 {
		return p
	}
	return []byte(RedactSecrets(string(p)))
}
