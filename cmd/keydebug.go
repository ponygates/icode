package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// keydebugCmd — hidden diagnostic: puts the terminal in raw mode and prints
// every incoming byte in hex so a misbehaving terminal's escape sequences can
// be captured exactly (arrow keys, mouse reports, IME output). Esc quits.
var keydebugCmd = &cobra.Command{
	Use:    "keydebug",
	Hidden: true,
	Short:  "Print raw bytes of every keypress (hex) — diagnostic for input bugs",
	RunE: func(c *cobra.Command, args []string) error {
		old, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(os.Stdin.Fd()), old)
		fmt.Println("按键诊断：按任意键查看原始字节（Esc 退出）")
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return err
			}
			var parts []string
			quit := false
			for _, b := range buf[:n] {
				parts = append(parts, fmt.Sprintf("%02X", b))
				if b == 0x1b {
					quit = true
				}
			}
			fmt.Printf("[%d bytes] %s\n", n, strings.Join(parts, " "))
			if quit {
				fmt.Println("已退出")
				return nil
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(keydebugCmd)
}
