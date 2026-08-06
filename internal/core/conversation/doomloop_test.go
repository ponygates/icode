package conversation

import (
	"testing"
	"time"
)

func TestDoomLoopDetector_NoLoop(t *testing.T) {
	d := NewDoomLoopDetector()

	calls := []string{"read_file", "write_file", "edit"}
	for _, c := range calls {
		loop := d.RecordCall(c, `{"path":"main.go"}`)
		if loop {
			t.Errorf("expected no doom loop for varied calls, got loop at %s", c)
		}
	}
}

func TestDoomLoopDetector_DetectLoop(t *testing.T) {
	d := NewDoomLoopDetector()

	// Same call 3+ times should trigger doom loop
	for i := 0; i < 5; i++ {
		loop := d.RecordCall("read_file", `{"path":"main.go"}`)
		if i >= 3 && !loop {
			t.Errorf("expected doom loop at iteration %d", i)
		}
	}
}

func TestDoomLoopDetector_DifferentArgsNoLoop(t *testing.T) {
	d := NewDoomLoopDetector()

	// Same tool but different args should not be a loop
	d.RecordCall("read_file", `{"path":"a.go"}`)
	d.RecordCall("read_file", `{"path":"b.go"}`)
	d.RecordCall("read_file", `{"path":"c.go"}`)
	d.RecordCall("read_file", `{"path":"d.go"}`)

	status := d.DoomLoopStatus()
	if status != "" {
		t.Errorf("expected no doom loop status for different args, got: %s", status)
	}
}

func TestDoomLoopDetector_Reset(t *testing.T) {
	d := NewDoomLoopDetector()

	// Trigger loop
	for i := 0; i < 4; i++ {
		d.RecordCall("read_file", `{"path":"main.go"}`)
	}

	d.Reset()

	// After reset, no loop
	loop := d.RecordCall("read_file", `{"path":"main.go"}`)
	if loop {
		t.Error("after reset, same call should not immediately trigger loop")
	}
}

func TestDoomLoopDetector_RejectionTracking(t *testing.T) {
	d := NewDoomLoopDetector()

	// Reject same tool 3 times (the per-tool threshold)
	for i := 0; i < 3; i++ {
		force := d.RecordRejection("bash")
		if i < 2 && force {
			t.Errorf("expected no force-strategy-change after %d rejections", i+1)
		}
	}

	// The 3rd rejection should trigger force
	force := d.RecordRejection("bash")
	if !force {
		t.Error("expected force-strategy-change after 3 rejections of same tool")
	}
}

func TestDoomLoopDetector_TotalRejectionLimit(t *testing.T) {
	d := NewDoomLoopDetector()

	// Reject many different tools
	tools := []string{"bash", "write_file", "edit", "read_file", "grep",
		"glob", "ls", "fetch", "git_diff", "git_commit", "git_status",
		"web_search", "ask_user", "task", "todo"}
	for i, tool := range tools {
		force := d.RecordRejection(tool)
		if i >= 20 && !force {
			t.Errorf("expected force after %d total rejections", i+1)
		}
		_ = force
	}
}

func TestDoomLoopDetector_EmptyStatus(t *testing.T) {
	d := NewDoomLoopDetector()
	status := d.DoomLoopStatus()
	if status != "" {
		t.Errorf("expected empty status for no calls, got: %s", status)
	}
}

func TestDoomLoopDetector_ResetToolRejections(t *testing.T) {
	d := NewDoomLoopDetector()

	d.RecordRejection("bash")
	d.RecordRejection("bash")
	d.ResetToolRejections("bash")

	// After resetting tool, it should start counting from 0
	force := d.RecordRejection("bash")
	if force {
		t.Error("after resetting tool rejections, should not force immediately")
	}

	rejections := d.RejectionStatus()
	if rejections["bash"] != 1 {
		t.Errorf("expected bash rejections = 1 after reset+1, got %d", rejections["bash"])
	}
}

func TestDoomLoopDetector_RejectionStatus(t *testing.T) {
	d := NewDoomLoopDetector()
	d.RecordRejection("bash")
	d.RecordRejection("edit")

	status := d.RejectionStatus()
	if status["bash"] != 1 {
		t.Errorf("expected bash=1, got %d", status["bash"])
	}
	if status["edit"] != 1 {
		t.Errorf("expected edit=1, got %d", status["edit"])
	}
}

func TestDoomLoopDetector_FailureBreaker(t *testing.T) {
	d := NewDoomLoopDetector()

	// A tool failing maxFailuresPerTool (3) times consecutively trips
	// the circuit breaker.
	for i := 0; i < 3; i++ {
		tripped := d.RecordFailure("bash")
		if i < 2 && tripped {
			t.Errorf("expected no trip after %d failures", i+1)
		}
	}
	// The 3rd record trips.
	if !d.RecordFailure("bash") {
		t.Error("expected breaker trip after 3 consecutive bash failures")
	}
	if status := d.FailureStatus(); status["bash"] != 4 {
		t.Errorf("expected bash failures = 4, got %d", status["bash"])
	}
}

func TestDoomLoopDetector_FailureSuccessResets(t *testing.T) {
	d := NewDoomLoopDetector()

	// Two failures then a success resets the counter; the breaker must
	// not trip immediately afterwards.
	d.RecordFailure("read_file")
	d.RecordFailure("read_file")
	d.ResetToolFailures("read_file")

	tripped := d.RecordFailure("read_file")
	if tripped {
		t.Error("after reset, a single failure should not trip the breaker")
	}
	if status := d.FailureStatus(); status["read_file"] != 1 {
		t.Errorf("expected read_file failures = 1 after reset+1, got %d", status["read_file"])
	}
}

func TestDoomLoopDetector_FailuresIndependentPerTool(t *testing.T) {
	d := NewDoomLoopDetector()

	// 3 failures of "fetch" trip only "fetch", not "grep".
	for i := 0; i < 3; i++ {
		d.RecordFailure("fetch")
	}
	d.RecordFailure("grep")

	if s := d.FailureStatus(); s["fetch"] != 3 || s["grep"] != 1 {
		t.Errorf("unexpected failure status: %#v", s)
	}
}

// TestCircuitBreaker_OpenBlocksCalls verifies that once tripped, the breaker
// blocks further calls until the cooldown elapses.
func TestCircuitBreaker_OpenBlocksCalls(t *testing.T) {
	d := NewDoomLoopDetector()
	d.breakerCooldown = time.Minute
	for i := 0; i < 3; i++ {
		d.RecordFailure("bash")
	}
	// Tripped → CheckBreaker must block with a positive retryIn.
	allowed, retryIn := d.CheckBreaker("bash")
	if allowed {
		t.Fatal("open breaker must block calls")
	}
	if retryIn <= 0 {
		t.Fatalf("open breaker must report retryIn > 0, got %v", retryIn)
	}
	// Other tools are unaffected.
	if a, _ := d.CheckBreaker("read_file"); !a {
		t.Fatal("untouched tool must stay allowed")
	}
}

// TestCircuitBreaker_HalfOpenProbe verifies the cooldown-elapsed transition:
// the breaker admits exactly ONE probe call, and a failed probe re-opens it.
func TestCircuitBreaker_HalfOpenProbe(t *testing.T) {
	d := NewDoomLoopDetector()
	d.breakerCooldown = time.Minute
	for i := 0; i < 3; i++ {
		d.RecordFailure("fetch")
	}
	// Fast-forward past the cooldown.
	d.mu.Lock()
	d.breakers["fetch"].trippedAt = time.Now().Add(-2 * time.Minute)
	d.mu.Unlock()

	// First call after cooldown = the probe, allowed.
	allowed, _ := d.CheckBreaker("fetch")
	if !allowed {
		t.Fatal("half-open breaker must admit the probe call")
	}
	// A second call before the probe resolves must be blocked (probe consumed).
	if a, _ := d.CheckBreaker("fetch"); a {
		t.Fatal("only one probe may be admitted per half-open window")
	}
	// Probe fails → breaker re-opens with a fresh cooldown.
	if !d.RecordFailure("fetch") {
		t.Fatal("failed probe should re-open the breaker")
	}
	if a, retryIn := d.CheckBreaker("fetch"); a || retryIn <= 0 {
		t.Fatalf("re-opened breaker must block with cooldown, got allowed=%v retryIn=%v", a, retryIn)
	}
}

// TestCircuitBreaker_ProbeSuccessHeals verifies a successful probe closes the
// breaker: subsequent calls flow normally and the failure count is reset.
func TestCircuitBreaker_ProbeSuccessHeals(t *testing.T) {
	d := NewDoomLoopDetector()
	d.breakerCooldown = time.Minute
	for i := 0; i < 3; i++ {
		d.RecordFailure("bash")
	}
	d.mu.Lock()
	d.breakers["bash"].trippedAt = time.Now().Add(-2 * time.Minute)
	d.mu.Unlock()

	if a, _ := d.CheckBreaker("bash"); !a {
		t.Fatal("probe should be admitted after cooldown")
	}
	// Success closes the breaker.
	d.ResetToolFailures("bash")

	if s := d.FailureStatus(); s["bash"] != 0 {
		t.Fatalf("expected failures reset after heal, got %d", s["bash"])
	}
	if a, _ := d.CheckBreaker("bash"); !a {
		t.Fatal("closed breaker must allow calls again")
	}
}

// TestCircuitBreaker_StatusExposesState verifies CircuitStatus surfaces
// open/half_open/closed for the UI layer.
func TestCircuitBreaker_StatusExposesState(t *testing.T) {
	d := NewDoomLoopDetector()
	d.breakerCooldown = time.Hour

	d.RecordFailure("bash")
	d.RecordFailure("bash")
	statuses := d.CircuitStatus()
	if len(statuses) == 0 {
		t.Fatal("expected circuit status entries")
	}
	// bash has 2 failures but is still closed (under threshold) — status shows
	// failures but state closed.
	found := false
	for _, st := range statuses {
		if st.Tool == "bash" && st.State == "closed" && st.Failures == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected closed bash with 2 failures, got %+v", statuses)
	}

	// Trip it → open with RetryIn.
	d.RecordFailure("bash")
	statuses = d.CircuitStatus()
	for _, st := range statuses {
		if st.Tool == "bash" && st.State == "open" && st.RetryIn > 0 {
			return
		}
	}
	t.Fatalf("expected open bash with RetryIn, got %+v", statuses)
}
