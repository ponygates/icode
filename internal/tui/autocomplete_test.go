package tui

import "testing"

func TestFuzzyScore(t *testing.T) {
	cases := []struct {
		query string
		name  string
		want  int // -1 = no match; 0 = exact prefix
	}{
		{"", "/model", 0},
		{"/mod", "/model", 0},   // prefix match always ranks best
		{"/MODEL", "/model", 0}, // case-insensitive prefix
		{"/xyz", "/model", -1},  // no subsequence
		{"/tasks", "/tasks", 0}, // exact
	}
	for _, tc := range cases {
		got := fuzzyScore(tc.query, tc.name)
		if got != tc.want {
			t.Errorf("fuzzyScore(%q,%q) = %d, want %d", tc.query, tc.name, got, tc.want)
		}
	}
	// Contiguous subsequences beat gappy ones: /cnf matches the adjacent
	// run c-n-f in "/config", while /cfg skips over o,n and i.
	if fuzzyScore("/cnf", "/config") <= fuzzyScore("/cfg", "/config") {
		t.Errorf("expected /cnf (%d) to outrank /cfg (%d)",
			fuzzyScore("/cnf", "/config"), fuzzyScore("/cfg", "/config"))
	}
	// Prefix beats any subsequence.
	if fuzzyScore("/con", "/config") != 0 {
		t.Error("prefix match must score 0")
	}
}

func TestRankSuggestionsRecencyFirst(t *testing.T) {
	tr := &TUI{}
	tr.noteRecentCmd("/token")
	tr.noteRecentCmd("/model")
	tr.noteRecentCmd("/token") // re-use moves /token back to newest

	items := []acItem{
		{Name: "/help"}, {Name: "/model"}, {Name: "/zzz"}, {Name: "/token"},
	}
	tr.rankSuggestions(items)
	if items[0].Name != "/token" {
		t.Errorf("items[0] = %s, want /token (most recent)", items[0].Name)
	}
	if items[1].Name != "/model" {
		t.Errorf("items[1] = %s, want /model (second most recent)", items[1].Name)
	}
}

func TestUsageHint(t *testing.T) {
	if usageHint("/lang") == "" {
		t.Error("/lang should carry an argument hint")
	}
	if usageHint("/help") != "" {
		t.Error("/help takes no args")
	}
}

func TestCompleteSlashCommandIncludesCustom(t *testing.T) {
	tr := &TUI{}
	// Built-in expansion still works.
	got, changed := tr.completeSlashCommand("/mem")
	if !changed || got != "/memory" {
		t.Errorf("/mem expanded to %q (changed=%v), want /memory", got, changed)
	}
	// Exact commands are never rewritten.
	got2, changed2 := tr.completeSlashCommand("/memory extra args")
	if changed2 || got2 != "/memory extra args" {
		t.Errorf("exact command rewritten: %q %v", got2, changed2)
	}
}
