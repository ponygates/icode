// Package codegraph builds a lightweight symbol index for the project.
//
// It extracts function/type/struct/interface/variable definitions from
// source files and makes them searchable. Supports Go and TypeScript/JS.
package codegraph

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// SymbolKind is the kind of code symbol.
type SymbolKind int

const (
	SymbolFunc    SymbolKind = iota // function / method
	SymbolType                      // type definition
	SymbolStruct                    // struct
	SymbolInterface                 // interface
	SymbolVar                       // variable / const
	SymbolMethod                    // method on a type
)

func (k SymbolKind) String() string {
	switch k {
	case SymbolFunc:
		return "func"
	case SymbolType:
		return "type"
	case SymbolStruct:
		return "struct"
	case SymbolInterface:
		return "interface"
	case SymbolVar:
		return "var"
	case SymbolMethod:
		return "method"
	default:
		return "unknown"
	}
}

// Symbol represents one indexed symbol.
type Symbol struct {
	Name     string
	Kind     SymbolKind
	FilePath string
	Line     int
	Receiver string // for methods: the receiver type name
}

// Graph is a read-only symbol index.
type Graph struct {
	mu      sync.RWMutex
	symbols []Symbol
	byFile  map[string][]Symbol // file path → symbols in that file
	byName  map[string][]Symbol // symbol name → all matches
}

// New creates an empty graph.
func New() *Graph {
	return &Graph{
		byFile: map[string][]Symbol{},
		byName: map[string][]Symbol{},
	}
}

// Build walks rootDir and indexes all recognized source files.
func (g *Graph) Build(rootDir string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Reset
	g.symbols = nil
	g.byFile = map[string][]Symbol{}
	g.byName = map[string][]Symbol{}

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		ext := filepath.Ext(path)
		switch ext {
		case ".go", ".ts", ".tsx", ".js", ".jsx":
		default:
			return nil
		}
		return g.indexFile(path)
	})
	return err
}

func (g *Graph) indexFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	var fileSymbols []Symbol

	for i, line := range lines {
		syms := extractSymbols(line, i+1, path)
		fileSymbols = append(fileSymbols, syms...)
	}

	g.symbols = append(g.symbols, fileSymbols...)
	g.byFile[path] = fileSymbols

	for _, s := range fileSymbols {
		g.byName[s.Name] = append(g.byName[s.Name], s)
	}

	return nil
}

// Go patterns
var (
	goFuncRe   = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?(\w+)\s*\(`)
	goTypeRe   = regexp.MustCompile(`^type\s+(\w+)\s+`)
	goStructRe = regexp.MustCompile(`^type\s+(\w+)\s+struct`)
	goIntfRe   = regexp.MustCompile(`^type\s+(\w+)\s+interface`)
	goVarRe    = regexp.MustCompile(`^(?:var|const)\s+(\w+)`)
)

// TS/JS patterns
var (
	tsFuncRe = regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\(`)
	tsClassRe = regexp.MustCompile(`(?:export\s+)?(?:abstract\s+)?class\s+(\w+)`)
	tsIntfRe  = regexp.MustCompile(`(?:export\s+)?interface\s+(\w+)`)
	tsVarRe   = regexp.MustCompile(`(?:export\s+)?(?:const|let|var)\s+(\w+)`)
	tsArrowRe = regexp.MustCompile(`(?:export\s+)?(?:const|let|var)\s+(\w+)\s*[:=]\s*(?:async\s*)?\(`)
)

func extractSymbols(line string, lineNum int, path string) []Symbol {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
		return nil
	}

	ext := filepath.Ext(path)
	var syms []Symbol

	switch ext {
	case ".go":
		syms = extractGoSymbols(trimmed, lineNum, path)
	case ".ts", ".tsx", ".js", ".jsx":
		syms = extractTSSymbols(trimmed, lineNum, path)
	}

	return syms
}

func extractGoSymbols(line string, lineNum int, path string) []Symbol {
	var syms []Symbol

	if m := goStructRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolStruct, FilePath: path, Line: lineNum})
		return syms
	}
	if m := goIntfRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolInterface, FilePath: path, Line: lineNum})
		return syms
	}
	if m := goTypeRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolType, FilePath: path, Line: lineNum})
		return syms
	}
	if m := goFuncRe.FindStringSubmatch(line); len(m) > 1 {
		kind := SymbolFunc
		receiver := ""
		if idx := strings.Index(line, "func ("); idx >= 0 {
			afterParen := line[idx+6:]
			closeParen := strings.Index(afterParen, ")")
			if closeParen > 0 {
				recv := strings.TrimSpace(afterParen[:closeParen])
				if starIdx := strings.Index(recv, "*"); starIdx >= 0 {
					receiver = strings.TrimSpace(recv[starIdx+1:])
				} else {
					receiver = recv
				}
				if idx2 := strings.LastIndex(recv, " "); idx2 >= 0 {
					receiver = strings.TrimSpace(recv[idx2+1:])
				}
				if receiver != "" {
					kind = SymbolMethod
				}
			}
		}
		syms = append(syms, Symbol{Name: m[1], Kind: kind, FilePath: path, Line: lineNum, Receiver: receiver})
		return syms
	}
	if m := goVarRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolVar, FilePath: path, Line: lineNum})
		return syms
	}

	return syms
}

func extractTSSymbols(line string, lineNum int, path string) []Symbol {
	var syms []Symbol

	if m := tsFuncRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolFunc, FilePath: path, Line: lineNum})
		return syms
	}
	if m := tsClassRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolStruct, FilePath: path, Line: lineNum})
		return syms
	}
	if m := tsIntfRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolInterface, FilePath: path, Line: lineNum})
		return syms
	}
	if m := tsArrowRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolFunc, FilePath: path, Line: lineNum})
		return syms
	}
	if m := tsVarRe.FindStringSubmatch(line); len(m) > 1 {
		syms = append(syms, Symbol{Name: m[1], Kind: SymbolVar, FilePath: path, Line: lineNum})
		return syms
	}

	return syms
}

// Search finds symbols by name (substring match).
func (g *Graph) Search(name string) []Symbol {
	g.mu.RLock()
	defer g.mu.RUnlock()
	q := strings.ToLower(name)
	var out []Symbol
	for _, s := range g.symbols {
		if strings.Contains(strings.ToLower(s.Name), q) {
			out = append(out, s)
		}
	}
	return out
}

// SymbolsInFile returns all symbols defined in the given file.
func (g *Graph) SymbolsInFile(path string) []Symbol {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.byFile[path]
}

// AllSymbols returns every indexed symbol.
func (g *Graph) AllSymbols() []Symbol {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Symbol, len(g.symbols))
	copy(out, g.symbols)
	return out
}

// Count returns the total number of indexed symbols.
func (g *Graph) Count() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.symbols)
}
