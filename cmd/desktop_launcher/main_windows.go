// +build windows

package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func main() {
	// Ensure we're in the right directory (desktop subfolder)
	exePath, _ := os.Executable()
	rootDir := filepath.Dir(exePath)
	if err := os.Chdir(rootDir); err != nil {
		// Try parent directory
		parentDir := filepath.Dir(rootDir)
		os.Chdir(parentDir)
	}

	// Find icode.exe in the root directory
	icodeExe := filepath.Join(rootDir, "icode.exe")
	if _, err := os.Stat(icodeExe); os.IsNotExist(err) {
		// Try parent
		icodeExe = filepath.Join(filepath.Dir(rootDir), "icode.exe")
		if _, err := os.Stat(icodeExe); os.IsNotExist(err) {
			fmt.Printf("iCode backend not found. Ensure icode.exe is in the same directory.\n")
			fmt.Println("Press Enter to exit...")
			fmt.Scanln()
			return
		}
	}

	// Find a free port
	port := findFreePort()
	if port == 0 {
		fmt.Printf("No free port available.\n")
		fmt.Scanln()
		return
	}

	fmt.Printf("Starting iCode Desktop on http://127.0.0.1:%d\n", port)

	// Start the iCode server
	cmd := exec.Command(icodeExe, "server", "--port", fmt.Sprintf("%d", port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Printf("Failed to start server: %v\n", err)
		fmt.Println("Press Enter to exit...")
		fmt.Scanln()
		return
	}

	// Wait for server to be ready
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
	}

	// Open browser
	openBrowser(fmt.Sprintf("http://127.0.0.1:%d", port))

	fmt.Printf("iCode Desktop running at http://127.0.0.1:%d\n", port)
	fmt.Println("Close the server window to shut down.")
	fmt.Println()

	// Wait for server to finish
	cmd.Wait()
}

func findFreePort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}
