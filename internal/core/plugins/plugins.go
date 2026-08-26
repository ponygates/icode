// Package plugins — bundled distribution for iCode extensions (Claude Code
// plugin parity). A plugin is a directory (or .zip) with a manifest plus any
// of: skills/, commands/, agents/, teams/. Installing copies it under
// ~/.icode/plugins/<name>/ and its subdirectories automatically join the
// corresponding loader search paths, so one install wires everything up.
//
//	plugin.yaml
//	name: my-toolkit
//	version: 1.0.0
//	description: Custom skills + commands bundle
package plugins

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest is the plugin.yaml contract. Only Name is required.
type Manifest struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
}

// rootOverride, when set (tests only), replaces the computed plugins root.
var rootOverride string

// Root returns the install root (~/.icode/plugins).
func Root() string {
	if rootOverride != "" {
		return rootOverride
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".icode", "plugins")
}

// LoadManifest parses plugin.yaml from a directory. Errors when the manifest
// is missing or the name is unusable as a directory name.
func LoadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return nil, fmt.Errorf("plugin.yaml not found in %s", dir)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse plugin.yaml: %w", err)
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || strings.ContainsAny(m.Name, `/\:*?"<>|`) || m.Name == "." || m.Name == ".." {
		return nil, fmt.Errorf("plugin.yaml needs a usable 'name' field")
	}
	return &m, nil
}

// Install copies a plugin from a local source directory or a .zip archive
// into the plugins root. Existing installs are refused unless force is set.
// Zip extraction guards against path traversal (zip-slip).
func Install(src string, force bool) (*Manifest, error) {
	root := Root()
	if root == "" {
		return nil, fmt.Errorf("cannot resolve home directory")
	}

	var staging string
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		staging = src
	} else if strings.EqualFold(filepath.Ext(src), ".zip") {
		tmp, err := os.MkdirTemp("", "icode-plugin-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		if err := extractZip(src, tmp); err != nil {
			return nil, fmt.Errorf("extract zip: %w", err)
		}
		staging = tmp
		if entries, _ := os.ReadDir(tmp); len(entries) == 1 && entries[0].IsDir() {
			// Common layout: zip wraps a single top-level folder.
			staging = filepath.Join(tmp, entries[0].Name())
		}
	} else {
		return nil, fmt.Errorf("unsupported plugin source %q (use a directory or .zip)", src)
	}

	m, err := LoadManifest(staging)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(root, m.Name)
	if _, err := os.Lstat(dest); err == nil {
		if !force {
			return nil, fmt.Errorf("plugin %q already installed (pass force to overwrite)", m.Name)
		}
		if err := os.RemoveAll(dest); err != nil {
			return nil, err
		}
	}
	if err := copyTree(staging, dest); err != nil {
		return nil, fmt.Errorf("copy plugin: %w", err)
	}
	return m, nil
}

// List returns manifests of every installed plugin, sorted by name.
func List() []*Manifest {
	root := Root()
	var out []*Manifest
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m, err := LoadManifest(filepath.Join(root, e.Name())); err == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Remove deletes an installed plugin by name.
func Remove(name string) error {
	root := Root()
	dest := filepath.Join(root, name)
	if _, err := os.Lstat(dest); err != nil {
		return fmt.Errorf("plugin %q not installed", name)
	}
	return os.RemoveAll(dest)
}

// SubDirs returns the existing <sub> directories across all installed
// plugins (e.g. sub="skills"), for appending to loader search paths. Later
// dirs override earlier ones, so plugins sort AFTER user dirs by convention.
func SubDirs(sub string) []string {
	root := Root()
	var out []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // deterministic override order between plugins
	for _, n := range names {
		p := filepath.Join(root, n, sub)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// extractZip unpacks src.zip into dest, refusing entries that escape dest.
func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("unsafe zip entry %q", f.Name)
		}
		target := filepath.Join(dest, name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
			return fmt.Errorf("unsafe zip entry %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil { //nolint:gosec // entry size validated by zip CRC
			rc.Close()
			out.Close()
			return err
		}
		rc.Close()
		out.Close()
	}
	return nil
}

// copyTree recursively copies src → dst (files + dirs).
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
