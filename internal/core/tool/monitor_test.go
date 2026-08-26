package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMonitorStreamsShellOutput(t *testing.T) {
	defer KillAllBgTasks()
	// Start through the global manager — that is where Monitor looks.
	bgID, err := bgTasks.Start("echo hello-monitor", "")
	if err != nil {
		t.Skipf("shell unavailable: %v", err)
	}
	mt := &MonitorTool{}
	res, execErr := mt.Execute(context.Background(), `{"task_id":"`+bgID+`","max_seconds":10,"quiet_seconds":1}`)
	if execErr != nil || !res.Success {
		t.Fatalf("Execute: %v / %+v", execErr, res)
	}
	if !strings.Contains(res.Content, "monitor "+bgID) {
		t.Errorf("missing header: %s", res.Content)
	}

	// Unknown ids fail cleanly.
	res2, _ := mt.Execute(context.Background(), `{"task_id":"bg-999999"}`)
	if res2.Success {
		t.Error("unknown task should fail")
	}
}

func TestMonitorQuietPeriodReturns(t *testing.T) {
	defer KillAllBgTasks()
	bgID, err := bgTasks.Start("ping -n 30 127.0.0.1 > nul 2>&1", "") // long-running, silent-ish
	if err != nil {
		t.Skipf("shell unavailable: %v", err)
	}
	mt := &MonitorTool{}
	start := time.Now()
	res, _ := mt.Execute(context.Background(), `{"task_id":"`+bgID+`","max_seconds":30,"quiet_seconds":1}`)
	elapsed := time.Since(start)
	if !res.Success {
		t.Fatalf("Execute failed: %+v", res)
	}
	// Must return well before the 30s max (quiet after ~1s of silence).
	if elapsed > 15*time.Second {
		t.Errorf("quiet-period return took %s, expected ~1-3s", elapsed)
	}
	if task := bgTasks.Get(bgID); task != nil {
		task.cancel()
	}
}

func TestMonitorAgtTaskStatusOnly(t *testing.T) {
	slow := &fakeRunner{delay: 5 * time.Second}
	id, _ := launchBackgroundAgent(slow, "explore", "long")
	defer KillAllAgentTasks()

	mt := &MonitorTool{}
	res, _ := mt.Execute(context.Background(), `{"task_id":"`+id+`","max_seconds":1}`)
	if !res.Success || !strings.Contains(res.Content, "still running") {
		t.Errorf("agt monitor = %+v", res)
	}
	KillAllAgentTasks()
}

func TestSpawnDepthCap(t *testing.T) {
	depth3 := WithSpawnDepth(context.Background(), MaxSpawnDepth)
	tt := NewTaskTool(&fakeRunner{delay: time.Millisecond})
	res, _ := tt.Execute(depth3, `{"name":"explore","prompt":"nested too deep"}`)
	if res.Success {
		t.Fatal("dispatch at max depth should be refused")
	}
	if !strings.Contains(res.Error, "nesting too deep") {
		t.Errorf("error = %q", res.Error)
	}

	// Depth 0 (main conversation) passes the gate and reaches the runner.
	depth0 := context.Background()
	fr2 := &fakeRunner{delay: time.Millisecond}
	tt2 := NewTaskTool(fr2)
	if res2, _ := tt2.Execute(depth0, `{"name":"explore","prompt":"ok"}`); !res2.Success {
		t.Errorf("top-level dispatch refused: %+v", res2)
	}
}

func TestSpawnDepthContextRoundtrip(t *testing.T) {
	ctx := context.Background()
	if got := SpawnDepthFromContext(ctx); got != 0 {
		t.Errorf("default depth = %d, want 0", got)
	}
	c1 := WithSpawnDepth(ctx, 1)
	c2 := WithSpawnDepth(c1, 2)
	if got := SpawnDepthFromContext(c2); got != 2 {
		t.Errorf("chained depth = %d, want 2", got)
	}
}
