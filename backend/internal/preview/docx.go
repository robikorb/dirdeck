package preview

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// extractDocxText pulls plain text from word/document.xml inside a .docx zip.
// Fail closed on corrupt archives, missing document part, or oversized XML.
func extractDocxText(r io.ReaderAt, size int64, lim TextLimits) (string, error) {
	if size <= 0 || size > lim.MaxDocxBytes {
		return "", ErrDocxHuge
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", ErrDocx
	}

	var docFile *zip.File
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", ErrDocx
	}
	if docFile.UncompressedSize64 > uint64(lim.MaxDocxXMLBytes) {
		return "", ErrDocxHuge
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", ErrDocx
	}
	defer rc.Close()

	limited := io.LimitReader(rc, lim.MaxDocxXMLBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", ErrDocx
	}
	if int64(len(data)) > lim.MaxDocxXMLBytes {
		return "", ErrDocxHuge
	}

	text, err := xmlPlainText(data)
	if err != nil {
		return "", ErrDocx
	}
	return text, nil
}

func xmlPlainText(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false

	var b strings.Builder
	var inText bool
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch local(t.Name) {
			case "t":
				inText = true
			case "tab":
				b.WriteByte('\t')
			case "br", "cr":
				b.WriteByte('\n')
			}
		case xml.EndElement:
			switch local(t.Name) {
			case "t":
				inText = false
			case "p":
				b.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func local(n xml.Name) string {
	if n.Local != "" {
		return n.Local
	}
	return n.Space
}
