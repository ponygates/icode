package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/tui"
)

func main() {
	var (
		privacyMode string
		model       string
		provider    string
		permMode    string
		version     bool
	)

	flag.StringVar(&privacyMode, "privacy", "", "Privacy mode: local, china-trusted, smart, global-audited, full, custom")
	flag.StringVar(&model, "model", "", "Model to use (e.g. deepseek-v4-flash, claude-sonnet-4.6)")
	flag.StringVar(&provider, "provider", "", "Provider to use (e.g. zhipu, openai, deepseek)")
	flag.StringVar(&permMode, "perm", "", "Permission mode: plan, ask, auto, yolo")
	flag.BoolVar(&version, "version", false, "Print version")
	flag.Parse()

	if version {
		fmt.Println("iCode v0.1.0")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if privacyMode != "" {
		cfg.Privacy.Mode = privacyMode
	}
	if permMode != "" {
		cfg.Permission.Mode = permMode
	}

	app := tui.New(cfg)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
