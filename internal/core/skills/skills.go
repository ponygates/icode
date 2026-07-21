// Package skills loads and serves SKILL.md files from .icode/skills/.
//
// Layout:
//
//	<project>/.icode/skills/<name>/SKILL.md  — project-scoped skills
//	~/.icode/skills/<name>/SKILL.md           — user-global skills
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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill is a single loaded skill.
type Skill struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Triggers    []string `yaml:"triggers,omitempty"`
	Source      string   // absolute path of the SKILL.md
	Body        string   // markdown body after frontmatter
}

// Registry holds all loaded skills.
type Registry struct {
	skills []Skill
	byName map[string]*Skill
}

// NewRegistry returns an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]*Skill{}}
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

// FormatSystemPrompt renders matching skills into a system prompt fragment.
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

// DefaultDirs returns standard load paths.
func DefaultDirs() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".icode", "skills"))
	}
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Join(cwd, ".icode", "skills"))
	}
	return out
}
