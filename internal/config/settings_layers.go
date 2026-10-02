package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// Settings layers.
//
// A single config file used to be the whole story: whoever wrote last won, and
// the project file was read BEFORE the home file, so a personal setting
// silently overrode the repository's — the opposite of what a team expects,
// and there was no way for an organisation to pin anything at all.
//
// Load order is now explicit and lowest-first:
//
//	defaults < user (~/…) < project (./.icode…) < env < cli < managed
//
// The managed layer lives outside the user's writable tree, so it cannot be
// edited away by the very person a policy is meant to constrain.

// Layer names, in increasing precedence.
const (
	LayerUser    = "user"
	LayerProject = "project"
	LayerEnv     = "env"
	LayerCLI     = "cli"
	LayerManaged = "managed"
)

var layerFiles = map[string][]string{
	LayerUser: {
		".icoderc.yaml",
		filepath.Join(".icode", "config.yaml"),
		filepath.Join(".config", "icode", "config.yaml"),
	},
	LayerProject: {
		".icoderc.yaml",
		".icoderc.yml",
		filepath.Join(".icode", "config.yaml"),
		filepath.Join(".icode", "config.yml"),
		"icode.yaml",
		"icode.yml",
		".icoderc.toml",
		"icode.toml",
	},
}

// ManagedPaths returns the OS-specific locations an administrator may drop a
// policy file. ICODE_MANAGED_CONFIG overrides them for tests and imaging.
func ManagedPaths() []string {
	if v := strings.TrimSpace(os.Getenv("ICODE_MANAGED_CONFIG")); v != "" {
		return []string{v}
	}
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("PROGRAMDATA")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return []string{filepath.Join(pd, "icode", "config.yaml")}
	case "darwin":
		return []string{
			"/Library/Application Support/icode/config.yaml",
			filepath.Join("/Library/Application Support/icode", "managed.yaml"),
		}
	default:
		return []string{"/etc/icode/config.yaml", "/etc/icode/managed.yaml"}
	}
}

// cliLayer holds overrides handed in by the command line. They are applied on
// every Load, which is why they cannot simply be passed as a function argument:
// config is re-read from many call sites deep in the CLI.
var (
	cliLayerMu sync.RWMutex
	cliLayer   map[string]any
)

// SetCLILayer records the command-line settings layer. Keys are dotted paths
// ("defaults.model", "language"). Called once by the root command after flags
// are parsed; later calls replace the previous layer.
func SetCLILayer(overrides map[string]any) {
	cliLayerMu.Lock()
	cliLayer = overrides
	cliLayerMu.Unlock()
}

// CLILayerSnapshot returns a copy of the current command-line layer.
func CLILayerSnapshot() map[string]any {
	cliLayerMu.RLock()
	defer cliLayerMu.RUnlock()
	out := make(map[string]any, len(cliLayer))
	for k, v := range cliLayer {
		out[k] = v
	}
	return out
}

// applyLayer merges one raw document into cfg and records which layer last set
// each leaf key. Values are normalised through a generic map so YAML and TOML
// sources behave identically.
func (c *Config) applyLayer(layer string, data []byte, asTOML bool) error {
	var raw map[string]any
	if asTOML {
		if err := unmarshalTOMLMap(data, &raw); err != nil {
			return err
		}
	} else if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		return nil
	}

	// Re-encode as YAML so a TOML layer lands on the same struct paths as a
	// YAML one; the tags are shared.
	y, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(y, c); err != nil {
		return fmt.Errorf("%s layer is not a valid iCode config: %w", layer, err)
	}

	if c.layers == nil {
		c.layers = map[string]string{}
	}
	if c.rawLayers == nil {
		c.rawLayers = map[string]map[string]any{}
	}
	for _, dotted := range flattenKeys("", raw) {
		c.layers[dotted] = layer
	}
	if layer == LayerManaged {
		// Keep it so a runtime mutation of a pinned key can be reverted.
		c.rawLayers[LayerManaged] = raw
	}
	return nil
}

// flattenKeys lists dotted paths of every leaf in a decoded document.
func flattenKeys(prefix string, m map[string]any) []string {
	var out []string
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok && len(sub) > 0 {
			out = append(out, flattenKeys(path, sub)...)
			continue
		}
		out = append(out, path)
	}
	return out
}

// mergeLayerFile reads one file into the given layer. A missing file is not an
// error — only layers that exist participate.
func (c *Config) mergeLayerFile(layer, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			return nil
		}
		return fmt.Errorf("load %s: %w", path, err)
	}
	if err := c.applyLayer(layer, data, layerFileIsTOML(path)); err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	return nil
}

// recordEnvLayer notes which keys the environment set, so SettingLayer can
// answer "why is this my value?" for env-driven changes too.
func recordEnvLayer(c *Config) {
	if c.layers == nil {
		c.layers = map[string]string{}
	}
	touched := []string{}
	if os.Getenv("ICODE_LANG") != "" {
		touched = append(touched, "language")
	}
	for _, p := range []string{"DEEPSEEK", "OPENROUTER", "ZHIPU", "KIMI"} {
		if os.Getenv(p+"_API_KEY") != "" {
			touched = append(touched, strings.ToLower("providers."+p+".api_key"))
		}
	}
	for _, k := range touched {
		c.layers[k] = LayerEnv
	}
}

// CLIOverlayFromFlags turns parsed command-line flags into the cli layer's
// dotted keys. Empty values mean "not supplied" and claim no layer, so a flag
// left at its default cannot override the repository's settings.
//
// Recognised flags: model, provider, mode, lang.
func CLIOverlayFromFlags(values map[string]string) map[string]any {
	mapping := map[string]string{
		"model":    "defaults.model",
		"provider": "defaults.provider",
		"mode":     "defaults.mode",
		"lang":     "language",
	}
	out := map[string]any{}
	for flag, dotted := range mapping {
		if v := strings.TrimSpace(values[flag]); v != "" {
			out[dotted] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyCLILayer applies the dotted cli overrides (e.g. "defaults.model").
func (c *Config) applyCLILayer() error {
	cliLayerMu.RLock()
	overrides := make(map[string]any, len(cliLayer))
	for k, v := range cliLayer {
		overrides[k] = v
	}
	cliLayerMu.RUnlock()
	if len(overrides) == 0 {
		return nil
	}
	nested := map[string]any{}
	for dotted, v := range overrides {
		setDotted(nested, dotted, v)
	}
	return c.applyLayer(LayerCLI, mustYAML(nested), false)
}

// applyManagedLayer re-applies the managed document, restoring any key a
// lower-priority source changed after Load.
func (c *Config) applyManagedLayer() error {
	if c.rawLayers == nil {
		return nil
	}
	raw := c.rawLayers[LayerManaged]
	if len(raw) == 0 {
		return nil
	}
	return c.applyLayer(LayerManaged, mustYAML(raw), false)
}

func mustYAML(v any) []byte {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// unmarshalTOMLMap decodes a TOML document into a generic map so it can be
// re-encoded as YAML and merged through the same path as a YAML layer.
func unmarshalTOMLMap(data []byte, out *map[string]any) error {
	_, err := toml.Decode(string(data), out)
	return err
}

func setDotted(m map[string]any, dotted string, v any) {
	parts := strings.Split(dotted, ".")
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = v
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// SettingLayer reports which layer last set a dotted key ("defaults.model"),
// or "" when no loaded file mentioned it. /status and `icode config why` use it
// to answer "why is this my value?" without guessing.
func (c *Config) SettingLayer(dotted string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.layers == nil {
		return ""
	}
	return c.layers[dotted]
}

// LayerReport lists every key that a non-default layer set, with its layer,
// sorted by key.
func (c *Config) LayerReport() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.layers))
	for k, v := range c.layers {
		out[k] = v
	}
	return out
}

// ManagedKeys returns the dotted keys pinned by the managed policy file.
func (c *Config) ManagedKeys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rawLayers == nil {
		return nil
	}
	keys := flattenKeys("", c.rawLayers[LayerManaged])
	sort.Strings(keys)
	return keys
}

// IsManaged reports whether a dotted key is pinned by policy. Callers that
// would otherwise write a setting must check this first — otherwise a UI
// change would look saved and silently revert on the next Load.
func (c *Config) IsManaged(dotted string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rawLayers == nil {
		return false
	}
	return c.layers[dotted] == LayerManaged
}

// layerFileIsTOML is the extension test shared by the loaders.
func layerFileIsTOML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".toml"
}
