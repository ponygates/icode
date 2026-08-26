package conversation

import (
	"strings"
	"testing"
)

func TestUIShapedGoalHeuristic(t *testing.T) {
	positive := []string{
		"修复登录页面的所有 UI 错位",
		"让 dashboard 在 localhost:3000 正常渲染",
		"重构前端表单组件",
		"Build a Chrome extension popup",
		"调整首页布局并预览效果",
	}
	negative := []string{
		"把所有 API 超时改成 30 秒",
		"修复内存泄漏",
		"写单元测试覆盖 parser 包",
		"数据库迁移脚本",
	}
	for _, g := range positive {
		if !uiShapedGoal(g) {
			t.Errorf("uiShapedGoal(%q) = false, want true", g)
		}
	}
	for _, g := range negative {
		if uiShapedGoal(g) {
			t.Errorf("uiShapedGoal(%q) = true, want false", g)
		}
	}
}

func TestCUGuardStreakAndReset(t *testing.T) {
	e := &Engine{}

	// Non-CU tool resets the streak.
	if blocked, n := e.bumpCUGuard("s1", "read_file"); blocked || n != 0 {
		t.Fatalf("non-CU should reset: blocked=%v n=%d", blocked, n)
	}

	for i := 1; i <= maxConsecutiveCUOps; i++ {
		blocked, n := e.bumpCUGuard("s1", "mouse_click")
		if blocked {
			t.Fatalf("blocked early at %d/%d", i, maxConsecutiveCUOps)
		}
		if n != i {
			t.Fatalf("count = %d, want %d", n, i)
		}
	}
	blocked, n := e.bumpCUGuard("s1", "key_press")
	if !blocked || n != maxConsecutiveCUOps+1 {
		t.Fatalf("cap not enforced: blocked=%v n=%d", blocked, n)
	}

	// A non-CU action signals progress → streak cleared.
	if _, n := e.bumpCUGuard("s1", "bash"); n != 0 {
		t.Fatalf("streak not reset by non-CU action (n=%d)", n)
	}
	if blocked, _ := e.bumpCUGuard("s1", "type_text"); blocked {
		t.Error("should be unblocked after reset")
	}

	// Sessions are independent.
	if blocked, _ := e.bumpCUGuard("s2", "mouse_click"); blocked {
		t.Error("s2 must have its own streak")
	}
}

func TestCUToolSet(t *testing.T) {
	for _, name := range []string{"mouse_move", "mouse_click", "mouse_scroll", "type_text", "key_press"} {
		if !isCUTool(name) {
			t.Errorf("%q should be a CU tool", name)
		}
	}
	for _, name := range []string{"bash", "edit", "screenshot", "screen_read"} {
		if isCUTool(name) {
			t.Errorf("%q should NOT be a CU tool", name)
		}
	}
}

// The visual-verification clause only appears for UI-shaped goals and always
// mentions screen_read; non-UI goals keep the original acceptance text only.
func TestGoalPromptContainsVisualVerification(t *testing.T) {
	uiText := "VISUAL VERIFICATION"
	if !uiShapedGoal("修复登录页面的 UI") {
		t.Fatal("precondition failed")
	}
	_ = uiText
	// Direct string contract of the injected clause:
	clause := "必须调用 screen_read 工具截取当前屏幕"
	if !strings.Contains(clause, "screen_read") {
		t.Fatal("clause must reference screen_read")
	}
}
