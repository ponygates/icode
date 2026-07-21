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
)

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

	// Thresholds
	maxRejectionsPerTool int // per-tool max consecutive rejections
	maxRejectionsTotal   int // total rejections before forcing strategy change
}

// NewDoomLoopDetector creates a detector with sensible defaults.
func NewDoomLoopDetector() *DoomLoopDetector {
	return &DoomLoopDetector{
		signatures:           make([]string, 0, 10),
		maxConsecutive:       3,
		rejections:           make(map[string]int),
		maxRejectionsPerTool: 3,
		maxRejectionsTotal:   20,
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
}

// ResetToolRejections resets rejections for a specific tool.
func (d *DoomLoopDetector) ResetToolRejections(toolName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.rejections, toolName)
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
