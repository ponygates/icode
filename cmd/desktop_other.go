//go:build !windows
// +build !windows

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var desktopCmd = &cobra.Command{
	Use:   "desktop",
	Short: "启动桌面版（仅 Windows 支持）",
	Long:  `桌面模式目前仅支持 Windows（使用 WebView2 原生控件）。其他平台请使用 'icode server' + 浏览器。`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("桌面模式目前仅支持 Windows。请改用 'icode server' 启动后端服务并通过浏览器访问。")
	},
}

// runDesktop is a stub for non-Windows platforms.
func runDesktop() error {
	return fmt.Errorf("桌面模式仅支持 Windows")
}
