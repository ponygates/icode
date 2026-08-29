package tool

import (
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	html := `<!DOCTYPE html>
<html><head><title>保险犹豫期退保规则</title>
<style>body { color: red; }</style>
<script>alert('xss');</script>
</head><body>
<h1>犹豫期退保</h1>
<p>投保人自签收保单之日起有 <b>15 天</b> 犹豫期。&nbsp;&amp;&nbsp;期间可无条件退保。</p>
<!-- secret comment -->
<ul><li>线下渠道</li><li>线上渠道</li></ul>
</body></html>`

	got := htmlToText(html, 10000)

	for _, want := range []string{"保险犹豫期退保规则", "犹豫期退保", "15 天", "可无条件退保", "线下渠道", "线上渠道"} {
		if !strings.Contains(got, want) {
			t.Errorf("htmlToText missing %q; got:\n%s", want, got)
		}
	}
	for _, bad := range []string{"alert", "color: red", "secret comment", "<p>", "&nbsp;", "&amp;"} {
		if strings.Contains(got, bad) {
			t.Errorf("htmlToText should not contain %q; got:\n%s", bad, got)
		}
	}
}

func TestHTMLToTextCapsAtMaxChars(t *testing.T) {
	html := "<html><body>" + strings.Repeat("<p>内容</p>", 500) + "</body></html>"
	got := htmlToText(html, 100)
	if len([]rune(got)) > 110 {
		t.Errorf("htmlToText exceeded cap: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("htmlToText should end with ellipsis when capped; got tail: %q", got[len(got)-10:])
	}
}

func TestIsHTML(t *testing.T) {
	cases := []struct {
		body, ct string
		want     bool
	}{
		{"<html><body>x</body></html>", "text/html; charset=utf-8", true},
		{"<!DOCTYPE html>...", "", true},
		{`{"a":1}`, "application/json", false},
		{"plain text", "text/plain", false},
		{"<html>", "application/octet-stream", true},
	}
	for _, c := range cases {
		if got := isHTML(c.body, c.ct); got != c.want {
			t.Errorf("isHTML(%q, %q) = %v, want %v", c.body, c.ct, got, c.want)
		}
	}
}
