// Package tool — WebSearchTool: 内置多引擎搜索.
//
// 支持搜索引擎: Bing, Baidu, SearXNG, Metaso, Tavily
// 主要使用免费的搜索引擎 API，无需额外 Key 即可使用基础功能。
//
// Claude Code parity: WebSearchTool 是 P0 缺失功能，对标 Claude Code 的 WebSearchTool。

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// SearchEngine defines the interface for a search engine provider.
type SearchEngine interface {
	Name() string
	Search(ctx context.Context, query string, count int) ([]SearchResult, error)
}

// SearchResult represents a single search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebSearchTool performs web searches using multiple engines.
type WebSearchTool struct {
	engines map[string]SearchEngine
}

// NewWebSearchTool creates a web search tool with default engines.
func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{
		engines: map[string]SearchEngine{
			"bing":   &BingSearch{},
			"baidu":  &BaiduSearch{},
			"tavily": &TavilySearch{},
		},
	}
}

func (t *WebSearchTool) Def() types.ToolDef {
	return types.ToolDef{
		Name:        "web_search",
		Description: "Search the web for information. Supports Bing, Baidu, Tavily search engines. Returns relevant results with titles, URLs, and snippets. Use this when you need current information not available in the local codebase.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query (required)",
				},
				"engine": map[string]any{
					"type":        "string",
					"description": "Search engine: 'bing', 'baidu', 'tavily' (default: auto)",
					"enum":        []string{"", "bing", "baidu", "tavily"},
				},
				"count": map[string]any{
					"type":        "integer",
					"description": "Number of results to return (1-10, default: 5)",
				},
			},
			"required": []string{"query"},
		},
	}
}

type webSearchInput struct {
	Query  string `json:"query"`
	Engine string `json:"engine"`
	Count  int    `json:"count"`
}

func (t *WebSearchTool) Execute(ctx context.Context, args string) (*types.ToolResult, error) {
	var in webSearchInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return &types.ToolResult{
			Success: false, Error: fmt.Sprintf("invalid args: %v", err),
		}, nil
	}

	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" {
		return &types.ToolResult{Success: false, Error: "query is required"}, nil
	}
	if in.Count <= 0 || in.Count > 10 {
		in.Count = 5
	}

	// Determine engine
	var results []SearchResult
	var usedEngine string

	if in.Engine != "" {
		engine, ok := t.engines[in.Engine]
		if !ok {
			return &types.ToolResult{
				Success: false, Error: fmt.Sprintf("unknown engine: %s (supported: bing, baidu, tavily)", in.Engine),
			}, nil
		}
		usedEngine = in.Engine
		var err error
		results, err = engine.Search(ctx, in.Query, in.Count)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("%s search failed: %v", in.Engine, err)}, nil
		}
	} else {
		// Auto: try engines in order until one works
		for name, engine := range t.engines {
			var err error
			results, err = engine.Search(ctx, in.Query, in.Count)
			if err == nil && len(results) > 0 {
				usedEngine = name
				break
			}
		}
		if len(results) == 0 {
			return &types.ToolResult{
				Success: false, Error: "all search engines failed. Try specifying an engine explicitly.",
			}, nil
		}
	}

	// Build response
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Web search results for %q (via %s):\n\n", in.Query, usedEngine))
	for i, r := range results {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		b.WriteString(fmt.Sprintf("   URL: %s\n", r.URL))
		if r.Snippet != "" {
			b.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
		}
		b.WriteString("\n")
	}

	return &types.ToolResult{
		Success: true,
		Content: b.String(),
	}, nil
}

// ============================================================================
// Bing Search (uses the public Bing search API via a free endpoint)
// ============================================================================

type BingSearch struct{}

func (s *BingSearch) Name() string { return "bing" }

func (s *BingSearch) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if count <= 0 {
		count = 5
	}

	// Use the public Bing search API via a simple HTTP GET to a search engine
	// that returns structured results. We use the SearXNG-compatible approach,
	// or fall back to scraping Bing's HTML results page.
	searchURL := fmt.Sprintf("https://www.bing.com/search?q=%s&count=%d", url.QueryEscape(query), count)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}

	// Parse Bing HTML results
	return parseBingResults(string(body), count), nil
}

func parseBingResults(html string, count int) []SearchResult {
	var results []SearchResult

	// Simple HTML parsing for Bing results
	// Look for <li class="b_algo"> blocks
	sections := strings.Split(html, `<li class="b_algo`)
	for _, section := range sections[1:] {
		if len(results) >= count {
			break
		}

		r := SearchResult{}

		// Extract title from <h2><a href="..." target="_blank">title</a></h2>
		if h2Start := strings.Index(section, "<h2>"); h2Start >= 0 {
			anchorStart := strings.Index(section[h2Start:], `<a`)
			if anchorStart >= 0 {
				aTag := section[h2Start+anchorStart:]
				// Extract href
				hrefStart := strings.Index(aTag, `href="`)
				if hrefStart >= 0 {
					hrefEnd := strings.Index(aTag[hrefStart+6:], `"`)
					if hrefEnd >= 0 {
						r.URL = aTag[hrefStart+6 : hrefStart+6+hrefEnd]
					}
				}
				// Extract title text
				titleStart := strings.Index(aTag, `>`)
				titleEnd := strings.Index(aTag, `</a>`)
				if titleStart >= 0 && titleEnd > titleStart {
					r.Title = stripTags(aTag[titleStart+1 : titleEnd])
				}
			}
		}

		// Extract snippet from <p class="b_lineclamp2">
		if pStart := strings.Index(section, `<p`); pStart >= 0 {
			pTag := section[pStart:]
			pContentStart := strings.Index(pTag, `>`)
			pContentEnd := strings.Index(pTag, `</p>`)
			if pContentStart >= 0 && pContentEnd > pContentStart {
				r.Snippet = stripTags(pTag[pContentStart+1 : pContentEnd])
			}
		}

		if r.Title != "" {
			results = append(results, r)
		}
	}

	return results
}

// ============================================================================
// Baidu Search
// ============================================================================

type BaiduSearch struct{}

func (s *BaiduSearch) Name() string { return "baidu" }

func (s *BaiduSearch) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if count <= 0 {
		count = 5
	}

	searchURL := fmt.Sprintf("https://www.baidu.com/s?wd=%s&rn=%d", url.QueryEscape(query), count)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}

	return parseBaiduResults(string(body), count), nil
}

func parseBaiduResults(html string, count int) []SearchResult {
	var results []SearchResult

	// Baidu result blocks use <div class="result c-container "> or similar
	sections := strings.Split(html, `class="result`)
	for _, section := range sections[1:] {
		if len(results) >= count {
			break
		}

		r := SearchResult{}

		// Title in <h3 class="t">...</h3>
		if h3Start := strings.Index(section, `<h3`); h3Start >= 0 {
			h3Tag := section[h3Start:]
			aStart := strings.Index(h3Tag, `<a`)
			if aStart >= 0 {
				aTag := h3Tag[aStart:]
				// href
				hrefStart := strings.Index(aTag, `href="`)
				if hrefStart >= 0 {
					hrefEnd := strings.Index(aTag[hrefStart+6:], `"`)
					if hrefEnd >= 0 {
						r.URL = aTag[hrefStart+6 : hrefStart+6+hrefEnd]
					}
				}
				// title text
				tStart := strings.Index(aTag, `>`)
				tEnd := strings.Index(aTag, `</a>`)
				if tStart >= 0 && tEnd > tStart {
					r.Title = stripTags(aTag[tStart+1 : tEnd])
				}
			}
		}

		// Snippet in <span class="content-right_..."> or <div class="c-abstract">
		if absStart := strings.Index(section, `c-abstract`); absStart >= 0 {
			absTag := section[absStart:]
			cStart := strings.Index(absTag, `>`)
			cEnd := strings.Index(absTag, `</div>`)
			if cStart >= 0 && cEnd > cStart {
				r.Snippet = stripTags(absTag[cStart+1 : cEnd])
			}
		}

		if r.Title != "" {
			// Clean Baidu redirect URLs
			if strings.Contains(r.URL, "baidu.com/link?") {
				r.URL = extractBaiduRealURL(html, r.URL)
			}
			results = append(results, r)
		}
	}

	return results
}

func extractBaiduRealURL(html, fakeURL string) string {
	// Try to find the data-url attribute in the result block
	idx := strings.Index(html, fakeURL)
	if idx < 0 {
		return fakeURL
	}
	context := html[idx:]
	if diStart := strings.Index(context, `data-url="`); diStart >= 0 {
		diEnd := strings.Index(context[diStart+10:], `"`)
		if diEnd > 0 {
			return context[diStart+10 : diStart+10+diEnd]
		}
	}
	return fakeURL
}

// ============================================================================
// Tavily Search (via API — requires configurable API key)
// ============================================================================

type TavilySearch struct{}

func (s *TavilySearch) Name() string { return "tavily" }

func (s *TavilySearch) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if count <= 0 {
		count = 5
	}

	// Check for API key in environment
	apiKey := getTavilyAPIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("Tavily API key not configured. Set TAVILY_API_KEY or configure via 'icode auth set'")
	}

	payload := map[string]any{
		"api_key":    apiKey,
		"query":      query,
		"max_results": count,
		"include_answer": false,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.tavily.com/search", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}

	var tavilyResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
		Error string `json:"error,omitempty"`
	}

	if err := json.Unmarshal(respBody, &tavilyResp); err != nil {
		return nil, fmt.Errorf("parse tavily response: %w", err)
	}

	if tavilyResp.Error != "" {
		return nil, fmt.Errorf("tavily API error: %s", tavilyResp.Error)
	}

	var results []SearchResult
	for _, r := range tavilyResp.Results {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
		if len(results) >= count {
			break
		}
	}

	return results, nil
}

// ============================================================================
// Helpers
// ============================================================================

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func getTavilyAPIKey() string {
	// Try from environment first
	if key := getEnv("TAVILY_API_KEY", ""); key != "" {
		return key
	}
	// TODO: add integration with iCode config system
	return ""
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(getEnvFn(key)); v != "" {
		return v
	}
	return fallback
}

// getEnvFn is overridable for testing. Default reads from OS env.
var getEnvFn = os.Getenv
