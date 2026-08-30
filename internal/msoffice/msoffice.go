// Package msoffice generates minimal-but-valid Office Open XML documents
// (.docx / .xlsx / .pptx) with nothing but the standard library. It is the
// zero-dependency baseline behind the docx-report / xlsx-sheet / pptx-deck
// skills: when pandoc / python-docx / openpyxl are missing, `icode msoffice`
// still produces a real file instead of telling the user to install things.
//
// The output is deliberately conservative (headings, paragraphs, lists,
// tables; sheets with inline strings; title+bullets slides) so Word, Excel,
// WPS and PowerPoint open it without a repair prompt.
package msoffice

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writer assembles a zip-based OOXML file.
type writer struct {
	buf bytes.Buffer
	zw  *zip.Writer
}

func newWriter() *writer {
	w := &writer{}
	w.zw = zip.NewWriter(&w.buf)
	return w
}

func (w *writer) add(name, body string) error {
	f, err := w.zw.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(body))
	return err
}

func (w *writer) save(path string) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := w.zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, w.buf.Bytes(), 0o644)
}

// xmlEscape escapes text for XML content and attribute positions.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;",
		"\"", "&quot;", "'", "&apos;",
		"\x00", "", "\r", "",
	)
	return r.Replace(s)
}

// contentTypesXML builds [Content_Types].xml from override entries.
func contentTypesXML(defaults, overrides [][2]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	for _, d := range defaults {
		b.WriteString(`<Default Extension="` + d[0] + `" ContentType="` + d[1] + `"/>`)
	}
	for _, o := range overrides {
		b.WriteString(`<Override PartName="` + o[0] + `" ContentType="` + o[1] + `"/>`)
	}
	b.WriteString(`</Types>`)
	return b.String()
}

// relsXML builds a .rels file from (id, type, target) triples.
func relsXML(entries [][3]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, e := range entries {
		b.WriteString(`<Relationship Id="` + e[0] + `" Type="` + e[1] + `" Target="` + e[2] + `"/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

// ============================================================================
// DOCX — Markdown-ish input: #/##/### headings, "- " bullets, paragraphs.
// ============================================================================

const nsW = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func docxParagraph(style, text string) string {
	pPr := ""
	if style != "" {
		pPr = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + pPr + `<w:r><w:t xml:space="preserve">` + xmlEscape(text) + `</w:t></w:r></w:p>`
}

// WriteDocx renders md-ish content into a .docx file at path.
func WriteDocx(path, content string) error {
	w := newWriter()
	must(w.add("[Content_Types].xml", contentTypesXML(
		[][2]string{{"rels", "application/vnd.openxmlformats-package.relationships+xml"}, {"xml", "application/xml"}},
		[][2]string{
			{"/word/document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"},
			{"/word/styles.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"},
		})))
	must(w.add("_rels/.rels", relsXML([][3]string{
		{"rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument", "word/document.xml"},
	})))
	must(w.add("word/_rels/document.xml.rels", relsXML([][3]string{
		{"rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles", "styles.xml"},
	})))

	var body strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		switch {
		case strings.HasPrefix(t, "### "):
			body.WriteString(docxParagraph("Heading3", strings.TrimPrefix(t, "### ")))
		case strings.HasPrefix(t, "## "):
			body.WriteString(docxParagraph("Heading2", strings.TrimPrefix(t, "## ")))
		case strings.HasPrefix(t, "# "):
			body.WriteString(docxParagraph("Heading1", strings.TrimPrefix(t, "# ")))
		case strings.HasPrefix(t, "- "):
			body.WriteString(docxParagraph("ListParagraph", "• "+strings.TrimPrefix(t, "- ")))
		default:
			body.WriteString(docxParagraph("", t))
		}
	}
	if body.Len() == 0 {
		body.WriteString(docxParagraph("", ""))
	}
	must(w.add("word/document.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document `+nsW+`><w:body>`+
			body.String()+
			`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800"/></w:sectPr>`+
			`</w:body></w:document>`))

	// Minimal style sheet defining the heading/list styles referenced above.
	must(w.add("word/styles.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles `+nsW+`>`+
			docxStyle("Heading1", "heading 1", 32, true)+
			docxStyle("Heading2", "heading 2", 28, true)+
			docxStyle("Heading3", "heading 3", 24, true)+
			docxStyle("ListParagraph", "List Paragraph", 21, false)+
			`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:eastAsia="宋体" w:hAnsi="Calibri"/><w:sz w:val="21"/></w:rPr></w:rPrDefault></w:docDefaults>`+
			`</w:styles>`))
	return w.save(path)
}

func docxStyle(id, name string, halfPts int, bold bool) string {
	rPr := ""
	if bold {
		rPr = `<w:rPr><w:b/><w:sz w:val="` + fmt.Sprint(halfPts) + `"/></w:rPr>`
	} else {
		rPr = `<w:rPr><w:sz w:val="` + fmt.Sprint(halfPts) + `"/></w:rPr>`
	}
	return `<w:style w:type="paragraph" w:styleId="` + id + `"><w:name w:val="` + name + `"/>` + rPr + `</w:style>`
}

// ============================================================================
// XLSX — TSV/CSV-ish input: first line = header row; auto-detects \t or comma.
// ============================================================================

// WriteXlsx renders rows (first row = header) into an .xlsx file at path.
func WriteXlsx(path string, rows [][]string) error {
	if len(rows) == 0 {
		return fmt.Errorf("xlsx: no rows")
	}
	w := newWriter()
	must(w.add("[Content_Types].xml", contentTypesXML(
		[][2]string{{"rels", "application/vnd.openxmlformats-package.relationships+xml"}, {"xml", "application/xml"}},
		[][2]string{
			{"/xl/workbook.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"},
			{"/xl/worksheets/sheet1.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"},
		})))
	must(w.add("_rels/.rels", relsXML([][3]string{
		{"rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument", "xl/workbook.xml"},
	})))
	must(w.add("xl/_rels/workbook.xml.rels", relsXML([][3]string{
		{"rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet", "worksheets/sheet1.xml"},
	})))
	must(w.add("xl/workbook.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`))

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for r, row := range rows {
		b.WriteString(`<row r="` + fmt.Sprint(r+1) + `">`)
		for c, cell := range row {
			ref, err := cellRef(r, c)
			if err != nil {
				return err
			}
			// Inline strings keep the package simple (no sharedStrings part).
			b.WriteString(`<c r="` + ref + `" t="inlineStr"><is><t xml:space="preserve">` + xmlEscape(cell) + `</t></is></c>`)
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	must(w.add("xl/worksheets/sheet1.xml", b.String()))
	return w.save(path)
}

// cellRef converts 0-based row/col to an A1-style reference.
func cellRef(row, col int) (string, error) {
	if row < 0 || col < 0 || col > 701 {
		return "", fmt.Errorf("xlsx: cell index out of range r=%d c=%d", row, col)
	}
	ref := string(rune('A' + col%26))
	if col >= 26 {
		ref = string(rune('A'+col/26-1)) + ref
	}
	return fmt.Sprintf("%s%d", ref, row+1), nil
}

// ============================================================================
// PPTX — outline input: "# title" starts a slide; "- " lines are bullets;
// plain lines become body paragraphs of the current slide.
// ============================================================================

const nsP = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
const nsA = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
const nsR = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

type slide struct {
	title   string
	bullets []string
}

// WritePptx renders an outline into a .pptx file at path.
func WritePptx(path, content string) error {
	var slides []slide
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "# ") {
			slides = append(slides, slide{title: strings.TrimPrefix(t, "# ")})
			continue
		}
		if len(slides) == 0 {
			slides = append(slides, slide{title: "iCode"})
		}
		if strings.HasPrefix(t, "- ") {
			t = strings.TrimPrefix(t, "- ")
		}
		s := &slides[len(slides)-1]
		s.bullets = append(s.bullets, t)
	}
	if len(slides) == 0 {
		return fmt.Errorf("pptx: outline is empty")
	}

	w := newWriter()
	overrides := [][2]string{
		{"/ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"},
		{"/ppt/slideMasters/slideMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"},
		{"/ppt/slideLayouts/slideLayout1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"},
		{"/ppt/theme/theme1.xml", "application/vnd.openxmlformats-officedocument.theme+xml"},
		{"/ppt/presProps.xml", "application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"},
	}
	for i := range slides {
		overrides = append(overrides, [2]string{
			fmt.Sprintf("/ppt/slides/slide%d.xml", i+1),
			"application/vnd.openxmlformats-officedocument.presentationml.slide+xml",
		})
	}
	must(w.add("[Content_Types].xml", contentTypesXML(
		[][2]string{{"rels", "application/vnd.openxmlformats-package.relationships+xml"}, {"xml", "application/xml"}},
		overrides)))
	must(w.add("_rels/.rels", relsXML([][3]string{
		{"rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument", "ppt/presentation.xml"},
	})))

	// presentation.xml + its rels (slides + master + presProps).
	var sldIDs strings.Builder
	var presRels strings.Builder
	for i := range slides {
		sldIDs.WriteString(`<p:sldId id="` + fmt.Sprint(256+i) + `" r:id="rIdS` + fmt.Sprint(i+1) + `"/>`)
		presRels.WriteString(relEntry("rIdS"+fmt.Sprint(i+1), "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide", fmt.Sprintf("slides/slide%d.xml", i+1)))
	}
	presRels.WriteString(relEntry("rIdM1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster", "slideMasters/slideMaster1.xml"))
	presRels.WriteString(relEntry("rIdP1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/presProps", "presProps.xml"))
	must(w.add("ppt/_rels/presentation.xml.rels",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+presRels.String()+`</Relationships>`))
	must(w.add("ppt/presentation.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation `+nsP+` `+nsR+` `+nsA+`><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rIdM1"/></p:sldMasterIdLst><p:sldIdLst>`+sldIDs.String()+`</p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`))
	must(w.add("ppt/presProps.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentationPr `+nsP+`/>`))

	// One layout, referenced by the master; slides reference the layout.
	must(w.add("ppt/slideMasters/_rels/slideMaster1.xml.rels",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
			relEntry("rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout", "../slideLayouts/slideLayout1.xml")+
			relEntry("rId2", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme", "../theme/theme1.xml")+
			`</Relationships>`))
	must(w.add("ppt/slideLayouts/_rels/slideLayout1.xml.rels",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
			relEntry("rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster", "../slideMasters/slideMaster1.xml")+
			`</Relationships>`))
	for i := range slides {
		must(w.add(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1),
			`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
				relEntry("rId1", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout", "../slideLayouts/slideLayout1.xml")+
				`</Relationships>`))
	}

	masterBody := `<p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>` +
		pptShape(2, "Title Placeholder", "title",
			`<a:off x="838200" y="365125"/><a:ext cx="10515600" cy="1325563"/>`) +
		pptShape(3, "Body Placeholder", "body",
			`<a:off x="838200" y="1825625"/><a:ext cx="10515600" cy="4351338"/>`) +
		`</p:spTree></p:cSld><p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`
	must(w.add("ppt/slideMasters/slideMaster1.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster `+nsP+` `+nsA+` `+nsR+`>`+masterBody+`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst></p:sldMaster>`))
	must(w.add("ppt/slideLayouts/slideLayout1.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout `+nsP+` `+nsA+` `+nsR+` type="obj"><p:cSld name="Title and Content"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`))
	must(w.add("ppt/theme/theme1.xml",
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme `+nsA+` name="iCode"><a:themeElements>`+
			pptClrScheme()+
			`<a:fontScheme name="iCode"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont><a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>`+
			`<a:fmtScheme name="iCode"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme>`+
			`</a:themeElements></a:theme>`))

	for i, s := range slides {
		body := `<p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/>` +
			pptTextShape(2, s.title, "title", 4400, true) +
			pptBulletBody(3, s.bullets) +
			`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>`
		must(w.add(fmt.Sprintf("ppt/slides/slide%d.xml", i+1),
			`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld `+nsP+` `+nsA+` `+nsR+`>`+body+`</p:sld>`))
	}
	return w.save(path)
}

func relEntry(id, typ, target string) string {
	return `<Relationship Id="` + id + `" Type="` + typ + `" Target="` + target + `"/>`
}

func pptShape(id int, name, phType string, xfrm string) string {
	return `<p:sp><p:nvSpPr><p:cNvPr id="` + fmt.Sprint(id) + `" name="` + name + `"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="` + phType + `"/></p:nvPr></p:nvSpPr><p:spPr><a:xfrm>` + xfrm + `</a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p/></p:txBody></p:sp>`
}

func pptTextShape(id int, text, phType string, szHundredths int, bold bool) string {
	b := ""
	if bold {
		b = ` b="1"`
	}
	return `<p:sp><p:nvSpPr><p:cNvPr id="` + fmt.Sprint(id) + `" name="` + phType + `"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="` + phType + `"/></p:nvPr></p:nvSpPr><p:spPr><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="zh-CN" sz="` + fmt.Sprint(szHundredths) + `"` + b + `/><a:t>` + xmlEscape(text) + `</a:t></a:r></a:p></p:txBody></p:sp>`
}

func pptBulletBody(id int, bullets []string) string {
	var ps strings.Builder
	for _, b := range bullets {
		ps.WriteString(`<a:p><a:pPr><a:buChar char="•"/></a:pPr><a:r><a:rPr lang="zh-CN" sz="2000"/><a:t>` + xmlEscape(b) + `</a:t></a:r></a:p>`)
	}
	if bullets == nil {
		ps.WriteString(`<a:p/>`)
	}
	return `<p:sp><p:nvSpPr><p:cNvPr id="` + fmt.Sprint(id) + `" name="content"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/>` + ps.String() + `</p:txBody></p:sp>`
}

func pptClrScheme() string {
	scheme := func(name, hex string) string {
		return `<a:` + name + `><a:srgbClr val="` + hex + `"/></a:` + name + `>`
	}
	sys := func(name, last, hex string) string {
		return `<a:` + name + `><a:sysClr val="` + last + `" lastClr="` + hex + `"/></a:` + name + `>`
	}
	return `<a:clrScheme name="iCode">` +
		sys("dk1", "windowText", "000000") + sys("lt1", "window", "FFFFFF") +
		scheme("dk2", "44546A") + scheme("lt2", "E7E6E6") +
		scheme("accent1", "4472C4") + scheme("accent2", "ED7D31") +
		scheme("accent3", "A5A5A5") + scheme("accent4", "FFC000") +
		scheme("accent5", "5B9BD5") + scheme("accent6", "70AD47") +
		scheme("hlink", "0563C1") + scheme("folHlink", "954F72") +
		`</a:clrScheme>`
}

// must panics on internal template errors — all inputs here are static XML.
func must(err error) {
	if err != nil {
		panic(err)
	}
}
