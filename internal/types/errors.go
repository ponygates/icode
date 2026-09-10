package types

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// RateLimitError — a transient rate-limit rejection carrying the server's own
// wait hint
// ============================================================================

// RateLimitError is a transient rate-limit / quota rejection (HTTP 429 or an
// equivalent provider signal).
//
// Providers return this instead of a bare fmt.Errorf so the engine can stop
// guessing its backoff schedule: the server knows exactly when the window
// resets and normally advertises it (RFC 9110 `Retry-After` or a vendor
// variant). Honouring that value avoids two failure modes — retrying too early
// (the request fails again, burning an attempt and worsening the limit) and
// waiting far longer than necessary (the user stares at a stalled turn).
//
// RetryAfter is 0 when the provider supplied no usable hint; callers must then
// fall back to their own exponential schedule with jitter.
type RateLimitError struct {
	Provider   string
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return "rate limited"
	}
	var b strings.Builder
	if e.Provider != "" {
		b.WriteString(e.Provider)
		b.WriteString(": ")
	}
	b.WriteString("rate limit exceeded")
	if e.StatusCode > 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.StatusCode)
	}
	if e.RetryAfter > 0 {
		fmt.Fprintf(&b, ", server asked to retry after %s", e.RetryAfter.Round(time.Second))
	}
	if e.Body != "" {
		b.WriteString(" — ")
		b.WriteString(e.Body)
	}
	return b.String()
}

// ============================================================================
// Retry-After header extraction
// ============================================================================

// httpDateLayouts mirrors net/http's accepted date formats so this package
// stays free of a net/http dependency.
var httpDateLayouts = []string{
	"Mon, 02 Jan 2006 15:04:05 GMT",  // http.TimeFormat
	"Monday, 02-Jan-06 15:04:05 GMT", // RFC 850
	"Mon Jan _2 15:04:05 2006",       // ANSI C asctime
}

// RetryAfterFromHeaders extracts a provider's own wait hint from response
// headers. get is normally resp.Header.Get.
//
// Vendors advertise the reset instant in several dialects, so we probe the
// common ones in order of specificity:
//
//	Retry-After: 30                         → delay seconds (RFC 9110, common)
//	Retry-After: Wed, 21 Oct 2015 07:28:00 GMT → absolute HTTP-date (RFC 9110)
//	retry-after-ms: 1500                    → delay milliseconds (OpenAI style)
//	x-ratelimit-reset-after: 5              → delay seconds (shared vendor style)
//	anthropic-ratelimit-requests-reset: <HTTP-date>
//	anthropic-ratelimit-tokens-reset:   <HTTP-date>
//
// Returns 0 when no usable hint is present — callers then use their own
// schedule.
func RetryAfterFromHeaders(get func(string) string) time.Duration {
	if get == nil {
		return 0
	}

	// 1) Standard Retry-After: delay-seconds or HTTP-date.
	if v := strings.TrimSpace(get("Retry-After")); v != "" {
		if d, ok := parseDelaySeconds(v); ok {
			return d
		}
		if d, ok := parseHTTPDate(v, time.Now()); ok {
			return d
		}
	}

	// 2) Millisecond variant (OpenAI and friends).
	if v := strings.TrimSpace(get("retry-after-ms")); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}

	// 3) "seconds until reset" variants.
	for _, h := range []string{"x-ratelimit-reset-after", "x-ratelimit-reset-requests"} {
		if v := strings.TrimSpace(get(h)); v != "" {
			if d, ok := parseDelaySeconds(v); ok {
				return d
			}
		}
	}

	// 4) Absolute reset instants.
	for _, h := range []string{
		"anthropic-ratelimit-requests-reset",
		"anthropic-ratelimit-tokens-reset",
		"x-ratelimit-reset",
	} {
		if v := strings.TrimSpace(get(h)); v != "" {
			if d, ok := parseHTTPDate(v, time.Now()); ok {
				return d
			}
			// x-ratelimit-reset is sometimes a Unix epoch instead of a date.
			if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
				if d := time.Until(time.Unix(int64(secs), 0)); d > 0 {
					return d
				}
			}
		}
	}

	return 0
}

// parseDelaySeconds parses the RFC 9110 delay-seconds form. Some vendors emit
// fractional seconds ("1.5"), which the RFC does not allow but is harmless to
// accept.
func parseDelaySeconds(v string) (time.Duration, bool) {
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second)), true
	}
	return 0, false
}

// parseHTTPDate parses an absolute reset instant and converts it to a duration
// from now. Past instants clamp to a small positive value rather than zero, so
// callers treat "already reset" as "retry immediately" instead of "no hint".
func parseHTTPDate(v string, now time.Time) (time.Duration, bool) {
	for _, layout := range httpDateLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			if d := t.Sub(now); d > 0 {
				return d, true
			}
			return time.Millisecond, true
		}
	}
	return 0, false
}
