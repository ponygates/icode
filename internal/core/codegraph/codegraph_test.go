package codegraph

// Tests for the symbol index: extraction patterns, tree walking (skip dirs,
// ignored suffixes, size cap), search accessors and concurrent use.
// Usage context: internal/core/tool/codesearch.go builds one Graph per
// project root and calls Search / SymbolsInFile / Count on it from the
// code_search tool.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// buildTree writes a fixture project into a temp dir and returns the dir.
func buildTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		fp := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSymbolKindString(t *testing.T) {
	cases := []struct {
		kind SymbolKind
		want string
	}{
		{SymbolFunc, "func"},
		{SymbolType, "type"},
		{SymbolStruct, "struct"},
		{SymbolInterface, "interface"},
		{SymbolVar, "var"},
		{SymbolMethod, "method"},
		{SymbolKind(99), "unknown"},
		{SymbolKind(-1), "unknown"},
	}
	for _, c := range cases {
		if got := c.kind.String(); got != c.want {
			t.Errorf("SymbolKind(%d).String() = %q, want %q", int(c.kind), got, c.want)
		}
	}
}

func TestNewIsEmpty(t *testing.T) {
	g := New()
	if g.Count() != 0 {
		t.Errorf("new graph Count = %d, want 0", g.Count())
	}
	if got := g.AllSymbols(); len(got) != 0 {
		t.Errorf("new graph AllSymbols = %v, want empty", got)
	}
	if got := g.Search("anything"); len(got) != 0 {
		t.Errorf("Search on empty graph = %v, want empty", got)
	}
	if got := g.SymbolsInFile("x.go"); got != nil {
		t.Errorf("SymbolsInFile miss = %v, want nil", got)
	}
}

func TestExtractGoSymbols(t *testing.T) {
	cases := []struct {
		name         string
		line         string
		wantName     string
		wantKind     SymbolKind
		wantCount    int
		wantReceiver string
	}{
		{"plain func", "func Alpha() {", "Alpha", SymbolFunc, 1, ""},
		{"func with params", "func hello(a, b int) int {", "hello", SymbolFunc, 1, ""},
		{"value receiver", "func (s Store) Get(key string) string {", "Get", SymbolMethod, 1, "Store"},
		{"struct", "type Beta struct {", "Beta", SymbolStruct, 1, ""},
		{"interface", "type Reader interface {", "Reader", SymbolInterface, 1, ""},
		{"named type", "type Celsius float64", "Celsius", SymbolType, 1, ""},
		{"type alias", "type Alias = Target", "Alias", SymbolType, 1, ""},
		{"var", "var registry = map[string]int{}", "registry", SymbolVar, 1, ""},
		{"const", `const Version = "1.0"`, "Version", SymbolVar, 1, ""},
		{"call is not a def", "result := compute(1)", "", SymbolKind(-1), 0, ""},
		{"var block opener", "var (", "", SymbolKind(-1), 0, ""},
		{"indented local var (pre-trimmed)", "var buf bytes.Buffer", "buf", SymbolVar, 1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			syms := extractGoSymbols(c.line, 7, "x.go")
			if len(syms) != c.wantCount {
				t.Fatalf("extractGoSymbols(%q) = %d symbols, want %d", c.line, len(syms), c.wantCount)
			}
			if c.wantCount == 0 {
				return
			}
			s := syms[0]
			if s.Name != c.wantName || s.Kind != c.wantKind {
				t.Errorf("extractGoSymbols(%q) = {%s %s}, want {%s %s}", c.line, s.Kind, s.Name, c.wantKind, c.wantName)
			}
			if s.Receiver != c.wantReceiver {
				t.Errorf("extractGoSymbols(%q) receiver = %q, want %q", c.line, s.Receiver, c.wantReceiver)
			}
			if s.Line != 7 || s.FilePath != "x.go" {
				t.Errorf("symbol position = %s:%d, want x.go:7", s.FilePath, s.Line)
			}
		})
	}
}

// TestExtractGoSymbols_PointerReceiverReportsTypeName expresses the expected
// contract: Symbol.Receiver is documented as "the receiver type name", so for
// `func (s *Store) Get` it must be "Store" (matching the value-receiver case).
func TestExtractGoSymbols_PointerReceiverReportsTypeName(t *testing.T) {
	syms := extractGoSymbols("func (s *Store) Get(key string) string {", 1, "x.go")
	if len(syms) != 1 {
		t.Fatalf("got %d symbols, want 1", len(syms))
	}
	if syms[0].Kind != SymbolMethod {
		t.Errorf("kind = %v, want method", syms[0].Kind)
	}
	if syms[0].Receiver != "Store" {
		t.Errorf("Receiver = %q, want %q", syms[0].Receiver, "Store")
	}
}

// The '*' suffix bug only affects Receiver text; the method/func classification
// around it must still work (this part passes today).
func TestExtractGoSymbols_PointerReceiverIsMethod(t *testing.T) {
	syms := extractGoSymbols("func (s *Store) Get(key string) string {", 2, "x.go")
	if len(syms) != 1 || syms[0].Kind != SymbolMethod || syms[0].Name != "Get" {
		t.Fatalf("pointer receiver func = %+v, want one method named Get", syms)
	}
	if syms[0].Receiver != "Store" {
		t.Errorf("Receiver = %q, want %q", syms[0].Receiver, "Store")
	}
}

func TestExtractTSSymbols(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		wantName  string
		wantKind  SymbolKind
		wantCount int
	}{
		{"function", "function foo(a) {", "foo", SymbolFunc, 1},
		{"exported async", "export async function load() {", "load", SymbolFunc, 1},
		{"class", "class Foo extends Bar {", "Foo", SymbolStruct, 1},
		{"exported abstract class", "export abstract class Base {", "Base", SymbolStruct, 1},
		{"interface", "export interface Props {", "Props", SymbolInterface, 1},
		{"arrow const", "export const App = () => (", "App", SymbolFunc, 1},
		{"plain var", "let count = 0", "count", SymbolVar, 1},
		{"function-expression keeps var name", "const handler = function(e) {", "handler", SymbolVar, 1},
		{"no symbol", "console.log('hi')", "", SymbolKind(-1), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			syms := extractTSSymbols(c.line, 3, "x.ts")
			if len(syms) != c.wantCount {
				t.Fatalf("extractTSSymbols(%q) = %d symbols, want %d", c.line, len(syms), c.wantCount)
			}
			if c.wantCount == 0 {
				return
			}
			if syms[0].Name != c.wantName || syms[0].Kind != c.wantKind {
				t.Errorf("extractTSSymbols(%q) = {%s %s}, want {%s %s}", c.line, syms[0].Kind, syms[0].Name, c.wantKind, c.wantName)
			}
		})
	}
}

func TestExtractSymbolsSkipsCommentsAndBlank(t *testing.T) {
	cases := []string{"", "   ", "\t", "// func Foo() {}", "/* var x = 1 */"}
	for _, line := range cases {
		for _, ext := range []string{".go", ".ts"} {
			if syms := extractSymbols(line, 1, "x"+ext); len(syms) != 0 {
				t.Errorf("extractSymbols(%q, %s) = %v, want none", line, ext, syms)
			}
		}
	}
	// Unknown extensions produce nothing even for obvious definitions.
	if syms := extractSymbols("func Alpha() {}", 1, "x.py"); len(syms) != 0 {
		t.Errorf("extractSymbols on .py = %v, want none (ext not dispatched)", syms)
	}
}

func TestBuildIndexesTreeAndHonoursSkips(t *testing.T) {
	dir := buildTree(t, map[string]string{
		"a.go":                  "package main\n\nfunc Alpha() {}\n\ntype Beta struct {\n\tname string\n}\n",
		"sub/c.ts":              "export function gamma() {}\n",
		"sub/d.jsx":             "const Delta = () => null\n",
		"notes.txt":             "func NotIndexed() {}\n",
		"node_modules/pkg/e.go": "func Zed() {}\n",
		"dist/f.ts":             "export function Bundled() {}\n",
		"g.min.js":              "function minfn() {}\n",
		"h.pb.go":               "func ProtoThing() {}\n",
	})
	g := New()
	if err := g.Build(dir); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Indexed: Alpha, Beta, gamma, Delta. Skipped: Zed/Bundled/minfn/ProtoThing
	// and everything in notes.txt.
	if got := g.Count(); got != 4 {
		t.Errorf("Count = %d, want 4 (AllSymbols: %v)", got, names(g.AllSymbols()))
	}
	for _, miss := range []string{"Alpha", "Beta", "gamma", "Delta"} {
		if len(g.Search(miss)) == 0 {
			t.Errorf("Search(%q) found nothing", miss)
		}
	}
	for _, banned := range []string{"Zed", "Bundled", "minfn", "ProtoThing", "NotIndexed"} {
		if got := g.Search(banned); len(got) != 0 {
			t.Errorf("Search(%q) = %v, want none (must be skipped)", banned, got)
		}
	}

	// Exact positions: a.go func Alpha on line 3, struct Beta on line 5.
	afs := g.SymbolsInFile(filepath.Join(dir, "a.go"))
	if len(afs) != 2 {
		t.Fatalf("SymbolsInFile(a.go) = %v, want 2 symbols", names(afs))
	}
	if afs[0].Name != "Alpha" || afs[0].Line != 3 || afs[0].Kind != SymbolFunc {
		t.Errorf("first symbol = %+v, want Alpha func at line 3", afs[0])
	}
	if afs[1].Name != "Beta" || afs[1].Line != 5 || afs[1].Kind != SymbolStruct {
		t.Errorf("second symbol = %+v, want Beta struct at line 5", afs[1])
	}

	// Search is a case-insensitive substring match.
	if got := g.Search("ALP"); len(got) != 1 || got[0].Name != "Alpha" {
		t.Errorf("Search(ALP) = %v, want just Alpha", names(got))
	}
	if got := g.Search("a"); len(got) == 0 {
		t.Error("Search of single char must still match by substring")
	}
	// Empty query matches every symbol (strings.Contains(x, "")).
	if got := g.Search(""); len(got) != g.Count() {
		t.Errorf("Search(\"\") = %d symbols, want %d", len(got), g.Count())
	}
	// Missing file returns nil.
	if got := g.SymbolsInFile(filepath.Join(dir, "zz.go")); got != nil {
		t.Errorf("SymbolsInFile(missing) = %v, want nil", got)
	}
}

func TestAccessorsReturnCopies(t *testing.T) {
	dir := buildTree(t, map[string]string{
		"a.go": "package main\n\nfunc Alpha() {}\n",
	})
	g := New()
	if err := g.Build(dir); err != nil {
		t.Fatalf("Build: %v", err)
	}

	all := g.AllSymbols()
	all[0].Name = "MUTATED"
	if got := g.AllSymbols(); got[0].Name != "Alpha" {
		t.Errorf("mutating AllSymbols result leaked into graph: %v", got)
	}
	if got := g.Search("Alpha"); len(got) != 1 {
		t.Errorf("graph must be intact after caller mutated the returned slice, got %v", got)
	}

	found := g.Search("alp")
	found[0].Name = "MUTATED"
	if got := g.Search("alp"); len(got) != 1 || got[0].Name != "Alpha" {
		t.Errorf("mutating Search result leaked into graph: %v", got)
	}

	inFile := g.SymbolsInFile(filepath.Join(dir, "a.go"))
	inFile[0].Name = "MUTATED"
	if got := g.SymbolsInFile(filepath.Join(dir, "a.go")); len(got) == 0 || got[0].Name != "Alpha" {
		t.Errorf("mutating SymbolsInFile result leaked into graph: %v", got)
	}
}

func TestBuildSkipsOversizedFile(t *testing.T) {
	// indexFile ignores files > 1 MiB (maxFileSize at codegraph.go:118).
	dir := buildTree(t, map[string]string{
		"small.go": "package main\n\nfunc Small() {}\n",
	})
	big := filepath.Join(dir, "big.go")
	padding := strings.Repeat("x", 1<<20)
	if err := os.WriteFile(big, []byte("func Oversized() {}\n"+padding), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(big); err == nil && fi.Size() <= 1<<20 {
		t.Fatalf("fixture too small to exceed cap: %d bytes", fi.Size())
	}
	g := New()
	if err := g.Build(dir); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := g.Search("Oversized"); len(got) != 0 {
		t.Errorf("file over 1 MiB must be skipped, got %v", got)
	}
	if got := g.Search("Small"); len(got) != 1 {
		t.Errorf("normal file next to the oversized one must still be indexed, got %v", got)
	}
}

func TestBuildErrorPaths(t *testing.T) {
	cases := []struct {
		name string
		root string
	}{
		{"missing dir", filepath.Join(t.TempDir(), "does-not-exist")},
		{"empty root", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := New()
			if err := g.Build(c.root); err == nil {
				t.Fatalf("Build(%q) must return an error", c.root)
			}
			if g.Count() != 0 {
				t.Errorf("graph should stay empty after failed build, count=%d", g.Count())
			}
		})
	}
}

func TestBuildResetsPreviousIndex(t *testing.T) {
	dirA := buildTree(t, map[string]string{"a.go": "package main\n\nfunc OnlyInA() {}\n"})
	dirB := buildTree(t, map[string]string{"b.go": "package main\n\nfunc OnlyInB() {}\n"})
	g := New()
	if err := g.Build(dirA); err != nil {
		t.Fatalf("Build A: %v", err)
	}
	if err := g.Build(dirB); err != nil {
		t.Fatalf("Build B: %v", err)
	}
	if got := g.Search("OnlyInA"); len(got) != 0 {
		t.Errorf("rebuild must drop old symbols, got %v", got)
	}
	if got := g.Search("OnlyInB"); len(got) != 1 {
		t.Errorf("rebuild must keep new symbols, got %v", got)
	}
	if got := g.SymbolsInFile(filepath.Join(dirA, "a.go")); got != nil {
		t.Errorf("byFile map must be reset too, got %v", got)
	}
}

func TestBuildOnSingleFileRoot(t *testing.T) {
	// codesearch roots at a directory, but filepath.Walk also accepts a file.
	dir := buildTree(t, map[string]string{"a.go": "package main\n\nfunc Alpha() {}\n"})
	g := New()
	if err := g.Build(filepath.Join(dir, "a.go")); err != nil {
		t.Fatalf("Build(file): %v", err)
	}
	if g.Count() != 1 {
		t.Errorf("Count = %d, want 1 (AllSymbols: %v)", g.Count(), names(g.AllSymbols()))
	}
}

func TestConcurrentBuildAndReads(t *testing.T) {
	dir := buildTree(t, map[string]string{
		"a.go": "package main\n\nfunc Alpha() {}\n\nfunc Beta() {}\n",
		"b.ts": "export function gamma() {}\n",
	})
	g := New()
	if err := g.Build(dir); err != nil {
		t.Fatalf("initial Build: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i == 0 {
				// Concurrent rebuilds reset + repopulate under the write lock.
				for n := 0; n < 5; n++ {
					if err := g.Build(dir); err != nil {
						t.Errorf("rebuild: %v", err)
						return
					}
				}
				return
			}
			for n := 0; n < 200; n++ {
				_ = g.Search("alpha")
				_ = g.Count()
				_ = g.AllSymbols()
				_ = g.SymbolsInFile(filepath.Join(dir, "a.go"))
			}
		}(i)
	}
	wg.Wait()

	if got := g.Count(); got != 3 {
		t.Errorf("Count after concurrent rebuilds = %d, want 3", got)
	}
}

func names(syms []Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = fmt.Sprintf("%s:%s@%d", s.Kind, s.Name, s.Line)
	}
	return out
}
