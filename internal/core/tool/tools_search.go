package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// ============================================================================
// GrepTool — search text in files (regex-aware)
// ============================================================================

type GrepTool struct{}

func (t *GrepTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "grep",
		Description: "Search for a pattern in files. The pattern is a regular expression; plain text also works (special characters are escaped automatically when the pattern is not a valid regex). Returns matching lines with file paths and line numbers.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "The regex pattern (or plain text) to search for",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in",
				},
				"ignore_case": map[string]any{
					"type":        "boolean",
					"description": "Case-insensitive search (default false)",
				},
			},
			"required": []string{"pattern", "path"},
		},
	}
}

func (t *GrepTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	pattern, err := parseArg(args, "pattern")
	if err != nil {
		return nil, err
	}
	searchPath, err := parseArg(args, "path")
	if err != nil {
		// Default to current directory
		searchPath = "."
	}
	ignoreCase := parseBoolArgWithDefault(args, "ignore_case", false)

	// Compile the pattern as a regex. If it fails to compile, fall back to
	// plain substring search (the old behavior) so a model that passes
	// literal text like "fmt.Println(" keeps working.
	re, reErr := regexp.Compile(pattern)
	if reErr != nil {
		re = nil
	} else if ignoreCase {
		// Recompile with the case-insensitive flag — lowering only the text
		// would break uppercase classes in the pattern itself.
		if reIC, err := regexp.Compile("(?i)" + pattern); err == nil {
			re = reIC
		}
	}

	// Use Go-native grep (cross-platform, no external dependency)
	var results []string
	_ = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			// Skip common directories
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == "release" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Skip binary files
		if isBinaryFile(path) {
			return nil
		}

		// Read file and search
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			var matched bool
			if re != nil {
				matched = re.MatchString(line)
			} else if ignoreCase {
				matched = strings.Contains(strings.ToLower(line), strings.ToLower(pattern))
			} else {
				matched = strings.Contains(line, pattern)
			}
			if matched {
				results = append(results, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})

	if len(results) == 0 {
		return &types.ToolResult{Success: true, Content: "No matches found."}, nil
	}

	// Limit results
	if len(results) > 100 {
		results = results[:100]
		results = append(results, fmt.Sprintf("\n... (%d more matches truncated)", len(results)-100))
	}

	return &types.ToolResult{Success: true, Content: strings.Join(results, "\n")}, nil
}

func isBinaryFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	binaryExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".bmp": true, ".ico": true, ".woff": true, ".woff2": true,
		".ttf": true, ".eot": true, ".zip": true, ".gz": true,
		".tar": true, ".7z": true, ".rar": true, ".pdf": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true,
	}
	return binaryExts[ext]
}

// ============================================================================
// GlobTool — find files by pattern
// ============================================================================

type GlobTool struct{}

func (t *GlobTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "glob",
		Description: "Find files matching a glob pattern (e.g., '**/*.go', 'src/*.ts').",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern to match files against",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GlobTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	pattern, err := parseArg(args, "pattern")
	if err != nil {
		return nil, err
	}

	// filepath.Glob does not support "**" recursion, but the schema advertises
	// '**/*.go' examples. Convert a leading "**/" into a recursive walk and
	// fall back to plain filepath.Glob for simple patterns.
	var matches []string
	if strings.Contains(pattern, "**") {
		matches = globDoubleStar(pattern)
	} else {
		m, err := filepath.Glob(pattern)
		if err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, nil
		}
		matches = m
	}

	var sb strings.Builder
	for _, m := range matches {
		sb.WriteString(m)
		sb.WriteString("\n")
	}
	// Read-before-edit guard: glob results reveal files the model has now
	// "seen" (spec: glob results count toward the tracked set).
	markFileReads(SessionIDFromContext(ctx), matches)
	return &types.ToolResult{Success: true, Content: sb.String()}, nil
}

// globDoubleStar implements the "**" recursion that filepath.Glob lacks.
// It walks from the fixed prefix of the pattern and matches each remaining
// segment against the relative paths at every depth, so '**/*.go' matches
// *.go at any depth and 'src/**/x.go' matches x.go under src recursively.
func globDoubleStar(pattern string) []string {
	// Split into the literal root and the recursive tail.
	idx := strings.Index(pattern, "**")
	root := strings.TrimSuffix(pattern[:idx], string(filepath.Separator))
	if root == "" {
		root = "."
	}
	tail := strings.TrimPrefix(pattern[idx+2:], string(filepath.Separator))
	tail = strings.TrimPrefix(tail, "/")

	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if tail == "" {
			out = append(out, path)
			return nil
		}
		// Match the tail against every suffix of rel so '**/x.go' also
		// matches x.go at depth 0 below root.
		ok := false
		parts := strings.Split(rel, string(filepath.Separator))
		for i := range parts {
			cand := strings.Join(parts[i:], string(filepath.Separator))
			if m, err := filepath.Match(tail, cand); err == nil && m {
				ok = true
				break
			}
		}
		if ok {
			out = append(out, path)
		}
		return nil
	})
	return out
}
