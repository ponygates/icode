package cmd

import (
	"os"
	"runtime"
	"testing"

	"debug/pe"
)

// TestDetectSubsystemSelf 验证 PE 子系统检测：go test 的测试二进制
// 必然是控制台子系统（CUI=3）。若此测试失败，说明工具链产物异常。
func TestDetectSubsystemSelf(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows：PE 子系统检测")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if got := detectSubsystem(exe); got != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		t.Errorf("测试二进制子系统 = %d, 期望 CUI(%d)", got, pe.IMAGE_SUBSYSTEM_WINDOWS_CUI)
	}
}

// TestDetectSubsystemNonPE 验证对非 PE 文件（含伪造 MZ 头的垃圾文件）
// 的容错：返回 0 而非报错，保证 warnIfGUISubsystem 永不炸。
func TestDetectSubsystemNonPE(t *testing.T) {
	dir := t.TempDir()
	path := dir + string(os.PathSeparator) + "not-a-pe.bin"
	if err := os.WriteFile(path, []byte("MZ definitely not a real PE file"), 0o600); err != nil {
		t.Fatalf("写入测试文件: %v", err)
	}
	if got := detectSubsystem(path); got != 0 {
		t.Errorf("非 PE 文件子系统 = %d, 期望 0", got)
	}
}
