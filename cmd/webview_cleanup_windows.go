//go:build windows && !nogui

package cmd

import (
	"context"
	"encoding/csv"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/executil"
)

// killStaleWebViewProcesses terminates orphaned msedgewebview2.exe processes
// whose command line references our WebView2 user-data folder. A previous
// crashed run can leave zombie WebView2 processes holding the data-dir lock;
// the next launch then hangs inside webview2.NewWithOptions waiting for the
// runtime ("桌面启动卡死，无窗口").
//
// Uses wmic (built-in Win32, no PowerShell dependency) to enumerate processes
// and their command lines. Best-effort: any failure is logged and ignored so
// boot always proceeds.
func killStaleWebViewProcesses(dataPath string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[desktop] webview cleanup panic: %v", r)
		}
	}()
	if strings.TrimSpace(dataPath) == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := executil.CommandContext(ctx,
		"wmic", "process", "where", "name='msedgewebview2.exe'",
		"get", "ProcessId,CommandLine", "/format:csv").CombinedOutput()
	if err != nil {
		log.Printf("[desktop] webview cleanup: wmic failed: %v", err)
		return
	}

	lower := strings.ToLower(dataPath)
	reader := csv.NewReader(strings.NewReader(string(out)))
	reader.FieldsPerRecord = -1 // variable columns
	records, _ := reader.ReadAll()

	for _, r := range records {
		if len(r) < 3 {
			continue
		}
		// wmic CSV columns: Node,ProcessId,CommandLine
		pidStr := strings.TrimSpace(r[1])
		cmdLine := strings.Trim(r[2], `"`)
		if pidStr == "" || pidStr == "ProcessId" {
			continue // header row
		}
		if !strings.Contains(strings.ToLower(cmdLine), lower) {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if perr := proc.Kill(); perr != nil {
			log.Printf("[desktop] webview cleanup: kill PID %d failed: %v", pid, perr)
		} else {
			log.Printf("[desktop] webview cleanup: killed zombie PID %d", pid)
		}
	}
}
