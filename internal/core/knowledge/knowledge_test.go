package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexAndSearch(t *testing.T) {
	dir := t.TempDir()
	doc1 := filepath.Join(dir, "保险条款.md")
	doc2 := filepath.Join(dir, "学习笔记.txt")
	os.WriteFile(doc1, []byte("# 犹豫期退保\n\n投保人自签收保单次日起 15 日内可无条件退保，保险公司全额退还保费。\n\n## 宽限期\n\n保费逾期未缴，给予 60 天宽限期。"), 0644)
	os.WriteFile(doc2, []byte("Go 的 goroutine 是轻量级线程，channel 用于 goroutine 间通信。"), 0644)

	m := New([]string{dir})
	n, err := m.Index(context.Background())
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if n == 0 {
		t.Fatal("expected non-zero chunks")
	}

	res := m.Search("退保 犹豫期 规则", 3)
	if len(res) == 0 {
		t.Fatal("expected hits for 退保 query")
	}
	if !contains(res[0].Text, "退保") {
		t.Fatalf("top hit should mention 退保, got: %s", res[0].Text)
	}

	res2 := m.Search("goroutine channel", 3)
	if len(res2) == 0 {
		t.Fatal("expected hits for goroutine query")
	}

	if got := Format(nil); got == "" {
		t.Fatal("Format(nil) should be non-empty")
	}
}

func TestSplitDocHeadings(t *testing.T) {
	chunks := splitDoc("/tmp/x.md", "# A\n\nbody A\n\n## B\n\nbody B\n")
	if len(chunks) < 2 {
		t.Fatalf("expected >=2 chunks, got %d", len(chunks))
	}
	if chunks[0].Section != "A" || chunks[1].Section != "B" {
		t.Fatalf("sections wrong: %q, %q", chunks[0].Section, chunks[1].Section)
	}
}

func contains(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && (len(s) >= len(sub)) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}

func TestIDFWeighting(t *testing.T) {
	dir := t.TempDir()
	// "退保" appears in only ONE chunk (rare → high IDF), "保险" in many.
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("# 条款\n\n退保 犹豫期 规则 说明"), 0644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("# 说明\n\n保险 保险 保险 说明 说明"), 0644)
	os.WriteFile(filepath.Join(dir, "c.md"), []byte("# 说明\n\n保险 说明 保险 说明"), 0644)

	m := New([]string{dir})
	m.Index(context.Background())

	m.mu.RLock()
	idfLen := len(m.idf)
	m.mu.RUnlock()
	if idfLen == 0 {
		t.Fatal("expected idf to be computed")
	}

	// Querying the rare term 退保 should surface the a.md chunk first.
	res := m.Search("退保", 3)
	if len(res) == 0 || !strings.Contains(res[0].Text, "退保") {
		t.Fatalf("rare-term query should rank 退保 chunk first, got: %+v", res)
	}
}
