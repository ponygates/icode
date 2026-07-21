package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ponygates/icode/internal/executil"
	"github.com/ponygates/icode/internal/types"
)

// ── DiskUsageTool ──────────────────────────────────────────────────────────

type DiskUsageTool struct{}

func (t *DiskUsageTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "disk_usage",
		Description: "Show disk space usage for all drives. Shows total, used, free space. Use this before cleaning to understand what needs attention.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *DiskUsageTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var output strings.Builder

	if runtime.GOOS == "windows" {
		// Use PowerShell for reliable disk info
		cmd := executil.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
			`Get-PSDrive -PSProvider FileSystem | Where-Object { $_.Used -ne $null -and ($_.Used + $_.Free) -gt 0 } | `+
				`ForEach-Object { $pct = [math]::Round(($_.Used / ($_.Used + $_.Free)) * 100); `+
				`"{0} | Used={1:N1}GB Free={2:N1}GB Total={3:N1}GB | {4}%" -f `+
				`$_.Root, ($_.Used/1GB), ($_.Free/1GB), (($_.Used+$_.Free)/1GB), $pct }`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("disk query failed: %v: %s", err, out)}, nil
		}
		output.WriteString(string(out))
	} else {
		cmd := executil.CommandContext(ctx, "df", "-h")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("df failed: %v", err)}, nil
		}
		output.WriteString(string(out))
	}

	if output.Len() == 0 {
		return &types.ToolResult{Success: false, Error: "no drives found"}, nil
	}
	return &types.ToolResult{Success: true, Content: output.String()}, nil
}

// ── DiskCleanupTool ────────────────────────────────────────────────────────

type DiskCleanupTool struct{}

func (t *DiskCleanupTool) Def() types.ToolDef {
	return types.ToolDef{
		Name: "disk_cleanup",
		Description: "Clean up disk space by removing temporary files, recycle bin, browser caches, and Windows update leftovers. Safe — only removes truly disposable files. Use disk_usage first to see what needs cleaning.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{
					"type":        "string",
					"description": "What to clean: 'temp' (Temp folders), 'recycle' (Recycle Bin), 'browser' (browser caches), 'windows_update' (Update cache), 'all' (everything safe). Default: 'all'.",
				},
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "If true, only report what would be deleted without actually deleting. Default: false.",
				},
			},
		},
	}
}

func (t *DiskCleanupTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	target := parseStrArg(args, "target", "all")
	dryRun := parseBoolArgWithDefault(args, "dry_run", false)

	if runtime.GOOS != "windows" {
		return &types.ToolResult{Success: false, Error: "disk_cleanup is currently Windows-only. Use bash on Linux/macOS."}, nil
	}

	var output strings.Builder
	var totalCleaned int64

	cleanPath := func(label, base string) int64 {
		if !dirExists(base) {
			return 0
		}
		var count int64
		var fileCount int
		_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			size := info.Size()
			if !dryRun {
				if err := os.Remove(path); err == nil {
					count += size
					fileCount++
				}
			} else {
				count += size
				fileCount++
			}
			return nil
		})
		action := "Cleaned"
		if dryRun {
			action = "Would clean"
		}
		if count > 0 {
			output.WriteString(fmt.Sprintf("  %s: %s %d files (%.1f MB)\n",
				label, action, fileCount, float64(count)/1024/1024))
		}
		return count
	}

	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		userProfile = `C:\Users\` + os.Getenv("USERNAME")
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = filepath.Join(userProfile, "AppData", "Local")
	}

	output.WriteString("=== Disk Cleanup ===\n")
	if dryRun {
		output.WriteString("(DRY RUN — no files deleted)\n\n")
	} else {
		output.WriteString("\n")
	}

	if target == "temp" || target == "all" {
		totalCleaned += cleanPath("Windows Temp", filepath.Join(systemRoot, "Temp"))
		totalCleaned += cleanPath("User Temp", filepath.Join(localAppData, "Temp"))
		totalCleaned += cleanPath("Prefetch", filepath.Join(systemRoot, "Prefetch"))
	}

	if target == "recycle" || target == "all" {
		totalCleaned += cleanPath("Recycle Bin", `C:\$Recycle.Bin`)
	}

	if target == "browser" || target == "all" {
		totalCleaned += cleanPath("Chrome Cache", filepath.Join(localAppData, "Google", "Chrome", "User Data", "Default", "Cache", "Cache_Data"))
		totalCleaned += cleanPath("Edge Cache", filepath.Join(localAppData, "Microsoft", "Edge", "User Data", "Default", "Cache", "Cache_Data"))
	}

	if target == "windows_update" || target == "all" {
		totalCleaned += cleanPath("Win Update Downloads", filepath.Join(systemRoot, "SoftwareDistribution", "Download"))
		if !dryRun {
			cmd := executil.CommandContext(ctx, "dism", "/online", "/cleanup-image", "/startcomponentcleanup", "/resetbase", "/quiet")
			cmd.Run() // best-effort, ignore errors
			output.WriteString("  DISM component cleanup: completed (best-effort)\n")
		}
	}

	output.WriteString(fmt.Sprintf("\n=== Total: %.1f MB %s ===\n",
		float64(totalCleaned)/1024/1024,
		map[bool]string{true: "could be freed", false: "freed"}[dryRun]))
	return &types.ToolResult{Success: true, Content: output.String()}, nil
}

// ── helpers ──

func parseStrArg(args, key, defaultVal string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return defaultVal
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return defaultVal
}

func parseBoolArgWithDefault(args, key string, defaultVal bool) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return defaultVal
	}
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return defaultVal
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
