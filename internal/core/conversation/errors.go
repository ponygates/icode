package conversation

import (
	"strings"
)

// friendlyModelError maps raw provider/network errors to a clear, actionable
// Chinese message so users never stare at a bare "HTTP 401" or a wall of JSON.
// The raw error text is appended (truncated) so engineers can still debug.
// Returns the friendly message when the error matches a known category, or the
// original error text otherwise.
func friendlyModelError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case containsAny(lower, "401", "unauthorized", "invalid api key", "authentication", "missing bearer"):
		return "⚠ 模型 API Key 无效或未配置（401）。请用 `icode auth set <provider> <key>` 或桌面「设置 → 提供商」填写正确的 Key。" + rawTail(msg)

	case containsAny(lower, "403", "forbidden", "permission denied", "not allowed", "access denied"):
		return "⚠ 模型服务拒绝访问（403）。检查 Key 的权限范围、账号是否被封禁或额度受限。" + rawTail(msg)

	case containsAny(lower, "429", "rate limit", "rate_limit", "too many requests", "quota", "insufficient", "limit reached", "billing"):
		return "⚠ 请求过于频繁或额度不足（429/限流）。稍等片刻重试，或检查账户余额/配额。" + rawTail(msg)

	case containsAny(lower, "500", "502", "503", "504", "internal server error", "server error", "overloaded", "service unavailable", "temporarily unavailable"):
		return "⚠ 模型服务端暂时不可用（5xx/过载）。稍后重试；持续失败可换模型（/model）或提供商。" + rawTail(msg)

	case containsAny(lower, "timeout", "timed out", "deadline exceeded", "context deadline"):
		return "⚠ 模型请求超时。可能是网络波动或模型负载高，重试一次；仍失败请检查网络与代理设置。" + rawTail(msg)

	case containsAny(lower, "connection refused", "no such host", "network is unreachable", "lookup", "tls", "certificate", "eof", "connection reset"):
		return "⚠ 无法连接模型服务（网络/地址问题）。检查 base URL、网络连接与代理。" + rawTail(msg)

	case containsAny(lower, "model", "not found", "unknown model"):
		if strings.Contains(lower, "not found") {
			return "⚠ 模型 ID 不存在或不可用。用 `/model` 或 `/models` 查看可用模型。" + rawTail(msg)
		}

	case containsAny(lower, "context window", "context length", "token limit", "too many tokens", "maximum context"):
		return "⚠ 超出模型上下文窗口。运行 `/compact` 压缩历史，或换一个窗口更大的模型。" + rawTail(msg)
	}

	return msg
}

// rawTail appends a truncated raw error so the underlying detail is not lost.
func rawTail(msg string) string {
	const max = 160
	if len(msg) <= max {
		return ""
	}
	return "\n（原始错误: " + msg[:max] + "…）"
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// FriendlyModelError is the exported wrapper around friendlyModelError, used
// by non-engine callers (e.g. the simple UI bridge) that need the same
// human-friendly model-error translation without reaching into the engine.
func FriendlyModelError(err error) string {
	return friendlyModelError(err)
}
