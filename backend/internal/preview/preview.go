// Package preview generates bounded image thumbnails and classifies previewable media.
// All inputs are treated as untrusted; limits fail closed.
package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"path"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder

	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
)

var (
	ErrDisabled      = errors.New("thumbnails disabled for volume")
	ErrUnsupported   = errors.New("unsupported image type")
	ErrTooLarge      = errors.New("image exceeds size limit")
	ErrTooManyPixels = errors.New("image exceeds pixel limit")
	ErrTimeout       = errors.New("image decode timed out")
	ErrBusy          = errors.New("thumbnail concurrency limit reached")
)

// Limits bound untrusted image decoding. Zero fields fall back to Defaults().
type Limits struct {
	MaxBytes       int64
	MaxPixels      int64
	MaxDecodeTime  time.Duration
	MaxConcurrency int
	ThumbMaxEdge   int
	JPEGQuality    int
}

// Defaults are conservative production limits for browser-facing thumbnails.
func Defaults() Limits {
	return Limits{
		MaxBytes:       16 << 20, // 16 MiB encoded
		MaxPixels:      40e6,    // 40 megapixels
		MaxDecodeTime:  5 * time.Second,
		MaxConcurrency: 2,
		ThumbMaxEdge:   256,
		JPEGQuality:    82,
	}
}

func (l Limits) withDefaults() Limits {
	d := Defaults()
	if l.MaxBytes <= 0 {
		l.MaxBytes = d.MaxBytes
	}
	if l.MaxPixels <= 0 {
		l.MaxPixels = d.MaxPixels
	}
	if l.MaxDecodeTime <= 0 {
		l.MaxDecodeTime = d.MaxDecodeTime
	}
	if l.MaxConcurrency <= 0 {
		l.MaxConcurrency = d.MaxConcurrency
	}
	if l.ThumbMaxEdge <= 0 {
		l.ThumbMaxEdge = d.ThumbMaxEdge
	}
	if l.JPEGQuality <= 0 || l.JPEGQuality > 100 {
		l.JPEGQuality = d.JPEGQuality
	}
	return l
}

// Service produces thumbnails and text/docx previews through the shared resolver.
type Service struct {
	fs      *appfs.Service
	lim     Limits
	textLim TextLimits
	sem     chan struct{}
}

// New creates a preview service with default text/docx limits.
func New(fsSvc *appfs.Service, lim Limits) *Service {
	return NewWithTextLimits(fsSvc, lim, TextLimits{})
}

// NewWithTextLimits creates a preview service with custom text/docx limits.
func NewWithTextLimits(fsSvc *appfs.Service, lim Limits, textLim TextLimits) *Service {
	lim = lim.withDefaults()
	textLim = textLim.withDefaults()
	return &Service{
		fs:      fsSvc,
		lim:     lim,
		textLim: textLim,
		sem:     make(chan struct{}, lim.MaxConcurrency),
	}
}

// Limits returns the effective image limits (for tests/docs).
func (s *Service) Limits() Limits {
	return s.lim
}

// TextLimits returns the effective text/docx limits (for tests/docs).
func (s *Service) TextLimits() TextLimits {
	return s.textLim
}

// ThumbnailResult is an encoded thumbnail.
type ThumbnailResult struct {
	ContentType string
	Bytes       []byte
}

// Thumbnail generates a JPEG thumbnail for an image under a volume.
// The volume must have thumbnails enabled. Failures return typed errors.
func (s *Service) Thumbnail(ctx context.Context, volumeID, rel string) (*ThumbnailResult, error) {
	vol, ok := s.fs.Volume(volumeID)
	if !ok {
		return nil, appfs.ErrNotFound
	}
	if !vol.Thumbnails {
		return nil, ErrDisabled
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return nil, ErrBusy
	}

	res, err := s.fs.OpenFile(volumeID, rel)
	if err != nil {
		return nil, err
	}
	defer res.Close()

	if res.Info.Size() > s.lim.MaxBytes {
		return nil, ErrTooLarge
	}
	if res.Info.Size() <= 0 {
		return nil, ErrUnsupported
	}

	ext := strings.ToLower(path.Ext(res.RelPath))
	if !IsImageExt(ext) {
		return nil, ErrUnsupported
	}

	deadline := s.lim.MaxDecodeTime
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	type result struct {
		out *ThumbnailResult
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := s.decodeAndThumb(res.File, res.Info.Size())
		ch <- result{out, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ErrTimeout
	case r := <-ch:
		return r.out, r.err
	}
}

func (s *Service) decodeAndThumb(r io.Reader, sizeHint int64) (*ThumbnailResult, error) {
	limited := io.LimitReader(r, s.lim.MaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > s.lim.MaxBytes {
		return nil, ErrTooLarge
	}
	if sizeHint > 0 && int64(len(data)) == 0 {
		return nil, ErrUnsupported
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnsupported
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrUnsupported
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if pixels > s.lim.MaxPixels {
		return nil, ErrTooManyPixels
	}
	// Guard against overflow / absurd configs before full decode.
	if cfg.Width > 1<<15 || cfg.Height > 1<<15 {
		return nil, ErrTooManyPixels
	}

	img, format2, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnsupported
	}
	if format2 != "" {
		format = format2
	}
	_ = format

	thumb := resizeToMaxEdge(img, s.lim.ThumbMaxEdge)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: s.lim.JPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode thumbnail: %w", err)
	}
	return &ThumbnailResult{
		ContentType: "image/jpeg",
		Bytes:       buf.Bytes(),
	}, nil
}

func resizeToMaxEdge(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return src
	}
	scale := float64(maxEdge) / float64(w)
	if h > w {
		scale = float64(maxEdge) / float64(h)
	}
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// IsImageExt reports whether an extension is a supported raster image.
func IsImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	default:
		return false
	}
}

// ContentTypeForExt returns a browser content type for previewable media.
func ContentTypeForExt(ext string) (string, bool) {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".png":
		return "image/png", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".mp4":
		return "video/mp4", true
	case ".webm":
		return "video/webm", true
	case ".mp3":
		return "audio/mpeg", true
	case ".wav":
		return "audio/wav", true
	case ".ogg":
		return "audio/ogg", true
	default:
		return "", false
	}
}

// IsPreviewable reports whether the path is suitable for the preview endpoint
// (media stream or text/docx JSON preview).
func IsPreviewable(relPath string) bool {
	if IsMediaPreviewable(relPath) {
		return true
	}
	return IsTextLike(relPath)
}

// Ensure decoders are linked (jpeg/png/gif are stdlib; webp via blank import).
var _ = []any{jpeg.Encode, png.Encode, gif.Decode}
