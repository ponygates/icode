// Package conversation — Doom Loop Detection + 拒绝跟踪.
//
// Doom Loop 检测：当 AI 连续 3 次以上发出相同的工具调用签名时，
// 暂停执行并要求用户输入。防止陷入"工具调用→失败→重试"的死循环。
//
// 拒绝跟踪：当用户拒绝同一工具的调用达到阈值时，强制 AI 改变策略。
//
// 参考实现：
//   - Claude Code: 每个工具最多连续拒绝 3 次，总共 20 次
//   - OpenCode: 3 次连续相同工具调用签名暂停

package conversation

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// breakerState is the classic three-state circuit breaker:
//
//	closed    → normal operation; consecutive failures are counted
//	open      → tool is blocked for a cooldown; failures have tripped it
//	half-open → cooldown elapsed; exactly ONE probe call is allowed. If it
//	            succeeds the breaker closes; if it fails it re-opens.
//
// This fixes the v1 "closed ↔ open" binary that only recovered on success:
// an open breaker now heals automatically after a cooldown via a single
// probe, so a temporarily-broken tool is retried after the storm passes
// without the model hammering it every turn (本书 ch.23 熔断机制).
type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// toolBreaker tracks one tool's breaker.
type toolBreaker struct {
	state     breakerState
	failures  int       // consecutive failures since last success
	trippedAt time.Time // when the breaker opened (drives cooldown)
	probeUsed bool      // half-open: whether the single probe was consumed
}

// CircuitStatus is a snapshot of one tool's breaker, for surfacing to UI.
type CircuitStatus struct {
	Tool     string
	State    string // "closed" | "open" | "half_open"
	Failures int
	RetryIn  time.Duration // remaining cooldown when open, 0 otherwise
}

// DoomLoopDetector monitors tool call patterns to detect and break
// infinite tool-call loops.
type DoomLoopDetector struct {
	mu sync.Mutex

	// Recent tool call signatures (rolling window)
	signatures []string

	// Max consecutive identical signatures before triggering
	maxConsecutive int

	// Track rejections per tool
	rejections      map[string]int // tool name → consecutive rejections
	rejectionsTotal int            // total rejections across all tools

	// Track consecutive execution failures per tool (熔断/circuit breaker).
	// Unlike rejections (user said no), failures mean the tool itself keeps
	// erroring — bash build errors, fetch timeouts, read errors. Without a
	// breaker the model would retry the same broken call forever, burning
	// tokens and stalling the loop (Claude Code harness ch.23).
	//
	// Each tool has an independent three-state breaker (see breakerState):
	// closed → open (on maxFailuresPerTool) → half-open (after cooldown, one
	// probe) → closed (probe ok) or open again (probe failed).
	breakers map[string]*toolBreaker

	// breakerCooldown is how long an open breaker stays blocked before it
	// half-opens and admits a single probe call.
	breakerCooldown time.Duration

	// Thresholds
	maxRejectionsPerTool int // per-tool max consecutive rejections
	maxRejectionsTotal   int // total rejections before forcing strategy change
	maxFailuresPerTool   int // per-tool max consecutive failures before trip
}

// NewDoomLoopDetector creates a detector with sensible defaults.
func NewDoomLoopDetector() *DoomLoopDetector {
	return &DoomLoopDetector{
		signatures:           make([]string, 0, 10),
		maxConsecutive:       3,
		rejections:           make(map[string]int),
		maxRejectionsPerTool: 3,
		maxRejectionsTotal:   20,
		breakers:             make(map[string]*toolBreaker),
		breakerCooldown:      30 * time.Second,
		maxFailuresPerTool:   3,
	}
}

// RecordCall records a tool call and returns true if it's part of a doom loop.
func (d *DoomLoopDetector) RecordCall(toolName, arguments string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	sig := callSignature(toolName, arguments)
	d.signatures = append(d.signatures, sig)

	// Keep only the last N*2 signatures
	maxSig := d.maxConsecutive * 3
	if len(d.signatures) > maxSig {
		d.signatures = d.signatures[len(d.signatures)-maxSig:]
	}

	return d.isDoomLoopLocked(sig)
}

// isDoomLoopLocked checks if the same signature has appeared too many times.
func (d *DoomLoopDetector) isDoomLoopLocked(sig string) bool {
	if len(d.signatures) < d.maxConsecutive {
		return false
	}

	// Check the last N signatures
	count := 0
	for i := len(d.signatures) - 1; i >= 0; i-- {
		if d.signatures[i] == sig {
			count++
		} else {
			break // only check consecutive
		}
	}

	return count >= d.maxConsecutive
}

// RecordRejection records a user rejection of a tool call.
// Returns true if the rejection threshold has been reached, forcing a
// strategy change.
func (d *DoomLoopDetector) RecordRejection(toolName string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.rejections[toolName]++
	d.rejectionsTotal++

	// Per-tool threshold reached
	if d.rejections[toolName] >= d.maxRejectionsPerTool {
		return true
	}

	// Total threshold reached
	if d.rejectionsTotal >= d.maxRejectionsTotal {
		return true
	}

	return false
}

// Reset clears all tracking — called when the user intervenes or the
// conversation takes a new direction.
func (d *DoomLoopDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.signatures = make([]string, 0, 10)
	d.rejections = make(map[string]int)
	d.rejectionsTotal = 0
	d.breakers = make(map[string]*toolBreaker)
}

// ResetToolRejections resets rejections for a specific tool.
func (d *DoomLoopDetector) ResetToolRejections(toolName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.rejections, toolName)
}

// CheckBreaker is consulted BEFORE a tool runs. It returns whether the call
// may proceed and, when blocked, how long until a probe is admitted.
//
//   - closed:    allow.
//   - open:      block while the cooldown has not elapsed. Once elapsed, the
//     breaker half-opens and the next call IS the single probe.
//   - half-open: allow exactly one probe call; the probe was consumed.
//
// This is the entry point of the three-state breaker.
func (d *DoomLoopDetector) CheckBreaker(toolName string) (allowed bool, retryIn time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.checkBreakerLocked(toolName, time.Now())
}

func (d *DoomLoopDetector) checkBreakerLocked(toolName string, now time.Time) (bool, time.Duration) {
	b := d.breakers[toolName]
	if b == nil {
		return true, 0
	}
	switch b.state {
	case breakerOpen:
		remaining := b.trippedAt.Add(d.breakerCooldown).Sub(now)
		if remaining > 0 {
			return false, remaining
		}
		// Cooldown elapsed → half-open, this call is the probe.
		b.state = breakerHalfOpen
		b.probeUsed = true
		return true, 0
	case breakerHalfOpen:
		if b.probeUsed {
			return false, 0 // the single probe was already consumed
		}
		b.probeUsed = true
		return true, 0
	default:
		return true, 0
	}
}

// RecordFailure records a consecutive execution failure for a tool. It returns
// true when the call just TRIPPED the breaker (closed → open), so the caller
// can surface a "breaker tripped" message. In half-open, a failed probe
// re-opens the breaker.
func (d *DoomLoopDetector) RecordFailure(toolName string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recordFailureLocked(toolName, time.Now())
}

func (d *DoomLoopDetector) recordFailureLocked(toolName string, now time.Time) bool {
	b := d.breakers[toolName]
	if b == nil {
		b = &toolBreaker{}
		d.breakers[toolName] = b
	}
	switch b.state {
	case breakerHalfOpen:
		// The probe failed → re-open with a fresh cooldown.
		b.state = breakerOpen
		b.trippedAt = now
		b.failures++
		return true
	case breakerOpen:
		// Shouldn't be reached via CheckBreaker, but be defensive: count and
		// stay open.
		b.failures++
		return true
	default: // closed
		b.failures++
		if b.failures >= d.maxFailuresPerTool {
			b.state = breakerOpen
			b.trippedAt = now
			return true
		}
		return false
	}
}

// ResetToolFailures clears the failure counter for a tool and, if the breaker
// was half-open (probe succeeded), closes it again. Called when a tool
// finally succeeds, so a flaky tool that recovers is not kept tripped.
func (d *DoomLoopDetector) ResetToolFailures(toolName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b := d.breakers[toolName]
	if b == nil {
		return
	}
	b.failures = 0
	b.probeUsed = false
	if b.state == breakerHalfOpen {
		b.state = breakerClosed
	}
}

// FailureStatus returns the current consecutive-failure counts per tool.
func (d *DoomLoopDetector) FailureStatus() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make(map[string]int, len(d.breakers))
	for k, b := range d.breakers {
		result[k] = b.failures
	}
	return result
}

// CircuitStatus returns a snapshot of every tool's breaker for the UI layer.
func (d *DoomLoopDetector) CircuitStatus() []CircuitStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	out := make([]CircuitStatus, 0, len(d.breakers))
	for tool, b := range d.breakers {
		if b.state == breakerClosed && b.failures == 0 {
			continue // not worth showing
		}
		state := "closed"
		var retryIn time.Duration
		switch b.state {
		case breakerOpen:
			state = "open"
			if r := b.trippedAt.Add(d.breakerCooldown).Sub(now); r > 0 {
				retryIn = r
			}
		case breakerHalfOpen:
			state = "half_open"
		}
		out = append(out, CircuitStatus{
			Tool:     tool,
			State:    state,
			Failures: b.failures,
			RetryIn:  retryIn,
		})
	}
	return out
}

// DoomLoopStatus returns a human-readable status of the current loop state.
func (d *DoomLoopDetector) DoomLoopStatus() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.signatures) < 2 {
		return ""
	}

	// Find the most frequent recent signature
	sigCount := make(map[string]int)
	for _, s := range d.signatures {
		sigCount[s]++
	}

	var maxSig string
	maxCount := 0
	for s, c := range sigCount {
		if c > maxCount {
			maxCount = c
			maxSig = s
		}
	}

	if maxCount >= d.maxConsecutive {
		return fmt.Sprintf("⚠️ 检测到可能的 Doom Loop：'%s' 在最近 %d 次调用中出现了 %d 次",
			maxSig, len(d.signatures), maxCount)
	}

	return ""
}

// RejectionStatus returns rejection counts per tool.
func (d *DoomLoopDetector) RejectionStatus() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()

	result := make(map[string]int, len(d.rejections))
	for k, v := range d.rejections {
		result[k] = v
	}
	return result
}

// callSignature creates a deterministic hash of a tool call.
func callSignature(toolName, arguments string) string {
	h := sha256.Sum256([]byte(toolName + "\x00" + arguments))
	return fmt.Sprintf("%s:%x", toolName, h[:4])
}
