package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestDumpLogo(t *testing.T) {
	tui := New(Config{Model: "deepseek-v4-flash", Provider: "deepseek", Lang: "zh-CN", Theme: "dark"})
	tui.color = false
	lines := tui.logoLines(60)
	re := strings.NewReplacer("█", "#", "●", "o", "▀", "^", "▄", "v")
	re2 := strings.NewReplacer("\x1b", "")
	_ = re2
	fmt.Println("── logo (ascii) ──")
	for _, ln := range lines {
		fmt.Println("|" + re.Replace(ln) + "|")
	}
}
