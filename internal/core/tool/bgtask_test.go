package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBashRunInBackgroundAndTaskOutput(t *testing.T) {
	bash := &BashTool{}
	res, err := bash.Execute(context.Background(), `{"command":"echo bg-hello","run_in_background":true}`)
	if err != nil || !res.Success {
		t.Fatalf("bash bg failed: %v / %+v", err, res)
	}
	if !strings.Contains(res.Content, "bg-") {
		t.Fatalf("expected task id in response, got: %s", res.Content)
	}
	// Extract the id (format: "Started background task bg-N. ...").
	fields := strings.Fields(res.Content)
	var id string
	for _, f := range fields {
		if strings.HasPrefix(f, "bg-") {
			id = strings.TrimSuffix(f, ".")
			break
		}
	}
	if id == "" {
		t.Fatalf("no task id parsed from: %s", res.Content)
	}

	// Poll until finished (max ~3s).
	to := &TaskOutputTool{}
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, err := to.Execute(context.Background(), `{"task_id":"`+id+`"}`)
		if err != nil || !out.Success {
			t.Fatalf("task_output failed: %v / %+v", err, out)
		}
		if strings.Contains(out.Content, "status=finished") {
			if !strings.Contains(out.Content, "bg-hello") {
				t.Fatalf("expected command output captured, got: %s", out.Content)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task did not finish in time: %s", out.Content)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Listing works.
	list, err := to.Execute(context.Background(), `{}`)
	if err != nil || !strings.Contains(list.Content, id) {
		t.Fatalf("list should contain %s: %v / %s", id, err, list.Content)
	}
}
