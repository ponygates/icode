package config

import (
	"os"
	"path/filepath"
	"testing"
)

// isolate points HOME/USERPROFILE and the working directory at empty temp
// dirs, so a developer's own ~/.icode/config.yaml or a checkout-level
// icode.yaml cannot join the layering under test.
func isolate(t *testing.T) (home, project string) {
	t.Helper()
	home = t.TempDir()
	project = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ICODE_MANAGED_CONFIG", filepath.Join(home, "nonexistent-managed.yaml"))
	t.Chdir(project)
	for _, k := range []string{"ICODE_LANG", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY", "ZHIPU_API_KEY", "KIMI_API_KEY"} {
		t.Setenv(k, "")
	}
	return home, project
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoad_ProjectBeatsUser(t *testing.T) {
	home, project := isolate(t)
	writeFile(t, filepath.Join(home, ".icode", "config.yaml"), "defaults:\n  model: user-model\n")
	writeFile(t, filepath.Join(project, ".icode", "config.yaml"), "defaults:\n  model: project-model\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Defaults.Model != "project-model" {
		t.Fatalf("project layer must outrank user layer, got %q", cfg.Defaults.Model)
	}
	if got := cfg.SettingLayer("defaults.model"); got != LayerProject {
		t.Fatalf("SettingLayer = %q, want %q", got, LayerProject)
	}
}

func TestLoad_CLIBeatsProject(t *testing.T) {
	_, project := isolate(t)
	writeFile(t, filepath.Join(project, "icode.yaml"), "defaults:\n  model: project-model\n")
	SetCLILayer(map[string]any{"defaults.model": "cli-model"})
	t.Cleanup(func() { SetCLILayer(nil) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Defaults.Model != "cli-model" {
		t.Fatalf("cli layer must outrank project, got %q", cfg.Defaults.Model)
	}
	if got := cfg.SettingLayer("defaults.model"); got != LayerCLI {
		t.Fatalf("SettingLayer = %q, want %q", got, LayerCLI)
	}
}

func TestLoad_ManagedBeatsEverything(t *testing.T) {
	home, project := isolate(t)
	managed := filepath.Join(home, "managed.yaml")
	writeFile(t, filepath.Join(home, ".icode", "config.yaml"), "defaults:\n  model: user-model\n  mode: yolo\n")
	writeFile(t, filepath.Join(project, "icode.yaml"), "defaults:\n  model: project-model\n")
	writeFile(t, managed, "defaults:\n  model: pinned-model\n  mode: plan\n")
	t.Setenv("ICODE_MANAGED_CONFIG", managed)
	SetCLILayer(map[string]any{"defaults.model": "cli-model", "defaults.mode": "yolo"})
	t.Cleanup(func() { SetCLILayer(nil) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Defaults.Model != "pinned-model" {
		t.Fatalf("managed layer must win, got model %q", cfg.Defaults.Model)
	}
	if cfg.Defaults.Mode != "plan" {
		t.Fatalf("managed layer must win, got mode %q", cfg.Defaults.Mode)
	}
	if !cfg.IsManaged("defaults.model") {
		t.Fatal("defaults.model should be reported as managed")
	}
	keys := cfg.ManagedKeys()
	if len(keys) != 2 {
		t.Fatalf("ManagedKeys = %v, want 2 entries", keys)
	}
}

// A re-read after a runtime mutation must not drop the policy: the managed
// document is reapplied on top of whatever changed.
func TestConfig_ManagedSurvivesReload(t *testing.T) {
	home, _ := isolate(t)
	managed := filepath.Join(home, "managed.yaml")
	writeFile(t, managed, "defaults:\n  mode: plan\n")
	t.Setenv("ICODE_MANAGED_CONFIG", managed)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Defaults.Mode = "yolo"
	if err := cfg.applyManagedLayer(); err != nil {
		t.Fatalf("reapply managed: %v", err)
	}
	if cfg.Defaults.Mode != "plan" {
		t.Fatalf("managed policy did not restore mode: %q", cfg.Defaults.Mode)
	}
}

func TestLoad_MalformedManagedIsError(t *testing.T) {
	home, _ := isolate(t)
	managed := filepath.Join(home, "managed.yaml")
	writeFile(t, managed, "defaults: [this is not a mapping\n")
	t.Setenv("ICODE_MANAGED_CONFIG", managed)

	if _, err := Load(); err == nil {
		t.Fatal("a malformed policy file must not load as if it were absent")
	}
}

func TestLoad_TOMLLayerStillMerges(t *testing.T) {
	_, project := isolate(t)
	writeFile(t, filepath.Join(project, "icode.toml"), "[defaults]\nmodel = \"toml-model\"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Defaults.Model != "toml-model" {
		t.Fatalf("TOML project layer ignored, got %q", cfg.Defaults.Model)
	}
}

func TestLoad_EnvLayerRecorded(t *testing.T) {
	isolate(t)
	t.Setenv("ICODE_LANG", "en")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Language != "en" {
		t.Fatalf("env override lost: %q", cfg.Language)
	}
	if got := cfg.SettingLayer("language"); got != LayerEnv {
		t.Fatalf("SettingLayer(language) = %q, want %q", got, LayerEnv)
	}
}

func TestSetCLILayerIgnoresEmptyValues(t *testing.T) {
	isolate(t)
	// An unset flag must not become a layer entry.
	SetCLILayer(CLIOverlayFromFlags(map[string]string{"model": "", "mode": "plan"}))
	t.Cleanup(func() { SetCLILayer(nil) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.SettingLayer("defaults.model"); got != "" {
		t.Fatalf("empty flag should not claim a layer, got %q", got)
	}
	if got := cfg.SettingLayer("defaults.mode"); got != LayerCLI {
		t.Fatalf("set flag should claim the cli layer, got %q", got)
	}
	if cfg.Defaults.Mode != "plan" {
		t.Fatalf("mode overlay not applied: %q", cfg.Defaults.Mode)
	}
}
