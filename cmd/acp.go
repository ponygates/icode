package cmd

import (
	"context"
	"fmt"

	"github.com/ponygates/icode/internal/acp"
	"github.com/ponygates/icode/internal/app"
	"github.com/spf13/cobra"
)

// acpCmd runs an Agent Client Protocol (ACP) server over stdio, so Zed /
// Neovim and other ACP-compatible editors can drive iCode as an agent
// (Reasonix `reasonix acp` parity). See docs/acp_design.md.
var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Run an ACP (Agent Client Protocol) server on stdio for editor integration",
	Long: `Start an ACP server speaking JSON-RPC 2.0 over stdin/stdout. Compatible
editors (Zed, Neovim, …) launch this as a subprocess to use iCode as their
coding agent.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := app.Bootstrap()
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		defer a.Close()
		return acp.Run(context.Background(), a.Engine, a.Gate, a.SessStore)
	},
}
