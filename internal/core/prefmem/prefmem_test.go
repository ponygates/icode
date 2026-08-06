package prefmem

import (
	"strings"
	"testing"
	"time"
)

func TestExtractDetectsChinesePreferences(t *testing.T) {
	got := Extract("以后都用简体中文回答。另外帮我看看这个bug")
	found := false
	for _, g := range got {
		if strings.Contains(g, "简体中文回答") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Chinese preference extracted, got %v", got)
	}
}

func TestExtractIgnoresPlainTask(t *testing.T) {
	// No preference marker => no memory candidate. Code/task text must never
	// be remembered (book: "remember preferences, never code").
	got := Extract("帮我重构一下 internal/core/conversation/engine.go，让它更快")
	if len(got) != 0 {
		t.Fatalf("plain task must not be remembered, got %v", got)
	}
}

func TestExtractEnglishPreference(t *testing.T) {
	got := Extract("please always use tabs for indentation, thanks")
	if len(got) == 0 {
		t.Fatal("expected English preference extracted")
	}
	if !strings.Contains(got[0], "tabs for indentation") {
		t.Fatalf("unexpected phrase: %q", got[0])
	}
}

func TestRememberAndRender(t *testing.T) {
	s := New(Options{})
	e := s.Remember("以后都用简体中文回答")
	if e == nil || e.Seen != 1 {
		t.Fatalf("first remember: %+v", e)
	}
	// Repeating refreshes the recency counter.
	s.Remember("以后都用简体中文回答")
	block := s.Render()
	if !strings.Contains(block, "简体中文回答") {
		t.Fatalf("render missing preference: %q", block)
	}
	if !strings.Contains(block, "USER PREFERENCES") {
		t.Fatalf("render missing header: %q", block)
	}
}

func TestStaleEntryAgesOut(t *testing.T) {
	s := New(Options{TTL: time.Hour})
	s.Remember("以后都用简体中文回答")
	if len(s.List()) != 1 {
		t.Fatal("fresh entry should be live")
	}
	// Age the entry beyond TTL by moving SeenAt backwards.
	s.mu.Lock()
	for _, e := range s.entries {
		e.SeenAt = time.Now().Add(-2 * time.Hour)
	}
	s.mu.Unlock()
	if len(s.List()) != 0 {
		t.Fatal("stale entry should age out")
	}
	if s.Render() != "" {
		t.Fatal("stale memory should not be injected")
	}
}

func TestCapacityEvictsOldest(t *testing.T) {
	s := New(Options{MaxEntries: 2})
	s.Remember("pref one")
	s.Remember("pref two")
	s.Remember("pref three")
	live := s.List()
	if len(live) != 2 {
		t.Fatalf("expected capacity 2, got %d (%v)", len(live), live)
	}
	for _, e := range live {
		if e.Text == "pref one" {
			t.Fatalf("oldest should be evicted: %v", live)
		}
	}
}

func TestRestoreMergesAndDropsStale(t *testing.T) {
	s := New(Options{TTL: time.Hour, MaxEntries: 10})
	now := time.Now()
	s.Restore([]Entry{
		{Text: "prefer x", SeenAt: now.Add(-time.Minute), Seen: 1},
		{Text: "prefer stale", SeenAt: now.Add(-2 * time.Hour), Seen: 1},
	})
	if len(s.List()) != 1 {
		t.Fatalf("stale restore should be dropped, got %v", s.List())
	}
	// Re-restoring a fresher version of the same key wins.
	s.Restore([]Entry{{Text: "prefer x", SeenAt: now, Seen: 3}})
	if e := s.List()[0]; e.Seen != 3 {
		t.Fatalf("restore should merge Seen count, got %+v", e)
	}
}

func TestForgetAndPurge(t *testing.T) {
	s := New(Options{})
	s.Remember("a")
	s.Remember("b")
	if !s.Forget("a") {
		t.Fatal("forget should report existed")
	}
	if s.Forget("a") {
		t.Fatal("second forget should report missing")
	}
	if n := s.Purge(); n != 1 {
		t.Fatalf("purge should remove 1, removed %d", n)
	}
	if s.Render() != "" {
		t.Fatal("render should be empty after purge")
	}
}

// TestExtract_OverlapDedup verifies overlapping markers ("以后都用" vs
// "以后都") do not emit near-duplicate phrases — only the longest match wins.
func TestExtract_OverlapDedup(t *testing.T) {
	got := Extract("以后都用简体中文回答我")
	// "以后都用" (longest) yields "简体中文回答我". The shorter "以后都" would
	// yield "用简体中文回答我" which is a suffix-duplicate and must be dropped.
	for _, g := range got {
		if strings.HasSuffix(g, "用简体中文回答我") {
			t.Fatalf("near-duplicate phrase leaked: %q (all: %v)", g, got)
		}
	}
	if len(got) == 0 {
		t.Fatal("expected at least one extracted phrase")
	}
}

// TestExtract_NewMarkers verifies expanded Chinese markers are recognized.
func TestExtract_NewMarkers(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"以后请务必用 markdown 写文档", "用 markdown 写文档"},
		{"请务必先跑测试", "先跑测试"},
		{"我会优先用 sqlite 存数据", "用 sqlite 存数据"},
		{"尽量使用小写命名", "小写命名"},
		{"我习惯用 tab 缩进", "用 tab 缩进"},
		{"别再用 eslint", "eslint"},
		{"请用中文回复我", "中文回复我"},
	}
	for _, c := range cases {
		got := Extract(c.in)
		found := false
		for _, g := range got {
			if strings.Contains(g, strings.TrimPrefix(c.want, "")) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Extract(%q) = %v, want containing %q", c.in, got, c.want)
		}
	}
}

// TestExtract_EnglishNewMarkers verifies the expanded English markers.
func TestExtract_EnglishNewMarkers(t *testing.T) {
	got := Extract("from now on always use tabs for indentation")
	if len(got) == 0 {
		t.Fatal("expected extraction for from now on always")
	}
	got = Extract("i prefer to write tests first")
	if len(got) == 0 {
		t.Fatal("expected extraction for i prefer to")
	}
}
