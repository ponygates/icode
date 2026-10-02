package app

import "testing"

// TestBootStageDeltas verifies the doctor startup profile: cumulative
// checkpoints become per-stage deltas, sorted slowest-first.
func TestBootStageDeltas(t *testing.T) {
	a := &App{BootTimings: []BootTiming{
		{Stage: "config", Millis: 2},
		{Stage: "storage", Millis: 40},   // +38ms
		{Stage: "providers", Millis: 45}, // +5ms
		{Stage: "engine", Millis: 50},    // +5ms
	}}
	got := a.BootStageDeltas()
	if len(got) != 4 {
		t.Fatalf("expected 4 deltas, got %d", len(got))
	}
	// Slowest (storage, +38ms) must lead.
	if got[0].Stage != "storage" || got[0].Millis != 38 {
		t.Fatalf("expected storage/38ms first, got %+v", got[0])
	}
	if a.BootTotalMillis() != 50 {
		t.Fatalf("BootTotalMillis = %d, want 50 (last cumulative)", a.BootTotalMillis())
	}
}

// TestBootStageDeltasEmpty guards the zero-value path (pre-built App, tests).
func TestBootStageDeltasEmpty(t *testing.T) {
	a := &App{}
	if d := a.BootStageDeltas(); len(d) != 0 {
		t.Fatalf("expected empty deltas, got %+v", d)
	}
	if a.BootTotalMillis() != 0 {
		t.Fatalf("BootTotalMillis on empty timings = %d, want 0", a.BootTotalMillis())
	}
}
