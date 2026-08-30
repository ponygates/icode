package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/ponygates/icode/internal/msoffice"
	"github.com/spf13/cobra"
)

// msofficeCmd — zero-dependency Office document generation (docx/xlsx/pptx).
// The office skills call this FIRST (before falling back to pandoc /
// python tooling), so document generation works on any machine with iCode
// installed and nothing else.
var msofficeCmd = &cobra.Command{
	Use:   "msoffice",
	Short: "Generate Office documents (docx/xlsx/pptx) with zero dependencies",
}

var msofficeDocx = &cobra.Command{
	Use:   "docx -o <out.docx> <input.md>",
	Short: "Markdown-ish → Word (.docx). #/##/### headings, - bullets, paragraphs.",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		out, _ := c.Flags().GetString("out")
		if out == "" {
			return fmt.Errorf("--out is required")
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		if err := msoffice.WriteDocx(out, string(data)); err != nil {
			return err
		}
		fmt.Println("✓ wrote", out)
		return nil
	},
}

var msofficeXlsx = &cobra.Command{
	Use:   "xlsx -o <out.xlsx> <input.csv>",
	Short: "CSV/TSV → Excel (.xlsx). First line becomes the header row.",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		out, _ := c.Flags().GetString("out")
		if out == "" {
			return fmt.Errorf("--out is required")
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		sep := ","
		if strings.Contains(text, "\t") {
			sep = "\t"
		}
		var rows [][]string
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			rows = append(rows, strings.Split(line, sep))
		}
		if err := msoffice.WriteXlsx(out, rows); err != nil {
			return err
		}
		fmt.Println("✓ wrote", out)
		return nil
	},
}

var msofficePptx = &cobra.Command{
	Use:   "pptx -o <out.pptx> <outline.md>",
	Short: "Outline → PowerPoint (.pptx). '# title' starts a slide; '- ' bullets.",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		out, _ := c.Flags().GetString("out")
		if out == "" {
			return fmt.Errorf("--out is required")
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		if err := msoffice.WritePptx(out, string(data)); err != nil {
			return err
		}
		fmt.Println("✓ wrote", out)
		return nil
	},
}

func init() {
	for _, sub := range []*cobra.Command{msofficeDocx, msofficeXlsx, msofficePptx} {
		sub.Flags().StringP("out", "o", "", "output file path")
		_ = sub.MarkFlagRequired("out")
		msofficeCmd.AddCommand(sub)
	}
	rootCmd.AddCommand(msofficeCmd)
}
