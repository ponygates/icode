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

// The concurrency cap must count only live tasks: finished ones stay readable
// via task_output but must not block new work.
func TestBgTaskManagerCapacityCountsOnlyRunning(t *testing.T) {
	m := &bgTaskManager{tasks: map[string]*bgTask{}, maxRunning: 1}
	done := &bgTask{id: "bg-1", start: time.Now().Add(-time.Minute), done: true}
	m.tasks["bg-1"] = done
	if err := m.acquireSlot(); err != nil {
		t.Fatalf("a finished task must not consume a slot: %v", err)
	}

	live := &bgTask{id: "bg-2", start: time.Now()}
	m.tasks["bg-2"] = live
	err := m.acquireSlot()
	if err == nil {
		t.Fatal("second live task must be rejected at maxRunning=1")
	}
	if !strings.Contains(err.Error(), "kill=true") {
		t.Errorf("error must tell the user how to recover, got %q", err)
	}
}

// Finished tasks are retained for task_output but bounded, so buffers cannot
// accumulate for the whole process lifetime.
func TestBgTaskManagerPrunesOldestFinished(t *testing.T) {
	m := &bgTaskManager{tasks: map[string]*bgTask{}, keepFinished: 2}
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		id := "bg-" + string(rune('a'+i))
		m.tasks[id] = &bgTask{id: id, start: base.Add(time.Duration(i) * time.Minute), done: true}
	}
	m.tasks["live"] = &bgTask{id: "live", start: base.Add(10 * time.Minute)}

	m.pruneLocked()

	if len(m.tasks) != 3 {
		t.Fatalf("want 2 finished + 1 live, got %d: %v", len(m.tasks), m.tasks)
	}
	if _, ok := m.tasks["live"]; !ok {
		t.Fatal("running task must never be pruned")
	}
	// The two newest finished tasks (d, e) survive.
	if _, ok := m.tasks["bg-d"]; !ok {
		t.Error("bg-d should be retained")
	}
	if _, ok := m.tasks["bg-e"]; !ok {
		t.Error("bg-e should be retained")
	}
	if _, ok := m.tasks["bg-a"]; ok {
		t.Error("bg-a (oldest) should have been pruned")
	}
}
