// Package knowledge provides a lightweight, fully-local document knowledge
// base (RAG) for iCode — 对标 WorkBuddy 的"资料库"能力，但保持 iCode 的
// "省 token" 使命：不调用任何 embedding API，用本地哈希特征向量 + 余弦
// 相似度做语义检索，零网络、零 token、离线、确定性。
//
// Usage: point it at one or more doc directories (config knowledge.dirs), call
// Index(), then Search(query) returns the most relevant passages for the model.
package knowledge

import (
	"context"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Chunk is one indexed passage of a document.
type Chunk struct {
	File    string  `json:"file"`    // source file path
	Section string  `json:"section"` // heading / first line (context)
	Text    string  `json:"text"`    // passage body
	Score   float64 `json:"score"`   // cosine similarity (only set by Search)
	vec     map[uint64]float64
}

// Manager indexes and searches a set of document directories.
type Manager struct {
	mu     sync.RWMutex
	dirs   []string
	chunks []Chunk

	// idf maps a hashed feature to its inverse-document-frequency weight,
	// computed over the indexed chunks. Common words (appearing in most chunks)
	// get a low weight, rare discriminative words get a high weight — a local,
	// zero-API BM25-style boost that sharpens retrieval without any embedding
	// service. nil until the first Index completes.
	idf map[uint64]float64
}

// New creates a knowledge manager over the given directories.
func New(dirs []string) *Manager {
	return &Manager{dirs: dirs}
}

// Index scans the configured directories for .md/.txt files, splits them into
// passages, and builds their feature vectors. Returns the number of chunks.
func (m *Manager) Index(ctx context.Context) (int, error) {
	var chunks []Chunk
	for _, dir := range m.dirs {
		if dir == "" {
			continue
		}
		if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if info.IsDir() {
				name := info.Name()
				if name != dir && (name == ".git" || name == "node_modules" || strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".md" && ext != ".txt" && ext != ".markdown" {
				return nil
			}
			if selectable(ctx) {
				return ctx.Err()
			}
			data, err := os.ReadFile(path)
			if err != nil || len(data) == 0 {
				return nil
			}
			chunks = append(chunks, splitDoc(path, string(data))...)
			return nil
		}); err != nil {
			return len(chunks), err
		}
	}
	// Two-pass indexing: first compute document frequencies, then build
	// IDF-weighted vectors. Rare discriminative words get boosted, common
	// stopword-ish words get damped — a local BM25-style ranking that stays
	// fully offline (no embedding API, no tokens spent).
	df := make(map[uint64]int)
	for i := range chunks {
		for k := range tokenize(chunks[i].Text) {
			df[k]++
		}
	}
	n := len(chunks)
	idf := make(map[uint64]float64, len(df))
	if n > 0 {
		for k, d := range df {
			idf[k] = math.Log(float64(n+1)/float64(d+1)) + 1
		}
	}
	for i := range chunks {
		chunks[i].vec = featurizeIDF(chunks[i].Text, idf)
	}
	m.mu.Lock()
	m.chunks = chunks
	m.idf = idf
	m.mu.Unlock()
	return len(chunks), nil
}

// ChunkCount returns the number of currently indexed passages.
func (m *Manager) ChunkCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.chunks)
}

// Search returns the top-K passages most similar to query (by cosine
// similarity over local IDF-weighted features). Results are sorted best-first.
func (m *Manager) Search(query string, topK int) []Chunk {
	if topK <= 0 {
		topK = 5
	}
	m.mu.RLock()
	chunks := m.chunks
	idf := m.idf
	m.mu.RUnlock()

	q := featurizeIDF(query, idf)
	if len(q) == 0 {
		return nil
	}

	type scored struct {
		c Chunk
		s float64
	}
	hits := make([]scored, 0, len(chunks))
	for _, c := range chunks {
		if len(c.vec) == 0 {
			continue
		}
		hits = append(hits, scored{c, cosine(q, c.vec)})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].s > hits[j].s })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	out := make([]Chunk, 0, len(hits))
	for _, h := range hits {
		if h.s <= 0 {
			continue
		}
		h.c.Score = h.s
		h.c.vec = nil
		out = append(out, h.c)
	}
	return out
}

// Format renders search results for the model/user.
func Format(results []Chunk) string {
	if len(results) == 0 {
		return "（知识库中没有匹配内容）"
	}
	var sb strings.Builder
	for i, r := range results {
		sb.WriteString("### 来源: ")
		sb.WriteString(filepath.Base(r.File))
		if r.Section != "" {
			sb.WriteString(" / ")
			sb.WriteString(r.Section)
		}
		sb.WriteString("\n")
		sb.WriteString(r.Text)
		if i < len(results)-1 {
			sb.WriteString("\n\n---\n\n")
		}
	}
	return sb.String()
}

// splitDoc breaks a document into passages. Markdown headings (#/##/###) start
// a new passage; otherwise passages are ~40 lines with a 3-line overlap.
func splitDoc(path, content string) []Chunk {
	lines := strings.Split(content, "\n")
	var chunks []Chunk
	var cur []string
	section := ""

	flush := func() {
		if len(cur) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(cur, "\n"))
		if text == "" {
			cur = nil
			return
		}
		chunks = append(chunks, Chunk{File: path, Section: section, Text: text})
		cur = nil
	}

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			flush()
			section = strings.TrimSpace(strings.TrimLeft(trim, "# "))
			if section == "" {
				section = trim
			}
			cur = append(cur, line)
			continue
		}
		if trim == "" {
			continue
		}
		if len(cur) >= 40 {
			flush()
		}
		cur = append(cur, line)
	}
	flush()
	return chunks
}

// selectable reports whether ctx is already cancelled (avoids running the
// walk's custom error handling on context cancellation mid-index).
func selectable(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// ── Local hashed feature vectors (TF-weighted, IDF-boosted, L2-normalized) ──

// tokenize returns the raw term-frequency weights for text (unnormalized).
func tokenize(text string) map[uint64]float64 {
	v := make(map[uint64]float64)
	add := func(tok string, w float64) {
		if tok == "" {
			return
		}
		v[hashFeature(tok)] += w
	}

	// Word tokens (ASCII) + CJK bigrams.
	var sb strings.Builder
	flushWord := func() {
		if sb.Len() > 0 {
			w := strings.ToLower(sb.String())
			if len(w) >= 2 {
				add(w, 1.0)
			}
			sb.Reset()
		}
	}
	runes := []rune(strings.ToLower(text))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if isCJK(r) {
			flushWord()
			add(string(r), 1.0)
			if i+1 < len(runes) && isCJK(runes[i+1]) {
				add(string([]rune{r, runes[i+1]}), 1.5) // bigram weighted higher
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		} else {
			flushWord()
		}
	}
	flushWord()
	return v
}

// featurizeIDF builds an IDF-weighted, L2-normalized vector. When idf is nil
// it degrades to plain TF (used before the corpus stats are available).
func featurizeIDF(text string, idf map[uint64]float64) map[uint64]float64 {
	v := tokenize(text)
	if idf != nil {
		for k, w := range v {
			if idw, ok := idf[k]; ok {
				v[k] = w * idw
			}
		}
	}
	return normalize(v)
}

// featurize is the legacy no-IDF wrapper (kept for tests/clarity).
func featurize(text string) map[uint64]float64 {
	return featurizeIDF(text, nil)
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han) || unicode.In(r, unicode.Hiragana) ||
		unicode.In(r, unicode.Katakana) || unicode.In(r, unicode.Hangul)
}

func hashFeature(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func normalize(v map[uint64]float64) map[uint64]float64 {
	var sum float64
	for _, w := range v {
		sum += w * w
	}
	if sum == 0 {
		return v
	}
	norm := math.Sqrt(sum)
	for k, w := range v {
		v[k] = w / norm
	}
	return v
}

func dot(a, b map[uint64]float64) float64 {
	// iterate the smaller map
	if len(a) > len(b) {
		a, b = b, a
	}
	var s float64
	for k, v := range a {
		if bv, ok := b[k]; ok {
			s += v * bv
		}
	}
	return s
}

// cosine returns the cosine similarity of two normalized vectors.
func cosine(a, b map[uint64]float64) float64 {
	return dot(a, b)
}
