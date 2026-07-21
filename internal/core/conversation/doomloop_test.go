package conversation

import (
	"testing"
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
