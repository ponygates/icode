package tui

// share_html.go — /share: render the current conversation as ONE
// self-contained HTML file (Claude Code /share parity). No server, no
// upload: the file embeds its CSS and inline-styled syntax highlighting, so
// it can be dropped into a chat, an email attachment, or a browser tab as-is.

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// ── Markdown → HTML (lightweight, escape-first) ───────────────────

// mdCodeRe splits source on fenced code blocks, capturing the language tag.
var mdCodeRe = regexp.MustCompile("(?s)```([a-zA-Z0-9+#._-]*)[ \\t]*\\n(.*?)```")

// Inline patterns are applied AFTER escaping, so their literals are chosen
// to survive escaping unchanged (` ` ** * [ ] ( ) all do).
var (
	mdBoldRe   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	mdItalicRe = regexp.MustCompile(`\*([^*\n]+)\*`)
	mdCodeInRe = regexp.MustCompile("`([^`\n]+)`")
	mdLinkRe   = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
)

// shareSafeURL allows only web-safe schemes in markdown links — anything
// else (javascript:, data:, vbscript:…) is neutralised to "#".
func shareSafeURL(u string) string {
	low := strings.ToLower(u)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") ||
		strings.HasPrefix(low, "mailto:") {
		return u
	}
	return "#"
}

// mdInline escapes then decorates one line of prose: `code`, **bold**,
// *italic*, [text](url).
func mdInline(s string) string {
	s = html.EscapeString(s)
	s = mdCodeInRe.ReplaceAllString(s, "<code>$1</code>")
	s = mdBoldRe.ReplaceAllString(s, "<strong>$1</strong>")
	s = mdItalicRe.ReplaceAllString(s, "<em>$1</em>")
	s = mdLinkRe.ReplaceAllString(s, `<a href="${2}" target="_blank" rel="noopener noreferrer">$1</a>`)
	// Neutralise dangerous hrefs AFTER substitution (scheme allow-list).
	return shareFixHrefs(s)
}

var hrefRe = regexp.MustCompile(`href="([^"]*)"`)

// shareFixHrefs runs the scheme allow-list over every href in the string.
func shareFixHrefs(s string) string {
	return hrefRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := hrefRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		return `href="` + shareSafeURL(sub[1]) + `"`
	})
}

// highlightShareCode renders a fenced block with chroma into inline-styled
// HTML (github-dark). Unknown languages fall back to plain escaped <pre>.
func highlightShareCode(lang, code string) string {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Fallback
	}
	formatter := chromahtml.New(chromahtml.WithClasses(false))
	var buf bytes.Buffer
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "<pre>" + html.EscapeString(code) + "</pre>"
	}
	if err := formatter.Format(&buf, style, it); err != nil {
		return "<pre>" + html.EscapeString(code) + "</pre>"
	}
	return buf.String()
}

// shareTableRe matches a markdown pipe-table row.
var shareTableRe = regexp.MustCompile(`^\s*\|.+\|\s*$`)

// mdToHTML renders the subset of Markdown iCode conversations actually use:
// fenced code (chroma), headings, blockquotes, ordered/unordered lists, pipe
// tables, hr, paragraphs + inline marks. Everything is escaped before any
// markup is introduced.
func mdToHTML(src string) string {
	// Pull fenced code blocks out first so no other rule touches them.
	var codeBlocks []string
	stripped := mdCodeRe.ReplaceAllStringFunc(src, func(m string) string {
		sub := mdCodeRe.FindStringSubmatch(m)
		lang, code := "", ""
		if len(sub) >= 3 {
			lang, code = sub[1], sub[2]
		}
		codeBlocks = append(codeBlocks, highlightShareCode(lang, code))
		return fmt.Sprintf("\x00CODE%d\x00", len(codeBlocks)-1)
	})

	lines := strings.Split(stripped, "\n")
	var out strings.Builder
	var (
		para     []string
		listKind string // "" | "ul" | "ol"
		quote    []string
		table    []string
	)
	flushPara := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + mdInline(strings.Join(para, "<br>")) + "</p>\n")
			para = nil
		}
	}
	flushList := func() {
		if listKind == "" {
			return
		}
		out.WriteString("</" + listKind + ">\n")
		listKind = ""
	}
	flushQuote := func() {
		if len(quote) > 0 {
			out.WriteString("<blockquote>" + mdInline(strings.Join(quote, "<br>")) + "</blockquote>\n")
			quote = nil
		}
	}
	flushTable := func() {
		if len(table) == 0 {
			return
		}
		var tb strings.Builder
		tb.WriteString("<table>")
		for i, row := range table {
			cells := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
			tag := "td"
			// First row is the header; a following |---|---| separator row is
			// dropped rather than rendered.
			if i == 0 {
				tag = "th"
			} else if strings.HasPrefix(strings.TrimSpace(cells[0]), ":--") ||
				strings.HasPrefix(strings.TrimSpace(cells[0]), "--") && len(cells) >= 2 && strings.Contains(cells[1], "--") {
				continue
			}
			tb.WriteString("<tr>")
			for _, c := range cells {
				tb.WriteString("<" + tag + ">" + mdInline(strings.TrimSpace(c)) + "</" + tag + ">")
			}
			tb.WriteString("</tr>")
		}
		tb.WriteString("</table>\n")
		out.WriteString(tb.String())
		table = nil
	}
	flushAll := func() {
		flushPara()
		flushList()
		flushQuote()
		flushTable()
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)

		// Code-block placeholder on its own line.
		if m := regexp.MustCompile(`^\x00CODE(\d+)\x00$`).FindStringSubmatch(trimmed); m != nil {
			flushAll()
			idx := 0
			fmt.Sscanf(m[1], "%d", &idx)
			if idx >= 0 && idx < len(codeBlocks) {
				out.WriteString(codeBlocks[idx] + "\n")
			}
			continue
		}
		// A placeholder left inline inside prose.
		if strings.Contains(trimmed, "\x00CODE") {
			flushAll()
			re := regexp.MustCompile(`\x00CODE(\d+)\x00`)
			out.WriteString("<p>" + re.ReplaceAllStringFunc(mdInline(trimmed), func(s string) string {
				sub := re.FindStringSubmatch(s)
				idx := 0
				fmt.Sscanf(sub[1], "%d", &idx)
				if idx >= 0 && idx < len(codeBlocks) {
					return codeBlocks[idx]
				}
				return ""
			}) + "</p>\n")
			continue
		}

		switch {
		case trimmed == "":
			flushAll()
		case strings.HasPrefix(trimmed, "#"):
			flushAll()
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' && level < 6 {
				level++
			}
			text := strings.TrimSpace(trimmed[level:])
			fmt.Fprintf(&out, "<h%d>%s</h%d>\n", level, mdInline(text), level)
		case trimmed == "---" || trimmed == "***":
			flushAll()
			out.WriteString("<hr>\n")
		case strings.HasPrefix(trimmed, "> "):
			flushPara()
			flushList()
			flushTable()
			quote = append(quote, strings.TrimPrefix(trimmed, "> "))
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			flushPara()
			flushQuote()
			flushTable()
			if listKind != "ul" {
				flushList()
				listKind = "ul"
				out.WriteString("<ul>\n")
			}
			out.WriteString("<li>" + mdInline(strings.TrimSpace(trimmed[2:])) + "</li>\n")
		case regexp.MustCompile(`^\d+[.)] `).MatchString(trimmed):
			flushPara()
			flushQuote()
			flushTable()
			if listKind != "ol" {
				flushList()
				listKind = "ol"
				out.WriteString("<ol>\n")
			}
			out.WriteString("<li>" + mdInline(strings.TrimSpace(trimmed[strings.Index(trimmed, " ")+1:])) + "</li>\n")
		case shareTableRe.MatchString(trimmed):
			flushPara()
			flushList()
			flushQuote()
			table = append(table, trimmed)
		default:
			flushList()
			flushQuote()
			flushTable()
			para = append(para, trimmed)
		}
	}
	flushAll()

	// Restore any code placeholders still embedded (e.g. trailing block
	// without a trailing newline after it).
	res := out.String()
	re := regexp.MustCompile(`\x00CODE(\d+)\x00`)
	res = re.ReplaceAllStringFunc(res, func(s string) string {
		sub := re.FindStringSubmatch(s)
		idx := 0
		fmt.Sscanf(sub[1], "%d", &idx)
		if idx >= 0 && idx < len(codeBlocks) {
			return codeBlocks[idx]
		}
		return ""
	})
	return res
}

// ── Conversation → HTML ───────────────────────────────────────────

const shareCSS = `
:root{--bg:#191919;--panel:#232323;--ink:#e8e6e3;--dim:#9b9791;--line:#33302c;--accent:#d97757;--user:#2a2a2a;--code-bg:#161819}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.7 -apple-system,"Segoe UI",Roboto,"PingFang SC","Microsoft YaHei",sans-serif}
.wrap{max-width:760px;margin:0 auto;padding:48px 20px 80px}
header{border-bottom:1px solid var(--line);padding-bottom:20px;margin-bottom:32px}
.logo{font-weight:700;font-size:20px;color:#fff}
.logo span{color:var(--accent)}
h1{font-size:22px;margin:14px 0 6px;font-weight:650}
.meta{color:var(--dim);font-size:13px}
.msg{margin:20px 0}
.msg .role{font-size:12px;color:var(--dim);margin-bottom:6px;letter-spacing:.04em}
.msg.user .bubble{background:var(--user);border-radius:12px;padding:12px 16px;display:inline-block;max-width:100%;text-align:left}
.msg.user .bubble p{margin:0 0 .5em}
.msg.user .bubble p:last-child{margin-bottom:0}
.msg.ai p{margin:.4em 0}
.msg.ai h1,.msg.ai h2,.msg.ai h3,.msg.ai h4{margin:.9em 0 .35em;color:#fff}
code{font-family:ui-monospace,Consolas,"Cascadia Mono",monospace;font-size:13px;background:var(--code-bg);border-radius:4px;padding:1px 5px}
pre{background:var(--code-bg);border-radius:10px;padding:14px;overflow-x:auto;line-height:1.55}
pre code{background:none;padding:0}
.msg.user pre{background:#1c1c1c}
blockquote{border-left:3px solid var(--line);margin:.6em 0;padding:2px 14px;color:var(--dim)}
table{border-collapse:collapse;margin:.8em 0;font-size:14px}
th,td{border:1px solid var(--line);padding:6px 12px;text-align:left}
th{color:#fff;background:var(--panel)}
a{color:var(--accent)}
details{background:var(--panel);border:1px solid var(--line);border-radius:10px;margin:10px 0;padding:0}
details summary{cursor:pointer;padding:9px 14px;font-size:13px;color:var(--dim);user-select:none;list-style:none}
details summary::before{content:"▸ ";color:var(--dim)}
details[open] summary::before{content:"▾ "}
details summary:hover{color:var(--ink)}
details .body{padding:0 14px 12px;font-size:13px;color:var(--dim)}
details pre{margin:4px 0;background:var(--code-bg)}
details.think summary{color:#8b7cb8}
details.tool summary .tname{color:#4fa3a3;font-family:ui-monospace,Consolas,monospace}
.sys{color:var(--dim);font-size:13px;text-align:center;margin:18px 0}
.err{color:#e5534b;font-size:14px;border:1px solid #5c2a27;background:#241716;border-radius:8px;padding:10px 14px;margin:14px 0}
footer{border-top:1px solid var(--line);margin-top:48px;padding-top:16px;color:var(--dim);font-size:12px;text-align:center}
@media print{body{background:#fff;color:#111}pre,.msg.user .bubble,details{background:#f6f6f6}}
`

// shareTitle derives the page title from the first user message.
func shareTitle(msgs []Message) string {
	for _, m := range msgs {
		if m.Role == RoleUser {
			line := strings.TrimSpace(m.Content)
			if i := strings.IndexByte(line, '\n'); i >= 0 {
				line = line[:i]
			}
			r := []rune(line)
			if len(r) > 60 {
				r = r[:60]
			}
			if len(r) > 0 {
				return string(r)
			}
		}
	}
	return "iCode 会话"
}

// buildShareHTML renders the conversation into a single self-contained HTML
// document (style + highlighting inlined, zero external requests).
func (t *TUI) buildShareHTML(msgs []Message) string {
	var b strings.Builder
	title := shareTitle(msgs)
	nUser := 0
	for _, m := range msgs {
		if m.Role == RoleUser {
			nUser++
		}
	}

	b.WriteString("<!doctype html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + " · iCode</title>\n")
	b.WriteString("<style>" + shareCSS + "</style>\n</head>\n<body>\n<div class=\"wrap\">\n")
	b.WriteString("<header><div class=\"logo\">i<span>Code</span></div>\n")
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>\n")
	fmt.Fprintf(&b, "<div class=\"meta\">%s · %s · %d 条消息 · 本地生成，未上传任何服务器</div>\n",
		time.Now().Format("2006-01-02 15:04"), html.EscapeString(t.model), len(msgs))
	b.WriteString("</header>\n<main>\n")

	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			b.WriteString(`<div class="msg user"><div class="role">用户</div><div class="bubble">` + "\n")
			b.WriteString(mdToHTML(m.Content))
			b.WriteString("</div></div>\n")
		case RoleAssistant:
			b.WriteString(`<div class="msg ai"><div class="role">` + html.EscapeString(t.model) + `</div>` + "\n")
			b.WriteString(mdToHTML(m.Content))
			b.WriteString("</div>\n")
		case RoleThinking:
			b.WriteString(`<details class="think"><summary>✻ 思考过程</summary><div class="body">` + "\n")
			b.WriteString(mdToHTML(m.Content))
			b.WriteString("</div></details>\n")
		case RoleTool:
			args := strings.TrimSpace(m.ToolArgs)
			if args == "{}" {
				args = ""
			}
			if r := []rune(args); len(r) > 80 {
				args = string(r[:80]) + "…"
			}
			b.WriteString(`<details class="tool"><summary>⏺ <span class="tname">` +
				html.EscapeString(m.Tool) + `</span> ` + html.EscapeString(args) + `</summary><div class="body"><pre>` +
				html.EscapeString(m.Content) + "</pre></div></details>\n")
		case RoleSystem:
			b.WriteString(`<div class="sys">` + mdInline(m.Content) + "</div>\n")
		case RoleError:
			b.WriteString(`<div class="err">` + mdInline(m.Content) + "</div>\n")
		}
	}

	b.WriteString("</main>\n")
	fmt.Fprintf(&b, "<footer>Generated by <b>i</b>Code v%s · /share 本地导出</footer>\n", html.EscapeString(t.version))
	b.WriteString("</div>\n</body>\n</html>\n")
	return b.String()
}

// shareCommand implements /share: writes the conversation as a single
// self-contained HTML file under ~/.icode/shares/ and prints the path.
func (t *TUI) shareCommand(args []string) {
	t.mu.Lock()
	msgs := append([]Message{}, t.messages...)
	t.mu.Unlock()
	if len(msgs) == 0 {
		t.add(RoleError, "当前会话为空，没有可分享的内容。")
		return
	}

	dir, err := os.UserHomeDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, ".icode", "shares")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.add(RoleError, "创建分享目录失败: "+err.Error())
		return
	}
	name := fmt.Sprintf("icode-share-%s.html", time.Now().Format("20060102-150405"))
	if len(args) > 0 && strings.HasSuffix(args[0], ".html") {
		name = filepath.Base(args[0])
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(t.buildShareHTML(msgs)), 0o644); err != nil {
		t.add(RoleError, "生成分享文件失败: "+err.Error())
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📤 可分享副本（单文件 HTML，含样式与代码高亮）:\n  %s\n", path))
	if osc8Supported() {
		url := "file:///" + strings.ReplaceAll(filepath.ToSlash(path), " ", "%20")
		fmt.Fprintf(&sb, "  \x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\\n", url, path)
	}
	sb.WriteString("纯本地生成、未上传；可直接发送给同事或用浏览器打开（start 命令）。")
	t.add(RoleSystem, sb.String())
}
