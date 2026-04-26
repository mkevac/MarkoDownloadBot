package main

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestIsInstagramCarouselURL(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://www.instagram.com/p/DWeuKatiKmT/", true},
		{"https://instagram.com/p/abc/", true},
		{"https://www.instagram.com/p/abc/?igsh=foo", true},
		{"https://www.instagram.com/reel/abc/", false},
		{"https://www.instagram.com/stories/user/123", false},
		{"https://www.instagram.com/", false},
		{"https://www.youtube.com/watch?v=abc", false},
		{"https://www.tiktok.com/@user/video/123", false},
	}
	for _, tt := range cases {
		u, err := url.Parse(tt.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.raw, err)
		}
		if got := isInstagramCarouselURL(u); got != tt.want {
			t.Errorf("isInstagramCarouselURL(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
	if got := isInstagramCarouselURL(nil); got != false {
		t.Errorf("isInstagramCarouselURL(nil) = %v, want false", got)
	}
}

func TestClassifyMediaExt(t *testing.T) {
	cases := []struct {
		ext     string
		isVideo bool
		ok      bool
	}{
		{".mp4", true, true},
		{".MP4", true, true},
		{".webm", true, true},
		{".mov", true, true},
		{".m4v", true, true},
		{".jpg", false, true},
		{".JPEG", false, true},
		{".png", false, true},
		{".webp", false, true},
		{".heic", false, false}, // gallery-dl renames .heic→.jpg server-side; raw .heic stays unsupported by Telegram
		{".gif", false, false},
		{".txt", false, false},
		{"", false, false},
	}
	for _, tt := range cases {
		isVideo, ok := classifyMediaExt(tt.ext)
		if ok != tt.ok || isVideo != tt.isVideo {
			t.Errorf("classifyMediaExt(%q) = (%v, %v), want (%v, %v)", tt.ext, isVideo, ok, tt.isVideo, tt.ok)
		}
	}
}

func TestChunkCarousel(t *testing.T) {
	mk := func(n int) []CarouselItem {
		out := make([]CarouselItem, n)
		for i := range out {
			out[i] = CarouselItem{Path: "x"}
		}
		return out
	}

	cases := []struct {
		name        string
		n, max      int
		wantNGroups int
		wantSizes   []int
	}{
		{"empty", 0, 10, 0, nil},
		{"under cap", 5, 10, 1, []int{5}},
		{"exactly cap", 10, 10, 1, []int{10}},
		{"one over", 11, 10, 2, []int{10, 1}},
		{"multiple groups", 23, 10, 3, []int{10, 10, 3}},
		{"max=0 falls back to default", 11, 0, 2, []int{10, 1}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkCarousel(mk(tt.n), tt.max)
			if len(chunks) != tt.wantNGroups {
				t.Fatalf("got %d groups, want %d", len(chunks), tt.wantNGroups)
			}
			for i, want := range tt.wantSizes {
				if len(chunks[i]) != want {
					t.Errorf("group %d: size %d, want %d", i, len(chunks[i]), want)
				}
			}
		})
	}
}

func TestCollectCarouselItemsClassifiesAndSkips(t *testing.T) {
	dir := t.TempDir()
	must := func(name string) {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	// Telegram-friendly media.
	must("01.mp4")
	must("02.jpg")
	must("03.png")
	// Should be skipped.
	must("notes.txt")
	must("old.heic")
	// Nested folder (gallery-dl creates extractor/user/ subdirs).
	sub := filepath.Join(dir, "instagram", "samuelcole90")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Create(filepath.Join(sub, "04.jpeg")); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}

	items, err := collectCarouselItems(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4: %+v", len(items), items)
	}

	videos, photos := 0, 0
	for _, it := range items {
		if it.IsVideo {
			videos++
		} else {
			photos++
		}
	}
	if videos != 1 || photos != 3 {
		t.Errorf("videos=%d photos=%d, want 1/3", videos, photos)
	}
}
