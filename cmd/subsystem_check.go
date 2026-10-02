package cmd

import (
	"bufio"
	"fmt"
	"os"
	"runtime"

	"debug/pe"
)

// detectSubsystem 返回给定可执行文件的 PE 子系统值
// （2=IMAGE_SUBSYSTEM_WINDOWS_GUI，3=IMAGE_SUBSYSTEM_WINDOWS_CUI）。
// 非 PE 文件（Linux/macOS 的 ELF/Mach-O）或读取失败时返回 0。
func detectSubsystem(path string) uint16 {
	f, err := pe.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		return oh.Subsystem
	case *pe.OptionalHeader64:
		return oh.Subsystem
	}
	return 0
}

// warnIfGUISubsystem 检测 CLI 是否被误构建为 Windows GUI 子系统
// （-H windowsgui）。GUI 子系统会破坏 conhost/ConPTY 的 IME 桥接：
// 拼音字母漏进 stdin、上屏串被旁路直接写进屏幕缓冲区——即
// 「打字时上部内容区轮流出现文字 + 乱码」bug 的根因（见 CHANGELOG
// v0.1.2 与 ICODE.md 构建约定；该配置历史回退过两次，故设此防线）。
// 检测到时打印醒目警告并等用户确认后再进入 TUI，避免用户在不知情的
// 情况下继续使用坏构建并再次陷入难排查的乱码问题。
func warnIfGUISubsystem() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if detectSubsystem(exe) != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		return
	}
	fmt.Fprint(os.Stderr, guiSubsystemWarning)
	fmt.Fprintln(os.Stderr, "按 Enter 键仍要继续（不推荐）...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

const guiSubsystemWarning = `
⚠⚠⚠ 构建配置错误：icode.exe 是 GUI 子系统（-H windowsgui）构建 ⚠⚠⚠

  此构建下中文输入法桥接损坏：拼音字母会漏进输入框、上屏串被
  直接写屏，表现为「打字时上方内容区轮流出现文字 + 乱码」。

  修复：go build -ldflags="-s -w" -o icode.exe .   （或 build.bat）
  桌面版请用 icode desktop / icode-desktop.exe（只有它是 GUI 子系统）。

`
