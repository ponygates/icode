//go:build !windows

package tool

import "fmt"

// Computer-use tools rely on the Windows GDI + SendInput APIs. On other
// platforms they report a friendly unsupported error so the tool surface stays
// uniform across builds.

func captureScreen(x0, y0, w, h int) ([]byte, int, int, error) {
	return nil, 0, 0, fmt.Errorf("computer use 当前仅支持 Windows")
}

func moveMouse(x, y int) error {
	return fmt.Errorf("computer use 当前仅支持 Windows")
}

func clickMouse(button string, double bool, x, y int) error {
	return fmt.Errorf("computer use 当前仅支持 Windows")
}

func scrollMouse(dx, dy int) error {
	return fmt.Errorf("computer use 当前仅支持 Windows")
}

func typeText(text string) error {
	return fmt.Errorf("computer use 当前仅支持 Windows")
}

func pressKey(key string) error {
	return fmt.Errorf("computer use 当前仅支持 Windows")
}