package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/provider"
	"github.com/ponygates/icode/internal/tui"
)

func main() {
	var (
		privacyMode string
		modelKey    string
		permMode    string
		version     bool
	)

	flag.StringVar(&privacyMode, "privacy", "", "Privacy mode: local, china-trusted, smart, global-audited, full, custom")
	flag.StringVar(&modelKey, "model", "", "Model to use (e.g. deepseek/deepseek-v4-flash)")
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
	if modelKey != "" {
		cfg.Provider.Default = modelKey
	}

	reg := provider.InitRegistry(cfg)

	pk := provider.ParseModelKey(cfg.Provider.Default)
	prov := reg.Get(pk.Name)
	if prov == nil {
		fmt.Fprintf(os.Stderr, "Provider '%s' not configured. Set %s_API_KEY or run /model\n",
			pk.Name, pk.Name)
		os.Exit(1)
	}

	app := tui.New(cfg, reg)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
