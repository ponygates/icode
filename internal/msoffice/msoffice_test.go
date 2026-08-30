package msoffice

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readZip opens a generated file and returns its entries.
func readZip(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		rc.Close()
		out[f.Name] = buf.String()
	}
	return out
}

// assertWellFormedXML parses every .xml entry.
func assertWellFormedXML(t *testing.T, entries map[string]string) {
	t.Helper()
	for name, body := range entries {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		if err := xml.Unmarshal([]byte(body), new(any)); err != nil {
			t.Errorf("%s: malformed XML: %v", name, err)
		}
	}
}

func TestWriteDocx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.docx")
	in := "# 报告标题\n\n## 第一章\n\n正文段落。\n\n- 要点一\n- 要点二\n"
	if err := WriteDocx(path, in); err != nil {
		t.Fatal(err)
	}
	entries := readZip(t, path)
	assertWellFormedXML(t, entries)
	for _, part := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml"} {
		if _, ok := entries[part]; !ok {
			t.Errorf("missing part %s", part)
		}
	}
	doc := entries["word/document.xml"]
	for _, want := range []string{"Heading1", "Heading2", "报告标题", "• 要点一"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml missing %q", want)
		}
	}
}

func TestWriteXlsx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.xlsx")
	rows := [][]string{{"姓名", "年龄"}, {"张三", "30"}, {"A&B <特殊>", "25"}}
	if err := WriteXlsx(path, rows); err != nil {
		t.Fatal(err)
	}
	entries := readZip(t, path)
	assertWellFormedXML(t, entries)
	sheet := entries["xl/worksheets/sheet1.xml"]
	if !strings.Contains(sheet, "张三") || !strings.Contains(sheet, "A&amp;B &lt;特殊&gt;") {
		t.Error("cell text missing or unescaped")
	}
	if !strings.Contains(sheet, `<c r="B2"`) {
		t.Error("A1 cell refs wrong")
	}
	if err := WriteXlsx(path, nil); err == nil {
		t.Error("empty rows should error")
	}
}

func TestCellRef(t *testing.T) {
	cases := map[[2]int]string{{0, 0}: "A1", {0, 25}: "Z1", {0, 26}: "AA1", {3, 27}: "AB4", {0, 701}: "ZZ1"}
	for in, want := range cases {
		got, err := cellRef(in[0], in[1])
		if err != nil || got != want {
			t.Errorf("cellRef(%v) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := cellRef(0, 702); err == nil {
		t.Error("col 702 should be out of range")
	}
}

func TestWritePptx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.pptx")
	in := "# 开场\n- 第一点\n- 第二点\n\n# 第二页\n正文内容\n"
	if err := WritePptx(path, in); err != nil {
		t.Fatal(err)
	}
	entries := readZip(t, path)
	assertWellFormedXML(t, entries)
	for _, part := range []string{
		"ppt/presentation.xml", "ppt/slides/slide1.xml", "ppt/slides/slide2.xml",
		"ppt/slideMasters/slideMaster1.xml", "ppt/slideLayouts/slideLayout1.xml",
		"ppt/theme/theme1.xml", "ppt/_rels/presentation.xml.rels",
	} {
		if _, ok := entries[part]; !ok {
			t.Errorf("missing part %s", part)
		}
	}
	if !strings.Contains(entries["ppt/slides/slide1.xml"], "第一点") {
		t.Error("slide1 bullets missing")
	}
	if !strings.Contains(entries["ppt/presentation.xml"], `r:id="rIdS2"`) {
		t.Error("slide2 not registered in presentation.xml")
	}
	// empty outline errors
	if err := WritePptx(path, ""); err == nil {
		t.Error("empty outline should error")
	}
}
