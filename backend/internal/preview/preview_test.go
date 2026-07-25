package preview_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
	"github.com/robikorb/dirdeck/backend/internal/preview"
	"github.com/robikorb/dirdeck/backend/internal/volumes"
)

func setupPreviewVol(t *testing.T, thumbnails bool) (*preview.Service, string, string) {
	t.Helper()
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	volPath := filepath.Join(root, "media")
	if err := os.MkdirAll(volPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "volumes.yaml")
	thumb := "false"
	if thumbnails {
		thumb = "true"
	}
	_ = os.WriteFile(cfgPath, []byte(`
volumes:
  - id: media
    name: Media
    path: `+volPath+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: `+thumb+`
`), 0o600)
	reg, err := volumes.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	svc := preview.New(appfs.New(reg), preview.Defaults())
	return svc, volPath, "media"
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 80, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailHappyPath(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	writePNG(t, filepath.Join(volPath, "ok.png"), 64, 48)
	out, err := svc.Thumbnail(context.Background(), id, "ok.png")
	if err != nil {
		t.Fatal(err)
	}
	if out.ContentType != "image/jpeg" || len(out.Bytes) < 50 {
		t.Fatalf("unexpected thumbnail: %#v len=%d", out.ContentType, len(out.Bytes))
	}
}

func TestThumbnailDisabled(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, false)
	writePNG(t, filepath.Join(volPath, "ok.png"), 16, 16)
	_, err := svc.Thumbnail(context.Background(), id, "ok.png")
	if err != preview.ErrDisabled {
		t.Fatalf("got %v", err)
	}
}

func TestThumbnailTooLargeBytes(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	// Override limits via new service with tiny MaxBytes.
	root := filepath.Dir(volPath)
	cfgPath := filepath.Join(root, "volumes.yaml")
	reg, err := volumes.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	lim := preview.Defaults()
	lim.MaxBytes = 200
	svc = preview.New(appfs.New(reg), lim)

	writePNG(t, filepath.Join(volPath, "big.png"), 120, 120) // PNG will exceed 200 bytes
	_, err = svc.Thumbnail(context.Background(), id, "big.png")
	if err != preview.ErrTooLarge {
		t.Fatalf("got %v", err)
	}
}

func TestThumbnailTooManyPixels(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	root := filepath.Dir(volPath)
	cfgPath := filepath.Join(root, "volumes.yaml")
	reg, _ := volumes.Load(cfgPath)
	lim := preview.Defaults()
	lim.MaxPixels = 100 // 10x10 = 100 ok; 20x20 = 400 fails
	svc = preview.New(appfs.New(reg), lim)

	writePNG(t, filepath.Join(volPath, "px.png"), 20, 20)
	_, err := svc.Thumbnail(context.Background(), id, "px.png")
	if err != preview.ErrTooManyPixels {
		t.Fatalf("got %v", err)
	}
}

func TestThumbnailUnsupported(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	_ = os.WriteFile(filepath.Join(volPath, "notes.txt"), []byte("hello"), 0o644)
	_, err := svc.Thumbnail(context.Background(), id, "notes.txt")
	if err != preview.ErrUnsupported {
		t.Fatalf("got %v", err)
	}
}

func TestThumbnailConcurrencyLimit(t *testing.T) {
	svc, volPath, id := setupPreviewVol(t, true)
	root := filepath.Dir(volPath)
	cfgPath := filepath.Join(root, "volumes.yaml")
	reg, _ := volumes.Load(cfgPath)
	lim := preview.Defaults()
	lim.MaxConcurrency = 1
	lim.MaxDecodeTime = 2 * time.Second
	svc = preview.New(appfs.New(reg), lim)
	writePNG(t, filepath.Join(volPath, "c.png"), 32, 32)

	started := make(chan struct{})
	var wg sync.WaitGroup
	var busyHits atomic.Int32

	// Hold the single slot with a slow decode by using a custom approach:
	// fire many concurrent requests; with MaxConcurrency=1 some should get ErrBusy.
	wg.Add(20)
	for i := 0; i < 20; i++ {
		go func() {
			defer wg.Done()
			<-started
			_, err := svc.Thumbnail(context.Background(), id, "c.png")
			if err == preview.ErrBusy {
				busyHits.Add(1)
			}
		}()
	}
	close(started)
	wg.Wait()
	if busyHits.Load() == 0 {
		t.Fatal("expected at least one ErrBusy under concurrency pressure")
	}
}

func TestThumbnailPathEscape(t *testing.T) {
	svc, _, id := setupPreviewVol(t, true)
	_, err := svc.Thumbnail(context.Background(), id, "../etc/passwd")
	if err == nil {
		t.Fatal("expected path error")
	}
}
