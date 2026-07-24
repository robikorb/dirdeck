package preview_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/preview"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

func writeMinimalDocx(t *testing.T, path, bodyText string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>` + bodyText + `</w:t></w:r></w:p>
  </w:body>
</w:document>`,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTextPreviewHappyJSONPretty(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	raw := compactJSON(`{"b":2,"a":1}`)
	_ = os.WriteFile(filepath.Join(volPath, "data.json"), []byte(raw), 0o644)
	out, err := svc.TextPreview(context.Background(), id, "data.json")
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != "json" || out.Truncated {
		t.Fatalf("unexpected: %#v", out)
	}
	if !strings.Contains(out.Text, "\n") || !strings.Contains(out.Text, `"a"`) {
		t.Fatalf("expected pretty JSON, got %q", out.Text)
	}
}

func compactJSON(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", ""), "\n", "")
}

func TestTextPreviewInvalidJSONRaw(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	raw := `{"broken": `
	_ = os.WriteFile(filepath.Join(volPath, "bad.json"), []byte(raw), 0o644)
	out, err := svc.TextPreview(context.Background(), id, "bad.json")
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != raw {
		t.Fatalf("expected raw invalid JSON, got %q", out.Text)
	}
}

func TestTextPreviewTruncate(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	root := filepath.Dir(volPath)
	cfgPath := filepath.Join(root, "volumes.yaml")
	reg, err := volumes.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	lim := preview.TextLimits{MaxTextBytes: 32}
	svc = preview.NewWithTextLimits(appfs.New(reg), preview.Defaults(), lim)

	payload := strings.Repeat("abcdefghij", 10) // 100 bytes
	_ = os.WriteFile(filepath.Join(volPath, "big.txt"), []byte(payload), 0o644)
	out, err := svc.TextPreview(context.Background(), id, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Fatal("expected truncated")
	}
	if int64(len(out.Text)) > 32 {
		t.Fatalf("text too long: %d", len(out.Text))
	}
}

func TestTextPreviewBinaryReject(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	_ = os.WriteFile(filepath.Join(volPath, "bin.txt"), []byte("hello\x00world"), 0o644)
	_, err := svc.TextPreview(context.Background(), id, "bin.txt")
	if err != preview.ErrBinary {
		t.Fatalf("got %v", err)
	}
}

func TestTextPreviewPathEscape(t *testing.T) {
	svc, _, id := setupPreviewVol(t, true)
	_, err := svc.TextPreview(context.Background(), id, "../etc/passwd")
	if err == nil {
		t.Fatal("expected path error")
	}
}

func TestTextPreviewMarkdownKind(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	_ = os.WriteFile(filepath.Join(volPath, "notes.md"), []byte("# Hello\n\nWorld"), 0o644)
	out, err := svc.TextPreview(context.Background(), id, "notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != "markdown" {
		t.Fatalf("kind %s", out.Kind)
	}
}

func TestDocxHappyPath(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	writeMinimalDocx(t, filepath.Join(volPath, "sample.docx"), "Hello from docx")
	out, err := svc.TextPreview(context.Background(), id, "sample.docx")
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != "docx" {
		t.Fatalf("kind %s", out.Kind)
	}
	if !strings.Contains(out.Text, "Hello from docx") {
		t.Fatalf("text %q", out.Text)
	}
}

func TestDocxOversizedFailClosed(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	root := filepath.Dir(volPath)
	cfgPath := filepath.Join(root, "volumes.yaml")
	reg, _ := volumes.Load(cfgPath)
	lim := preview.TextLimits{MaxDocxBytes: 200}
	svc = preview.NewWithTextLimits(appfs.New(reg), preview.Defaults(), lim)

	writeMinimalDocx(t, filepath.Join(volPath, "big.docx"), strings.Repeat("x", 500))
	_, err := svc.TextPreview(context.Background(), id, "big.docx")
	if err != preview.ErrDocxHuge {
		t.Fatalf("got %v", err)
	}
}

func TestDocxCorruptFailClosed(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	_ = os.WriteFile(filepath.Join(volPath, "bad.docx"), []byte("not a zip"), 0o644)
	_, err := svc.TextPreview(context.Background(), id, "bad.docx")
	if err != preview.ErrDocx {
		t.Fatalf("got %v", err)
	}
}

func TestClassifyTextPath(t *testing.T) {
	cases := map[string]string{
		"a.txt":         "text",
		"a.md":          "markdown",
		"a.json":        "json",
		"a.ts":          "code",
		".env.example":  "code",
		"x.docx":        "docx",
		"photo.png":     "",
		"report.pdf":    "",
	}
	for name, want := range cases {
		got, _ := preview.ClassifyTextPath(name)
		if got != want {
			t.Fatalf("%s: got %q want %q", name, got, want)
		}
	}
}
