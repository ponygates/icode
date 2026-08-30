package slashui

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// shareHTMLTemplate is the self-contained single-file HTML export (/share html,
// OpenCode /share parity via the local-file route — no server, no upload, the
// user hosts it anywhere static). Inlined CSS + a tiny dependency-free
// Markdown renderer keep it fully offline.
const shareHTMLTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s — iCode 会话</title>
<style>
  :root { color-scheme: light; }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
         background: #f5f6f8; color: #1f2328; line-height: 1.7; padding: 24px 12px; }
  .wrap { max-width: 860px; margin: 0 auto; }
  header { background: #fff; border: 1px solid #e4e7eb; border-radius: 12px;
           padding: 18px 22px; margin-bottom: 18px; }
  header h1 { font-size: 17px; font-weight: 600; }
  header .meta { color: #6a737d; font-size: 12px; margin-top: 6px; }
  .msg { background: #fff; border: 1px solid #e4e7eb; border-radius: 12px;
         padding: 14px 18px; margin-bottom: 12px; }
  .msg .role { font-size: 11px; font-weight: 600; letter-spacing: .5px;
               margin-bottom: 8px; text-transform: uppercase; }
  .msg.user   { border-left: 3px solid #4c8dff; }
  .msg.user .role   { color: #4c8dff; }
  .msg.assistant { border-left: 3px solid #2da44e; }
  .msg.assistant .role { color: #2da44e; }
  .msg.system { border-left: 3px solid #95a0aa; background: #fafbfc; }
  .msg.system .role { color: #95a0aa; }
  .msg.tool { border-left: 3px solid #d29922; background: #fffdf5; }
  .msg.tool .role { color: #b08300; }
  .content { font-size: 14px; overflow-wrap: break-word; }
  .content p { margin: 6px 0; }
  .content h1, .content h2, .content h3 { margin: 14px 0 6px; font-size: 15px; }
  .content ul, .content ol { margin: 6px 0 6px 22px; }
  .content li { margin: 2px 0; }
  .content code { background: #eff1f3; border-radius: 4px; padding: 1px 5px;
                  font-family: Consolas, "Courier New", monospace; font-size: 12.5px; }
  .content pre { background: #1e222a; color: #e6e9ee; border-radius: 8px;
                 padding: 12px 14px; overflow-x: auto; margin: 10px 0; }
  .content pre code { background: none; color: inherit; padding: 0; font-size: 12.5px; }
  .content blockquote { border-left: 3px solid #d0d7de; color: #6a737d;
                        padding-left: 12px; margin: 8px 0; }
  .content table { border-collapse: collapse; margin: 10px 0; font-size: 13px; }
  .content th, .content td { border: 1px solid #d8dee4; padding: 6px 12px; }
  .content th { background: #f6f8fa; font-weight: 600; }
  footer { text-align: center; color: #9aa4ae; font-size: 11px; margin-top: 22px; }
</style>
</head>
<body>
<div class="wrap">
<header>
  <h1>%s</h1>
  <div class="meta">模型: %s · 导出: %s · 由 iCode 生成</div>
</header>
%s
<footer>自包含单文件 · 可直接发给任何人或托管到任意静态空间</footer>
</div>
<script>
// Tiny dependency-free Markdown renderer (headings, lists, code fences,
// inline code, bold/italic, links, blockquotes, hr).
(function () {
  function esc(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }
  function inline(s) {
    s = esc(s);
    var bt = String.fromCharCode(96); // backtick — cannot appear literally in a Go raw string
    var re = new RegExp(bt + "([^" + bt + "]+)" + bt, "g");
    s = s.replace(re, "<code>$1</code>");
    s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
    s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, "$1<em>$2</em>");
    s = s.replace(/\[([^\]]+)\]\((https?:[^)\s]+)\)/g,
      '<a href="$2" target="_blank" rel="noopener">$1</a>');
    return s;
  }
  function render(src) {
    var lines = src.split(/\r?\n/), out = [], i = 0;
    var fence = bt + bt + bt;
    while (i < lines.length) {
      var ln = lines[i];
      if (ln.indexOf(fence) === 0) {
        var buf = [];
        i++;
        while (i < lines.length && lines[i].indexOf(fence) !== 0) { buf.push(lines[i]); i++; }
        i++;
        out.push("<pre><code>" + esc(buf.join("\n")) + "</code></pre>");
        continue;
      }
      var m;
      if ((m = ln.match(/^(#{1,3})\s+(.*)/))) {
        var lv = m[1].length;
        out.push("<h" + lv + ">" + inline(m[2]) + "</h" + lv + ">");
      } else if (/^\s*[-*]\s+/.test(ln) || /^\s*\d+\.\s+/.test(ln)) {
        var ordered = /^\s*\d+\./.test(ln), items = [];
        while (i < lines.length && (/^\s*[-*]\s+/.test(lines[i]) || /^\s*\d+\.\s+/.test(lines[i]))) {
          items.push("<li>" + inline(lines[i].replace(/^\s*(?:[-*]|\d+\.)\s+/, "")) + "</li>");
          i++;
        }
        out.push((ordered ? "<ol>" : "<ul>") + items.join("") + (ordered ? "</ol>" : "</ul>"));
        continue;
      } else if (/^\s*>/.test(ln)) {
        var q = [];
        while (i < lines.length && (/^\s*[>]/.test(lines[i]))) { q.push(inline(lines[i].replace(/^\s*>\s?/, ""))); i++; }
        out.push("<blockquote>" + q.join("<br>") + "</blockquote>");
        continue;
      } else if (/^\s*(---+|\*\*\*+)\s*$/.test(ln)) {
        out.push("<hr>");
      } else if (ln.trim() === "") {
        // skip blank
      } else {
        var para = [];
        while (i < lines.length && lines[i].trim() !== "" && lines[i].indexOf(fence) !== 0 && !/^#{1,3}\s|^\s*[-*]\s|^\s*\d+\.\s|^\s*>/.test(lines[i])) {
          para.push(inline(lines[i])); i++;
        }
        out.push("<p>" + para.join("<br>") + "</p>");
        continue;
      }
      i++;
    }
    return out.join("\n");
  }
  document.querySelectorAll("[data-md]").forEach(function (el) {
    el.innerHTML = render(el.getAttribute("data-md") || "");
  });
})();
</script>
</body>
</html>
`

// shareHTMLMessage renders one conversation message as an HTML block.
func shareHTMLMessage(role, content string) string {
	cls := map[string]string{
		"user": "user", "assistant": "assistant", "system": "system", "tool": "tool",
	}[role]
	label := map[string]string{
		"user": "User", "assistant": "Assistant", "system": "System", "tool": "Tool",
	}[role]
	if cls == "" {
		cls, label = "system", role
	}
	// content rides in the data-md attribute (JSON-free escaping via html
	// attr escaping) so the inline renderer handles it; a <noscript> fallback
	// shows the raw text.
	return fmt.Sprintf(
		`<div class="msg %s"><div class="role">%s</div><div class="content" data-md="%s"><noscript><pre>%s</pre></noscript></div></div>`,
		cls, label, html.EscapeString(content), html.EscapeString(content))
}

// buildShareHTML assembles the full self-contained page for a conversation.
func buildShareHTML(title, model string, msgs []shareMsg) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(shareHTMLMessage(m.Role, m.Content))
		sb.WriteString("\n")
	}
	return fmt.Sprintf(shareHTMLTemplate,
		html.EscapeString(title), html.EscapeString(title),
		html.EscapeString(model), time.Now().Format("2006-01-02 15:04"), sb.String())
}

// shareMsg is one message for the HTML export.
type shareMsg struct {
	Role    string
	Content string
}
