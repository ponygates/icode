// Package skills — recommended remote sources (marketplace v1, A3).
//
// The user's saved skill sources live in ~/.icode/skill_sources.json:
//
//	[{"source": "anthropics/skills", "alias": "Anthropic 官方技能"}]
//
// The file is self-seeding: on first read it is created with the default
// recommendation list (the official Anthropic skills repo as the ecosystem
// anchor point). Users can add their own repos/URLs from the market UI and
// remove any entry — an empty list is valid and respected.
package skills

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// errNoHome is returned when the user home directory cannot be resolved, so
// there is nowhere to persist the source list.
var errNoHome = errors.New("cannot resolve user home dir")

// SkillSource is one saved remote source entry.
type SkillSource struct {
	Source string `json:"source"`           // owner/repo or https URL
	Alias  string `json:"alias,omitempty"`  // optional display label
}

// defaultSources seeds skill_sources.json on first use. Only entries we are
// confident exist are shipped; anything else belongs to the user's own list.
var defaultSources = []SkillSource{
	{Source: "anthropics/skills", Alias: "Anthropic 官方技能仓库"},
}

// ListSources returns the saved sources, seeding the file with defaults on
// first use. The result is never nil.
func ListSources() []SkillSource {
	p := sourcesPath()
	if p == "" {
		return append([]SkillSource(nil), defaultSources...)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		// First run (or unreadable): seed the defaults so the UI has
		// something to show and the file materialises for later edits.
		out := append([]SkillSource(nil), defaultSources...)
		_ = writeSources(p, out)
		return out
	}
	var out []SkillSource
	if err := json.Unmarshal(data, &out); err != nil {
		return append([]SkillSource(nil), defaultSources...)
	}
	if out == nil {
		out = []SkillSource{}
	}
	return out
}

// AddSource validates and saves a new source. Duplicates (by source string)
// are rejected rather than merged, so the chips row stays one-per-source.
func AddSource(source, alias string) (SkillSource, error) {
	source = strings.TrimSpace(source)
	if _, _, err := parseSource(source); err != nil {
		return SkillSource{}, err
	}
	alias = strings.TrimSpace(alias)
	p := sourcesPath()
	if p == "" {
		return SkillSource{}, errNoHome
	}
	list := ListSources()
	for _, s := range list {
		if strings.EqualFold(s.Source, source) {
			return SkillSource{}, os.ErrExist
		}
	}
	list = append(list, SkillSource{Source: source, Alias: alias})
	if err := writeSources(p, list); err != nil {
		return SkillSource{}, err
	}
	return SkillSource{Source: source, Alias: alias}, nil
}

// RemoveSource drops a saved source by its source string.
func RemoveSource(source string) error {
	source = strings.TrimSpace(source)
	p := sourcesPath()
	if p == "" {
		return errNoHome
	}
	list := ListSources()
	out := make([]SkillSource, 0, len(list))
	found := false
	for _, s := range list {
		if strings.EqualFold(s.Source, source) {
			found = true
			continue
		}
		out = append(out, s)
	}
	if !found {
		return os.ErrNotExist
	}
	return writeSources(p, out)
}

// sourcesPath returns ~/.icode/skill_sources.json ("" when home is unknown).
func sourcesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".icode", "skill_sources.json")
}

// writeSources persists the list with a stable shape.
func writeSources(p string, list []SkillSource) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}
