package tui

import (
	"bufio"
	"io"
	"testing"
	"time"
)

// ── 滚轮乱码复现矩阵（F12 取证：inputBuf 出现 "64;34;29M"×18）─────────────
// screen-dump 铁证：SGR 滚轮尾段灌入 inputBuf，且 cli.log 无任何 drain/main
// 站点的字符记录、无 csi-agg-split —— 推理矛盾，直接跑完整管道复现。
// 变体 B（拆帧）是头号嫌疑：ConPTY/WT 把一个滚轮事件拆成两次写入且间隔
// 超过泵层 15ms 重组窗口 → pumpEscape 回落 dispatch "\x1b[<"，尾段
// "64;34;29M" 晚到 → drainStream 的 swallowCSI 只等 10ms → 超时放弃 →
// 尾段被 handleQueueKey 当打字逐字插入 inputBuf。

// startFullPump wires the COMPLETE production pipeline (rawBytePump → byteCh
// → keyPump → keyCh) against an io.Pipe and returns the write end.
func startFullPump(t *testing.T, tu *TUI) *io.PipeWriter {
	t.Helper()
	pr, pw := io.Pipe()
	tu.reader = bufio.NewReader(pr)
	go tu.rawBytePump()
	go tu.keyPump()
	t.Cleanup(func() {
		_ = pw.Close()
		close(tu.keyStop)
	})
	return pw
}

// mainLoopSim mimics the raw-mode main loop: read keyCh → handleKey. Exits
// after dur of idleness.
func mainLoopSim(tu *TUI, dur time.Duration) {
	for {
		select {
		case r, ok := <-tu.keyCh:
			if !ok {
				return
			}
			if !tu.handleKey(r) {
				return
			}
		case <-time.After(dur):
			return
		}
	}
}

const wheelSGR = "\x1b[<64;34;29M"

func inputBufOf(t *testing.T, tu *TUI) string {
	t.Helper()
	tu.mu.Lock()
	defer tu.mu.Unlock()
	return tu.inputBuf
}

// TestReproWheelStreamingBurst — variant A: streaming, wheel as one burst.
func TestReproWheelStreamingBurst(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.writer = io.Discard
	pw := startFullPump(t, tu)
	tu.streaming = true
	go tu.drainStream()
	for i := 0; i < 5; i++ {
		_, _ = pw.Write([]byte(wheelSGR))
	}
	time.Sleep(400 * time.Millisecond)
	if buf := inputBufOf(t, tu); buf != "" {
		t.Fatalf("variant A leaked: %q", buf)
	}
	tu.streamDone <- struct{}{}
}

// TestReproWheelStreamingSplit — variant B: streaming, wheel split as
// "ESC[<" + 40ms gap + "64;34;29M" (the suspected ConPTY/WT shape).
func TestReproWheelStreamingSplit(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.writer = io.Discard
	pw := startFullPump(t, tu)
	tu.streaming = true
	go tu.drainStream()
	for i := 0; i < 3; i++ {
		_, _ = pw.Write([]byte("\x1b[<"))
		time.Sleep(40 * time.Millisecond)
		_, _ = pw.Write([]byte("64;34;29M"))
	}
	time.Sleep(500 * time.Millisecond)
	if buf := inputBufOf(t, tu); buf != "" {
		t.Fatalf("variant B leaked: %q", buf)
	}
	tu.streamDone <- struct{}{}
}

// TestReproWheelMainBurst — variant C: main loop, wheel as one burst.
func TestReproWheelMainBurst(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.writer = io.Discard
	pw := startFullPump(t, tu)
	go mainLoopSim(tu, 300*time.Millisecond)
	for i := 0; i < 5; i++ {
		_, _ = pw.Write([]byte(wheelSGR))
	}
	time.Sleep(500 * time.Millisecond)
	if buf := inputBufOf(t, tu); buf != "" {
		t.Fatalf("variant C leaked: %q", buf)
	}
}

// TestReproWheelMainSplit — variant D: main loop, split wheel. handleMouse's
// parseSGRMouse BLOCKS (no timeout), so the late tail is still consumed —
// this should NOT leak even before the fix.
func TestReproWheelMainSplit(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.writer = io.Discard
	pw := startFullPump(t, tu)
	go mainLoopSim(tu, 300*time.Millisecond)
	for i := 0; i < 3; i++ {
		_, _ = pw.Write([]byte("\x1b[<"))
		time.Sleep(40 * time.Millisecond)
		_, _ = pw.Write([]byte("64;34;29M"))
	}
	time.Sleep(500 * time.Millisecond)
	if buf := inputBufOf(t, tu); buf != "" {
		t.Fatalf("variant D leaked: %q", buf)
	}
}

// TestReproWheelStreamingEscSplit — variant E: "ESC" + 60ms gap + "[<64;34;29M".
// The gap exceeds the pump's 30ms first window, so the pump dispatches a LONE
// ESC — the shape behind the phantom "stream interrupted by itself" symptoms
// in cli.log (4 drain-site ESC entries the user never pressed). drainStream
// must wait escSwallowWait (100ms) for the '[', reassemble the sequence and
// swallow it — NOT interrupt the stream.
func TestReproWheelStreamingEscSplit(t *testing.T) {
	tu := newTestTUI()
	tu.rawMode = true
	tu.writer = io.Discard
	interrupted := false
	tu.callback = &testCallback{onInterrupt: func() { interrupted = true }}
	pw := startFullPump(t, tu)
	tu.streaming = true
	go tu.drainStream()
	_, _ = pw.Write([]byte("\x1b"))
	time.Sleep(60 * time.Millisecond)
	_, _ = pw.Write([]byte("[<64;34;29M"))
	time.Sleep(500 * time.Millisecond)
	if buf := inputBufOf(t, tu); buf != "" {
		t.Fatalf("variant E leaked: %q", buf)
	}
	if interrupted {
		t.Fatal("variant E: split wheel sequence must NOT interrupt the stream")
	}
	tu.streamDone <- struct{}{}
}
