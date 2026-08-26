package plugins

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleManifest = "name: my-toolkit\nversion: 1.2.0\ndescription: test bundle\n"

func makePluginDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(sampleManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, "skills", name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// withRoot points the plugins root at a temp dir for isolation.
func withRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := rootOverride
	rootOverride = root
	t.Cleanup(func() { rootOverride = old })
	return root
}

func TestInstallListRemove(t *testing.T) {
	withRoot(t)
	src := makePluginDir(t, "toolkit")

	m, err := Install(src, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if m.Name != "my-toolkit" || m.Version != "1.2.0" {
		t.Fatalf("manifest = %+v", m)
	}

	// Installed layout carries the skill subdir.
	if _, err := os.Stat(filepath.Join(Root(), "my-toolkit", "skills", "toolkit", "SKILL.md")); err != nil {
		t.Errorf("skill file missing after install: %v", err)
	}

	list := List()
	if len(list) != 1 || list[0].Name != "my-toolkit" {
		t.Fatalf("list = %+v", list)
	}

	dirs := SubDirs("skills")
	if len(dirs) != 1 || !strings.HasSuffix(dirs[0], filepath.Join("my-toolkit", "skills")) {
		t.Errorf("SubDirs = %v", dirs)
	}

	// Re-install without force refuses; with force succeeds.
	if _, err := Install(src, false); err == nil {
		t.Error("duplicate install should refuse")
	}
	if _, err := Install(src, true); err != nil {
		t.Errorf("force install: %v", err)
	}

	if err := Remove("my-toolkit"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(List()) != 0 {
		t.Error("list not empty after remove")
	}
	if err := Remove("ghost"); err == nil {
		t.Error("removing uninstalled plugin should error")
	}
}

func TestLoadManifestValidation(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadManifest(dir); err == nil {
		t.Error("missing manifest should error")
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("version: 1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(dir); err == nil {
		t.Error("missing name should error")
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("name: \"a/b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(dir); err == nil {
		t.Error("path-hostile name should error")
	}
}

func TestInstallZipAndSlipGuard(t *testing.T) {
	withRoot(t)

	// Build a valid zip wrapping one top-level folder.
	zipPath := filepath.Join(t.TempDir(), "bundle.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	src := makePluginDir(t, "zipped")
	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join("my-toolkit-zip", rel)
		if info.IsDir() {
			_, err := zw.Create(target + "/")
			return err
		}
		data, _ := os.ReadFile(path)
		w, err := zw.Create(target)
		if err != nil {
			return err
		}
		_, werr := w.Write(data)
		return werr
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zf.Close()

	m, err := Install(zipPath, false)
	if err != nil {
		t.Fatalf("zip install: %v", err)
	}
	if m.Name != "my-toolkit" {
		t.Fatalf("zip manifest = %+v", m)
	}
	if _, err := os.Stat(filepath.Join(Root(), "my-toolkit", "plugin.yaml")); err != nil {
		t.Errorf("zip contents missing: %v", err)
	}

	// Zip-slip guard: an entry escaping the destination must abort install.
	slip := filepath.Join(t.TempDir(), "slip.zip")
	sf, _ := os.Create(slip)
	sw := zip.NewWriter(sf)
	evils := []string{"../evil.txt", `/abs/evil.txt`}
	for _, e := range evils {
		if _, err := sw.Create(e); err != nil {
			t.Fatal(err)
		}
	}
	sw.Close()
	sf.Close()
	if _, err := Install(slip, false); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("zip-slip = %v, want unsafe-entry error", err)
	}
	// Nothing escaped into the plugins root.
	entries, _ := os.ReadDir(Root())
	for _, e := range entries {
		if strings.Contains(e.Name(), "evil") {
			t.Error("zip-slip wrote outside dest")
		}
	}
}
