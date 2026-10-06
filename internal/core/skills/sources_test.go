package skills

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListSources_SeedsDefaults(t *testing.T) {
	home := isolateHome(t)

	list := ListSources()
	if len(list) == 0 {
		t.Fatal("expected seeded defaults, got empty list")
	}
	found := false
	for _, s := range list {
		if s.Source == "anthropics/skills" {
			found = true
		}
	}
	if !found {
		t.Errorf("default anchor anthropics/skills missing: %+v", list)
	}
	// The seed must have been persisted so the user can edit the file.
	if _, err := os.Stat(filepath.Join(home, ".icode", "skill_sources.json")); err != nil {
		t.Errorf("skill_sources.json not seeded: %v", err)
	}
}

func TestAddRemoveSource(t *testing.T) {
	isolateHome(t)

	added, err := AddSource("octo/example", "Example")
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if added.Alias != "Example" {
		t.Errorf("alias = %q, want Example", added.Alias)
	}
	// Duplicate is rejected.
	if _, err := AddSource("OCTO/example", "dupe"); !errors.Is(err, os.ErrExist) {
		t.Errorf("duplicate AddSource err = %v, want os.ErrExist", err)
	}
	list := ListSources()
	found := false
	for _, s := range list {
		if s.Source == "octo/example" {
			found = true
		}
	}
	if !found {
		t.Fatal("added source missing from list")
	}
	// Remove (case-insensitive match) then confirm gone.
	if err := RemoveSource("OCTO/example"); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
	for _, s := range ListSources() {
		if s.Source == "octo/example" {
			t.Error("source still present after remove")
		}
	}
	// Removing again → not exist.
	if err := RemoveSource("octo/example"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("re-remove err = %v, want os.ErrNotExist", err)
	}
}

func TestAddSource_RejectsInvalid(t *testing.T) {
	isolateHome(t)
	if _, err := AddSource("not a valid source", ""); err == nil {
		t.Error("expected error for invalid source")
	}
	if _, err := AddSource("", ""); err == nil {
		t.Error("expected error for empty source")
	}
	if _, err := AddSource("http://plain-http.example/x", ""); err == nil {
		t.Error("expected error for non-https URL")
	}
}

func TestRemoveSource_ToEmptyList(t *testing.T) {
	isolateHome(t)
	// Removing every entry must be allowed — the user owns the list.
	for _, s := range ListSources() {
		if err := RemoveSource(s.Source); err != nil {
			t.Fatalf("RemoveSource(%q): %v", s.Source, err)
		}
	}
	if got := ListSources(); len(got) != 0 {
		t.Errorf("expected empty list after removing all, got %+v", got)
	}
}
