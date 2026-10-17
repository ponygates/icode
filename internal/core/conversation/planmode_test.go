package conversation

import (
	"context"
	"testing"

	"github.com/ponygates/icode/internal/core/permission"
	"github.com/ponygates/icode/internal/types"
)

// enter_plan_mode must flip the gate to read-only and tell the UI, without
// going through the permission gate (which would be circular: the model would
// need approval to ask for fewer permissions).
func TestEnterPlanModeSwitchesGateAndNotifies(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	e.gate = permission.NewGate(permission.ModeAgent)
	out := make(chan types.StreamEvent, 1)

	res := e.executeTool(context.Background(), "s1",
		types.ToolCall{Name: "enter_plan_mode", Arguments: "{}"}, out)
	if res == nil || !res.Success {
		t.Fatalf("enter_plan_mode failed: %+v", res)
	}
	if e.gate.Mode() != permission.ModePlan {
		t.Fatalf("gate mode = %q, want plan", e.gate.Mode())
	}

	select {
	case ev := <-out:
		if ev.Type != types.EventModeChanged || ev.Mode != string(permission.ModePlan) {
			t.Fatalf("unexpected event: %+v", ev)
		}
	default:
		t.Fatal("no mode_changed event emitted — the UI badge would stay stale")
	}
}

// Calling it again is harmless and must not emit a duplicate event.
func TestEnterPlanModeIsIdempotent(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	e.gate = permission.NewGate(permission.ModePlan)
	out := make(chan types.StreamEvent, 1)

	res := e.executeTool(context.Background(), "s1",
		types.ToolCall{Name: "enter_plan_mode", Arguments: "{}"}, out)
	if res == nil || !res.Success {
		t.Fatalf("idempotent call failed: %+v", res)
	}
	select {
	case ev := <-out:
		t.Fatalf("duplicate mode_changed event: %+v", ev)
	default:
	}
}

// Without a gate the tool must fail loudly rather than pretending it worked.
func TestEnterPlanModeWithoutGateFails(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	out := make(chan types.StreamEvent, 1)
	res := e.executeTool(context.Background(), "s1",
		types.ToolCall{Name: "enter_plan_mode", Arguments: "{}"}, out)
	if res == nil || res.Success {
		t.Fatalf("expected failure without a gate, got %+v", res)
	}
}
