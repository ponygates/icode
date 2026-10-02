//go:build windows

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/ponygates/icode/internal/core/privacy"
)

var procReadConsoleOutputCharacterW = kernel32.NewProc("ReadConsoleOutputCharacterW")

// dumpScreen snapshots the ACTIVE console screen buffer (the 1049 alternate
// buffer while the TUI is live) row by row into ~/.icode/screen-dump.txt,
// together with the physical cursor position and the current console input
// mode (an ECHO revival is caught red-handed in the dump). Bound to F12: when
// the user reports "text appears in the wrong place", one keypress captures
// exactly what is on screen so the layout can be diffed against the intended
// frame. Returns the rows it read so the caller can diff them against the
// renderer's last frame (external-write detection).
func dumpScreen() []string {
	handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || handle == syscall.InvalidHandle {
		return nil
	}
	var csbi _CONSOLE_SCREEN_BUFFER_INFO
	ret, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&csbi)))
	if ret == 0 {
		return nil
	}
	width := int(csbi.Window.Right - csbi.Window.Left + 1)
	if width <= 0 {
		return nil
	}

	// Console input mode at dump time: the ECHO bit (0x4) tells whether a
	// conhost echo revival could have written the stray text — the prime
	// suspect for "typed text appears in the conversation area".
	echoNote := ""
	var inMode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &inMode); err == nil {
		echoNote = fmt.Sprintf("  stdin-mode=%#04x", inMode)
		if inMode&0x4 != 0 {
			echoNote += "  *** ECHO IS ON — conhost echo revival active! ***"
		}
	}
	// Terminal host matters: under Windows Terminal the IME draws its
	// composition window as a floating TSF layer (never touches the text
	// buffer), while a bare conhost window paints composition strings
	// straight into the screen buffer — the exact profile of our stray
	// rows. WT_SESSION (set by WT) and TERM_PROGRAM discriminate the two.
	echoNote += fmt.Sprintf("  host-env[WT_SESSION=%q TERM_PROGRAM=%q TERM=%q]",
		os.Getenv("WT_SESSION"), os.Getenv("TERM_PROGRAM"), os.Getenv("TERM"))

	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(home, ".icode", "screen-dump.txt")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Screen rows can show pasted keys or provider errors verbatim — mask
	// before the snapshot lands in the file users attach to bug reports.
	fw := privacy.NewRedactingWriter(f)

	fmt.Fprintf(fw, "screen-dump %s  buffer=%dx%d viewport rows %d..%d  cursor=(%d,%d)%s\n",
		time.Now().Format("15:04:05.000"),
		csbi.Size.X, csbi.Size.Y,
		csbi.Window.Top, csbi.Window.Bottom,
		csbi.CursorPosition.X, csbi.CursorPosition.Y,
		echoNote)

	var rows []string
	buf := make([]uint16, width)
	for row := int(csbi.Window.Top); row <= int(csbi.Window.Bottom); row++ {
		var read uint32
		coord := uint32(uint16(0)) | (uint32(uint16(row)) << 16) // COORD{0,row}
		r2, _, _ := procReadConsoleOutputCharacterW.Call(
			uintptr(handle),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(width),
			uintptr(coord),
			uintptr(unsafe.Pointer(&read)),
		)
		if r2 == 0 || read == 0 {
			fmt.Fprintf(fw, "%2d| <read failed>\n", row+1)
			rows = append(rows, "")
			continue
		}
		// Decode UTF-16 code units properly (utf16.Decode merges surrogate
		// pairs). Casting units to runes one by one turns every emoji and
		// non-BMP glyph into lone surrogates that string() renders as U+FFFD
		// — forensic noise that would swamp exactly the kind of corruption
		// the dump exists to capture.
		runes := utf16.Decode(buf[:read])
		line := string(runes)
		rows = append(rows, line)
		fmt.Fprintf(fw, "%2d|%s|\n", row+1, line)
	}
	return rows
}
