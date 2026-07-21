package cmd

import (
	"fmt"

	"github.com/ponygates/icode/internal/app"
	"github.com/spf13/cobra"
)

// cleanupCmd runs disk cleanup without requiring an AI model.
var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Clean disk space (C盘清理) — no AI model required",
	Long: `Scan and clean temporary files, recycle bin, browser cache, 
and Windows Update leftovers. 

Run without arguments for a dry-run scan.
Use --execute to actually delete files.

Examples:
  icode cleanup              # Scan only (dry run)
  icode cleanup --execute    # Actually clean`,
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := app.Bootstrap()
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		defer a.Close()

		execute, _ := cmd.Flags().GetBool("execute")

		// Show disk usage first
		fmt.Println("\n📊 磁盘使用情况")
		fmt.Println("═══════════════")
		duRes := a.Engine.ExecuteTool("disk_usage", "{}")
		if duRes != nil {
			fmt.Println(duRes.Content)
		}

		// Run cleanup
		target := "all"
		argsJSON := fmt.Sprintf(`{"target":"%s","dry_run":%v}`, target, !execute)
		if execute {
			fmt.Println("\n🧹 开始清理...")
		} else {
			fmt.Println("\n🔍 扫描可清理文件（仅预览，未实际删除）...")
		}
		dcRes := a.Engine.ExecuteTool("disk_cleanup", argsJSON)
		fmt.Println(dcRes.Content)

		if !execute {
			fmt.Println("\n💡 使用 --execute 参数实际执行清理: icode cleanup --execute")
		}
		return nil
	},
}

func init() {
	cleanupCmd.Flags().BoolP("execute", "x", false, "Actually delete files (default: dry-run only)")
	rootCmd.AddCommand(cleanupCmd)
}
