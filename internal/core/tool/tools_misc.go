package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/netsec"
	"github.com/ponygates/icode/internal/types"
)

// ============================================================================
// LSTool — list directory contents
// ============================================================================

type LSTool struct{}

func (t *LSTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "ls",
		Description: "List files and directories at a given path.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path to list",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *LSTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	dir, err := parseArg(args, "path")
	if err != nil {
		dir = "."
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	var sb strings.Builder
	var seen []string
	for _, e := range entries {
		if e.IsDir() {
			sb.WriteString(fmt.Sprintf("DIR  %s\n", e.Name()))
		} else {
			info, _ := e.Info()
			sb.WriteString(fmt.Sprintf("FILE %-30s %d bytes\n", e.Name(), info.Size()))
			seen = append(seen, filepath.Join(dir, e.Name()))
		}
	}
	// Read-before-edit guard: listing a file counts as "seen" (see GlobTool).
	markFileReads(SessionIDFromContext(ctx), seen)
	return &types.ToolResult{Success: true, Content: sb.String()}, nil
}

// ============================================================================
// FetchTool — HTTP GET a URL
// ============================================================================

type FetchTool struct{}

func (t *FetchTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "fetch",
		Description: "Fetch content from a URL (HTTP GET).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (t *FetchTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	url, err := parseArg(args, "url")
	if err != nil {
		return nil, err
	}

	if err := validateFetchURL(url); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	client := netsec.GuardedClient(30*time.Second, true)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		// Re-validate every redirect hop so a public URL can't bounce us onto
		// loopback/private/metadata targets (SSRF via redirect). The guarded
		// transport covers the case where DNS answers change between hops.
		if err := validateFetchURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	}

	httpreq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	httpreq.Header.Set("User-Agent", "iCode/0.1.0")

	resp, err := client.Do(httpreq)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	maxSize := int64(256 * 1024) // 256KB limit
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}

	content := string(body)
	// HTML pages → readable plain text (Claude Code WebFetch parity): raw
	// markup would burn tokens and hurt comprehension. JSON/plain responses
	// stay untouched.
	if isHTML(content, resp.Header.Get("Content-Type")) {
		content = htmlToText(content, 12000)
	} else if int64(len(body)) >= maxSize {
		content += "\n\n[Response truncated at 256KB]"
	}

	return &types.ToolResult{
		Success: true,
		Content: content,
	}, nil
}

// validateFetchURL blocks SSRF targets before the fetch tool dials them. The
// policy itself lives in internal/netsec so vendor /models fetches and the MCP
// transports are checked against the same rules instead of each rolling their
// own (or none). Redirect hops are re-validated at the call site.
func validateFetchURL(raw string) error { return netsec.ValidatePublicURL(raw) }

// ============================================================================
// Helpers
// ============================================================================

func parseArg(rawJSON, key string) (string, error) {
	// Primary: strict JSON parsing (handles escaped quotes, nested content).
	if m, err := parseJSONArgs(rawJSON); err == nil {
		v, ok := m[key]
		if !ok {
			return "", fmt.Errorf("missing required argument: %s", key)
		}
		switch val := v.(type) {
		case string:
			return val, nil
		case bool:
			return fmt.Sprintf("%t", val), nil
		case float64:
			// Preserve integer-looking numbers without trailing .0
			if val == float64(int64(val)) {
				return fmt.Sprintf("%d", int64(val)), nil
			}
			return fmt.Sprintf("%v", val), nil
		default:
			return fmt.Sprintf("%v", val), nil
		}
	}

	// Fallback: lenient substring search for loosely-formatted arguments.
	raw := strings.TrimSpace(rawJSON)
	search := fmt.Sprintf(`"%s":`, key)
	idx := strings.Index(raw, search)
	if idx < 0 {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	start := idx + len(search)
	rest := strings.TrimSpace(raw[start:])
	if !strings.HasPrefix(rest, `"`) {
		return "", fmt.Errorf("argument %s must be a string", key)
	}
	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return rest[1:], nil
	}
	return rest[1 : end+1], nil
}

// parseStringArrayArg extracts a []string argument (e.g. the git_commit
// "files" list). Returns nil when the key is absent or not a JSON array.
func parseStringArrayArg(rawJSON, key string) []string {
	m, err := parseJSONArgs(rawJSON)
	if err != nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
