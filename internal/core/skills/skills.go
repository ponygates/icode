// Package skills loads and serves SKILL.md files from .icode/skills/.
//
// Layout:
//
//	<project>/.icode/skills/<name>/SKILL.md  — project-scoped skills
//	~/.icode/skills/<name>/SKILL.md           — user-global skills
//
// Interop: WorkBuddy (.workbuddy/skills) and mimocode (.mimocode/skills)
// directories are loaded too, at lower priority than iCode's own dirs.
//
// Each SKILL.md has optional YAML frontmatter + Markdown body:
//
//	---
//	name: my-skill
//	description: Does something useful
//	triggers:
//	  - keyword1
//	  - keyword2
//	---
//	# My Skill
//	Instructions and workflow here...
package skills

import (
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ponygates/icode/internal/core/plugins"
	"gopkg.in/yaml.v3"
)

// Skill is a single loaded skill.
type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Triggers    []string `yaml:"triggers,omitempty"`
	Source      string   // absolute path of the SKILL.md
	Body        string   // markdown body after frontmatter
	Enabled     bool     `yaml:"enabled" json:"enabled"` // user-managed on/off switch
}

// Registry holds all loaded skills.
type Registry struct {
	skills   []Skill
	byName   map[string]*Skill
	disabled map[string]bool // names explicitly disabled by the user
}

// NewRegistry returns an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]*Skill{}, disabled: map[string]bool{}}
}

// Load walks directories and loads all SKILL.md files.
// Later directories override earlier ones.
func Load(dirs ...string) *Registry {
	r := NewRegistry()
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			skillPath := filepath.Join(dir, ent.Name(), "SKILL.md")
			data, err := os.ReadFile(skillPath)
			if err != nil {
				continue
			}
			skill := parseSkill(ent.Name(), skillPath, string(data))
			if skill != nil {
				r.byName[skill.Name] = skill
			}
		}
	}
	// Build sorted list from map.
	for _, s := range r.byName {
		r.skills = append(r.skills, *s)
	}
	sort.Slice(r.skills, func(i, j int) bool {
		return r.skills[i].Name < r.skills[j].Name
	})
	// Apply persistent user on/off switches.
	r.loadDisabledState()
	for i := range r.skills {
		r.skills[i].Enabled = !r.disabled[r.skills[i].Name]
	}
	return r
}

func parseSkill(name, path, content string) *Skill {
	body := content
	var meta struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Triggers    []string `yaml:"triggers,omitempty"`
	}

	if strings.HasPrefix(body, "---") {
		rest := body[3:]
		end := strings.Index(rest, "\n---")
		if end >= 0 {
			raw := rest[:end]
			raw = strings.TrimPrefix(raw, "\n")
			_ = yaml.Unmarshal([]byte(raw), &meta)
			body = rest[end+len("\n---"):]
			body = strings.TrimPrefix(body, "\n")
		}
	}

	if meta.Name == "" {
		meta.Name = name
	}

	return &Skill{
		Name:        meta.Name,
		Description: meta.Description,
		Triggers:    meta.Triggers,
		Source:      path,
		Body:        strings.TrimSpace(body),
	}
}

// Find returns skills matching the given trigger keywords.
// A skill matches if any trigger word appears in the query.
func (r *Registry) Find(query string) []*Skill {
	q := strings.ToLower(query)
	var hits []*Skill
	for _, s := range r.skills {
		if !s.Enabled {
			continue
		}
		for _, t := range s.Triggers {
			if strings.Contains(q, strings.ToLower(t)) {
				hits = append(hits, &s)
				break
			}
		}
	}
	return hits
}

// Get returns a skill by name.
func (r *Registry) Get(name string) (*Skill, bool) {
	s, ok := r.byName[name]
	return s, ok
}

// List returns all loaded skills.
func (r *Registry) List() []Skill {
	out := make([]Skill, len(r.skills))
	copy(out, r.skills)
	return out
}

// SetEnabled toggles a skill's user-managed on/off switch and persists it to
// ~/.icode/skills_state.yaml. Disabled skills are excluded from Find results
// and from the skill index offered to the model.
func (r *Registry) SetEnabled(name string, enabled bool) error {
	if r.disabled == nil {
		r.disabled = map[string]bool{}
	}
	if enabled {
		delete(r.disabled, name)
	} else {
		r.disabled[name] = true
	}
	for i := range r.skills {
		if r.skills[i].Name == name {
			r.skills[i].Enabled = enabled
		}
	}
	if s, ok := r.byName[name]; ok {
		s.Enabled = enabled
	}
	return r.saveDisabledState()
}

// loadDisabledState reads the persistent disabled-skill set (best-effort).
func (r *Registry) loadDisabledState() {
	r.disabled = map[string]bool{}
	path := statePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var names []string
	if err := yaml.Unmarshal(data, &names); err != nil {
		return
	}
	for _, n := range names {
		r.disabled[n] = true
	}
}

// saveDisabledState writes the disabled-skill set back to disk.
func (r *Registry) saveDisabledState() error {
	path := statePath()
	names := make([]string, 0, len(r.disabled))
	for n := range r.disabled {
		names = append(names, n)
	}
	sort.Strings(names)
	data, err := yaml.Marshal(names)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(path, data, 0o644)
}

// statePath returns the disabled-skill state file location.
func statePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".icode", "skills_state.yaml")
}

// FormatSystemPrompt renders matching skills into a system prompt fragment.
// NOTE: this dumps every skill's full body into the prompt. It is kept for
// backward compatibility and tests, but the engine should prefer
// FormatIndex — dumping full bodies into the immutable prefix defeats the
// Cache-First Loop once many skills are installed (the prefix grows on every
// skill and invalidates the provider's KV cache). Use FormatIndex + the
// use_skill tool for token-efficient, cache-stable skill discovery.
func FormatSystemPrompt(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Active Skills\n")
	for _, s := range skills {
		b.WriteString(fmt.Sprintf("\n### %s\n%s\n", s.Name, s.Body))
	}
	return b.String()
}

// FormatIndex renders a compact, cache-stable index of available skills.
// Unlike FormatSystemPrompt it only emits the name + one-line description +
// trigger keywords — never the full body. This keeps the immutable system
// prefix tiny and stable regardless of how many skills are installed, which
// is what preserves the provider's prefix-cache hit rate (the core of iCode's
// token-saving mechanism). The model loads a skill's full instructions on
// demand via the use_skill tool.
func FormatIndex(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Available Skills\n")
	b.WriteString("Skills are optional workflows you may follow when relevant. To load a skill's full instructions, call the `use_skill` tool with its name. Do not invent skill names — only use those listed here.\n")
	for _, s := range skills {
		trig := ""
		if len(s.Triggers) > 0 {
			trigs := s.Triggers
			if len(trigs) > 3 {
				trigs = trigs[:3]
			}
			trig = " [triggers: " + strings.Join(trigs, ", ") + "]"
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		// Clip long descriptions so the always-present skill index stays
		// small (long descriptions are the biggest fixed-input cost of the
		// prefix; full instructions are loaded on demand via use_skill).
		if r := []rune(desc); len(r) > 80 {
			desc = string(r[:80]) + "…"
		}
		b.WriteString(fmt.Sprintf("\n- **%s**: %s%s", s.Name, desc, trig))
	}
	return b.String()
}

// catalogFS embeds the built-in skill market shipped with iCode. These skills
// form a local "market" the user can browse and install with one click. The
// format is identical to project/user SKILL.md, so installing just copies the
// file into the user's skills directory. (A future remote manifest URL can
// extend this catalog without changing the install path.)
//
//go:embed catalog
var catalogFS embed.FS

// CatalogSkill describes a skill available in the built-in market.
type CatalogSkill struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Triggers    []string `json:"triggers,omitempty" yaml:"triggers,omitempty"`
	Installed   bool     `json:"installed"`
}

// userSkillsDir returns ~/.icode/skills, where user-installed and imported
// skills live. Returns "" if the home dir cannot be resolved.
func userSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".icode", "skills")
}

// ListCatalog returns every skill bundled in the built-in market, annotated
// with whether it is already installed into the user's skills directory.
func ListCatalog() []CatalogSkill {
	entries, err := catalogFS.ReadDir("catalog")
	if err != nil {
		return nil
	}
	out := make([]CatalogSkill, 0, len(entries))
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		data, err := catalogFS.ReadFile(path.Join("catalog", ent.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		s := parseSkill(ent.Name(), "catalog/"+ent.Name(), string(data))
		if s == nil {
			continue
		}
		out = append(out, CatalogSkill{
			Name:        s.Name,
			Description: s.Description,
			Triggers:    s.Triggers,
			Installed:   IsInstalled(s.Name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// IsInstalled reports whether a skill with the given name is present in the
// user's skills directory (installed from the market or imported locally).
func IsInstalled(name string) bool {
	dir := userSkillsDir()
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, name, "SKILL.md"))
	return err == nil
}

// Install copies a built-in market skill into the user's skills directory so
// it becomes active on the next registry load. Returns an error if the named
// skill is not present in the embedded catalog.
func Install(name string) error {
	data, err := catalogFS.ReadFile(path.Join("catalog", name, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("skill %q not found in market", name)
	}
	dir := userSkillsDir()
	if dir == "" {
		return fmt.Errorf("cannot resolve user skills dir")
	}
	dest := filepath.Join(dir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

// Uninstall removes a skill from the user's skills directory. It only deletes
// user-installed skills (never the embedded catalog, which is read-only, nor
// WorkBuddy/mimocode skills that live in a different directory).
func Uninstall(name string) error {
	dir := userSkillsDir()
	if dir == "" {
		return fmt.Errorf("cannot resolve user skills dir")
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		return fmt.Errorf("skill %q is not installed in user dir", name)
	}
	return os.RemoveAll(target)
}

// Import installs a skill from a local SKILL.md file (or a directory
// containing SKILL.md) into the user's skills directory. The skill name is
// taken from the file's frontmatter, falling back to the directory name.
func Import(src string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	skillPath := src
	if info.IsDir() {
		skillPath = filepath.Join(src, "SKILL.md")
	}
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return err
	}
	base := filepath.Base(filepath.Dir(skillPath))
	s := parseSkill(base, skillPath, string(data))
	if s == nil || s.Name == "" {
		return fmt.Errorf("invalid skill: missing name in frontmatter")
	}
	dir := userSkillsDir()
	if dir == "" {
		return fmt.Errorf("cannot resolve user skills dir")
	}
	dest := filepath.Join(dir, s.Name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

// DefaultDirs returns standard load paths. Besides iCode's own directories,
// WorkBuddy and mimocode skill directories are included as well — the SKILL.md
// format (YAML frontmatter with name/description + Markdown body) is
// compatible, so skills installed via those tools are immediately usable in
// iCode.
func DefaultDirs() []string {
	var out []string
	// Order matters: later dirs override earlier ones, so interop dirs come
	// first and iCode's own dirs win on name conflicts. Installed plugins
	// sort last so a plugin can shadow built-in skill names deliberately.
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out,
			filepath.Join(home, ".workbuddy", "skills"), // WorkBuddy user-level skills
			filepath.Join(home, ".mimocode", "skills"),  // mimocode user-level skills
			filepath.Join(home, ".icode", "skills"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		out = append(out,
			filepath.Join(cwd, ".workbuddy", "skills"), // WorkBuddy project-level skills
			filepath.Join(cwd, ".mimocode", "skills"),  // mimocode project-level skills
			filepath.Join(cwd, ".icode", "skills"),
		)
	}
	// Installed plugins last: plugin skills join the search space and may
	// override same-named user/built-in entries.
	out = append(out, plugins.SubDirs("skills")...)
	return out
}
