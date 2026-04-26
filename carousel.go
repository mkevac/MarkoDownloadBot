package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultGalleryDLTimeout = 5 * time.Minute
	telegramMediaGroupMax   = 10
)

type CarouselItem struct {
	Path    string
	IsVideo bool
}

type Carousel struct {
	Items   []CarouselItem
	WorkDir string
}

func (c *Carousel) Cleanup() {
	if c == nil || c.WorkDir == "" {
		return
	}
	if err := os.RemoveAll(c.WorkDir); err != nil {
		log.Printf("carousel: error removing %s: %v", c.WorkDir, err)
	}
}

// isInstagramCarouselURL returns true for Instagram /p/<id>/ URLs, which can
// be either single-video posts or carousels mixing photos and videos. /reel/
// URLs are always single videos and stay on the existing yt-dlp path.
func isInstagramCarouselURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	if !strings.Contains(u.Host, "instagram.com") {
		return false
	}
	return strings.HasPrefix(u.Path, "/p/")
}

func galleryDLTimeout() time.Duration {
	return envDuration("GALLERY_DL_TIMEOUT_SECONDS", defaultGalleryDLTimeout)
}

// DownloadCarousel runs gallery-dl on the given URL, depositing every media
// item into a fresh subdirectory under tmpDir. It returns a Carousel with
// classified items (video vs photo). Caller must Cleanup() when done.
func DownloadCarousel(ctx context.Context, mediaURL, logTag, tmpDir, cookiesFile string) (*Carousel, error) {
	workDir := filepath.Join(tmpDir, "carousel-"+uuid.New().String())
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("creating carousel workdir: %w", err)
	}

	args := []string{
		"--quiet",
		"-d", workDir,
	}
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	}
	args = append(args, mediaURL)

	log.Printf("[%s]: running gallery-dl on %s", logTag, mediaURL)

	cctx, cancel := context.WithTimeout(ctx, galleryDLTimeout())
	defer cancel()

	cmd := exec.CommandContext(cctx, "gallery-dl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(workDir)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if cctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("gallery-dl timed out after %s", galleryDLTimeout())
		}
		log.Printf("[%s]: gallery-dl output: %s", logTag, strings.TrimSpace(string(output)))
		return nil, fmt.Errorf("gallery-dl failed: %w", err)
	}

	items, err := collectCarouselItems(workDir)
	if err != nil {
		_ = os.RemoveAll(workDir)
		return nil, err
	}
	if len(items) == 0 {
		_ = os.RemoveAll(workDir)
		return nil, fmt.Errorf("gallery-dl found no media")
	}

	log.Printf("[%s]: gallery-dl downloaded %d items", logTag, len(items))
	return &Carousel{Items: items, WorkDir: workDir}, nil
}

// collectCarouselItems walks workDir and classifies every regular file by
// extension. Items are sorted by path so carousel order is preserved.
func collectCarouselItems(workDir string) ([]CarouselItem, error) {
	var items []CarouselItem
	err := filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		isVideo, ok := classifyMediaExt(filepath.Ext(path))
		if !ok {
			log.Printf("carousel: skipping unrecognized file %s", path)
			return nil
		}
		items = append(items, CarouselItem{Path: path, IsVideo: isVideo})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking carousel dir: %w", err)
	}
	return items, nil
}

// classifyMediaExt reports whether the extension belongs to a Telegram-friendly
// media type. Returns (isVideo, recognized).
func classifyMediaExt(ext string) (bool, bool) {
	switch strings.ToLower(ext) {
	case ".mp4", ".webm", ".mov", ".m4v":
		return true, true
	case ".jpg", ".jpeg", ".png", ".webp":
		return false, true
	default:
		return false, false
	}
}

// chunkCarousel splits items into groups of at most maxPerGroup so each group
// can be sent as a single Telegram media group (cap is 10 per the API).
func chunkCarousel(items []CarouselItem, maxPerGroup int) [][]CarouselItem {
	if maxPerGroup <= 0 {
		maxPerGroup = telegramMediaGroupMax
	}
	var chunks [][]CarouselItem
	for i := 0; i < len(items); i += maxPerGroup {
		end := i + maxPerGroup
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[i:end])
	}
	return chunks
}
