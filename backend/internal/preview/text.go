package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

var (
	ErrBinary   = errors.New("file appears to be binary")
	ErrDocx     = errors.New("docx preview failed")
	ErrDocxHuge = errors.New("docx exceeds size limit")
)

// TextLimits bound untrusted text and Office text extraction.
type TextLimits struct {
	MaxTextBytes     int64 // max UTF-8 bytes returned (truncate beyond)
	MaxDocxBytes     int64 // max encoded .docx zip size
	MaxDocxXMLBytes  int64 // max uncompressed word/document.xml
	BinaryNULMaxRatio float64 // reject when NUL ratio in sample exceeds this
}

func defaultTextLimits() TextLimits {
	return TextLimits{
		MaxTextBytes:      512 << 10, // 512 KiB
		MaxDocxBytes:      8 << 20,   // 8 MiB zip
		MaxDocxXMLBytes:   2 << 20,   // 2 MiB document.xml
		BinaryNULMaxRatio: 0,         // any NUL → binary (fail closed)
	}
}

func (l TextLimits) withDefaults() TextLimits {
	d := defaultTextLimits()
	if l.MaxTextBytes <= 0 {
		l.MaxTextBytes = d.MaxTextBytes
	}
	if l.MaxDocxBytes <= 0 {
		l.MaxDocxBytes = d.MaxDocxBytes
	}
	if l.MaxDocxXMLBytes <= 0 {
		l.MaxDocxXMLBytes = d.MaxDocxXMLBytes
	}
	if l.BinaryNULMaxRatio < 0 {
		l.BinaryNULMaxRatio = d.BinaryNULMaxRatio
	}
	return l
}

// TextPreview is a bounded UTF-8 preview of a text-like or docx file.
type TextPreview struct {
	Kind      string `json:"kind"` // text | markdown | json | code | docx
	Language  string `json:"language,omitempty"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	BytesRead int64  `json:"bytesRead"`
	FileSize  int64  `json:"fileSize"`
}

// ClassifyTextPath returns preview kind and language for text-like names.
// Empty kind means not a text-like path.
func ClassifyTextPath(relPath string) (kind, language string) {
	base := path.Base(relPath)
	lower := strings.ToLower(base)

	if isEnvExampleStyle(lower) {
		return "code", "env"
	}

	ext := strings.ToLower(path.Ext(lower))
	switch ext {
	case ".txt", ".log", ".csv":
		return "text", strings.TrimPrefix(ext, ".")
	case ".md", ".markdown":
		return "markdown", "markdown"
	case ".json":
		return "json", "json"
	case ".jsonc":
		return "json", "jsonc"
	case ".yaml", ".yml":
		return "code", "yaml"
	case ".toml":
		return "code", "toml"
	case ".xml":
		return "code", "xml"
	case ".html", ".htm":
		return "code", "html"
	case ".css":
		return "code", "css"
	case ".js":
		return "code", "javascript"
	case ".ts":
		return "code", "typescript"
	case ".tsx":
		return "code", "tsx"
	case ".jsx":
		return "code", "jsx"
	case ".go":
		return "code", "go"
	case ".py":
		return "code", "python"
	case ".rs":
		return "code", "rust"
	case ".sh":
		return "code", "shell"
	case ".docx":
		return "docx", "docx"
	default:
		return "", ""
	}
}

func isEnvExampleStyle(baseLower string) bool {
	if baseLower == ".env" || strings.HasPrefix(baseLower, ".env.") {
		return true
	}
	return false
}

// IsTextLike reports whether the path is eligible for text/docx preview.
func IsTextLike(relPath string) bool {
	kind, _ := ClassifyTextPath(relPath)
	return kind != ""
}

// IsMediaPreviewable reports whether the path uses the binary media preview path.
func IsMediaPreviewable(relPath string) bool {
	_, ok := ContentTypeForExt(path.Ext(relPath))
	return ok
}

// TextPreview reads a bounded UTF-8 (or docx-extracted) preview through the resolver.
func (s *Service) TextPreview(ctx context.Context, volumeID, rel string) (*TextPreview, error) {
	_ = ctx
	kind, language := ClassifyTextPath(rel)
	if kind == "" {
		return nil, ErrUnsupported
	}

	res, err := s.fs.OpenFile(volumeID, rel)
	if err != nil {
		return nil, err
	}
	defer res.Close()

	fileSize := res.Info.Size()
	tl := s.textLim

	if kind == "docx" {
		if fileSize > tl.MaxDocxBytes {
			return nil, ErrDocxHuge
		}
		if fileSize <= 0 {
			return nil, ErrDocx
		}
		text, err := extractDocxText(res.File, fileSize, tl)
		if err != nil {
			return nil, err
		}
		truncated := false
		if int64(len(text)) > tl.MaxTextBytes {
			text = truncateUTF8(text, int(tl.MaxTextBytes))
			truncated = true
		}
		return &TextPreview{
			Kind:      "docx",
			Language:  "docx",
			Text:      text,
			Truncated: truncated,
			BytesRead: int64(len(text)),
			FileSize:  fileSize,
		}, nil
	}

	// Reject absurd sizes before reading; still allow truncate of large text files.
	// Cap read to MaxTextBytes+1 to detect truncation without loading the whole file.
	limit := tl.MaxTextBytes + 1
	data, err := io.ReadAll(io.LimitReader(res.File, limit))
	if err != nil {
		return nil, err
	}
	if looksBinary(data, tl.BinaryNULMaxRatio) {
		return nil, ErrBinary
	}

	truncated := int64(len(data)) > tl.MaxTextBytes
	if truncated {
		data = data[:tl.MaxTextBytes]
		// Avoid cutting mid-rune.
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}

	text := string(bytes.ToValidUTF8(data, []byte("\uFFFD")))

	if kind == "json" {
		text = prettyJSONOrRaw(text)
	}

	return &TextPreview{
		Kind:      kind,
		Language:  language,
		Text:      text,
		Truncated: truncated,
		BytesRead: int64(len(data)),
		FileSize:  fileSize,
	}, nil
}

func prettyJSONOrRaw(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return raw
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return string(pretty)
}

func looksBinary(data []byte, maxNULRatio float64) bool {
	if len(data) == 0 {
		return false
	}
	var nuls int
	for _, b := range data {
		if b == 0 {
			nuls++
		}
	}
	if maxNULRatio <= 0 {
		return nuls > 0
	}
	return float64(nuls)/float64(len(data)) > maxNULRatio
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	b := []byte(s[:maxBytes])
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}
