package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestFirstYTDLPJSONLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "single json",
			in:   `{"id":"abc"}`,
			want: `{"id":"abc"}`,
		},
		{
			name: "json with leading spaces and trailing newline",
			in:   "  {\"id\":\"abc\"}\n",
			want: `{"id":"abc"}`,
		},
		{
			name: "carousel: first json then error lines",
			in: `{"id":"vid1","playlist_index":1}
ERROR: [Instagram] DWeu: No video formats found!
ERROR: [Instagram] DWeu: No video formats found!`,
			want: `{"id":"vid1","playlist_index":1}`,
		},
		{
			name: "errors first then json (defensive)",
			in: `ERROR: something
{"id":"vid1"}`,
			want: `{"id":"vid1"}`,
		},
		{
			name: "no json, only errors",
			in:   "ERROR: foo\nERROR: bar\n",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstYTDLPJSONLine(tt.in)
			if got != tt.want {
				t.Errorf("firstYTDLPJSONLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsCarouselSite(t *testing.T) {
	cases := map[string]bool{
		"instagram.com":     true,
		"www.instagram.com": true,
		"youtube.com":       false,
		"www.youtube.com":   false,
		"tiktok.com":        false,
		"":                  false,
	}
	for host, want := range cases {
		if got := isCarouselSite(host); got != want {
			t.Errorf("isCarouselSite(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestRunCommandStreamingStdoutCancelPropagates(t *testing.T) {
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("/bin/sleep not available")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := runCommandStreamingStdout(ctx, time.Minute, nil, "/bin/sleep", "10")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected error from canceled command, got nil (elapsed %s)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected fast cancel (<2s), took %s — ctx not propagating", elapsed)
	}
}

func TestRunCommandWithTimeoutCancelPropagates(t *testing.T) {
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("/bin/sleep not available")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := runCommandWithTimeout(ctx, time.Minute, "/bin/sleep", "10")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected error from canceled command, got nil (elapsed %s)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected fast cancel (<2s), took %s — ctx not propagating", elapsed)
	}
}

func TestMediaGetCommandString(t *testing.T) {
	tmpDir := "/tmp/test"
	randomName := "test-uuid"

	tests := []struct {
		name           string
		url            string
		audioOnly      bool
		simplified     bool
		expectedParams []string
		notExpected    []string
	}{
		{
			name:           "YouTube video with compatible format selection",
			url:            "https://www.youtube.com/watch?v=test",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--no-playlist", "--max-filesize", defaultMaxMediaFileSize, "--merge-output-format", "mp4", "-f", compatibleVideoFormatSelector},
			notExpected:    []string{"--recode-video", "-x", "--audio-format", "-S"},
		},
		{
			name:           "YouTube video simplified",
			url:            "https://www.youtube.com/watch?v=test",
			audioOnly:      false,
			simplified:     true,
			expectedParams: []string{"yt-dlp", "--no-playlist", "--max-filesize", defaultMaxMediaFileSize, "--merge-output-format", "mp4", "-f", "best[ext=mp4]/best"},
			notExpected:    []string{"--recode-video", "-S", "-x"},
		},
		{
			name:           "YouTube Shorts",
			url:            "https://www.youtube.com/shorts/test",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--no-playlist", "--max-filesize", defaultMaxMediaFileSize, "--merge-output-format", "mp4", "-f", compatibleVideoFormatSelector},
			notExpected:    []string{"--recode-video", "-S"},
		},
		{
			name:           "YouTube audio only",
			url:            "https://www.youtube.com/watch?v=test",
			audioOnly:      true,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--no-playlist", "--max-filesize", defaultMaxMediaFileSize, "-x", "--audio-format", "mp3"},
			notExpected:    []string{"--recode-video", "--merge-output-format", "-f", "-S"},
		},
		{
			name:           "TikTok video",
			url:            "https://www.tiktok.com/@user/video/123",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--no-playlist", "--max-filesize", defaultMaxMediaFileSize, "--merge-output-format", "mp4", "-f", "b[url!^=\"https://www.tiktok.com/\"]"},
			notExpected:    []string{"--recode-video", "-x", "-S", compatibleVideoFormatSelector},
		},
		{
			name:           "Generic URL",
			url:            "https://example.com/video.mp4",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--no-playlist", "--max-filesize", defaultMaxMediaFileSize, "--merge-output-format", "mp4", "-f", compatibleVideoFormatSelector},
			notExpected:    []string{"--recode-video", "-S", "-x"},
		},
		{
			name:           "Instagram post (carousel) — no --no-playlist",
			url:            "https://www.instagram.com/p/DWeuKatiKmT/",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--max-filesize", defaultMaxMediaFileSize, "--merge-output-format", "mp4"},
			notExpected:    []string{"--no-playlist", "--playlist-items"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedUrl, err := url.Parse(tt.url)
			if err != nil {
				t.Fatalf("Failed to parse URL: %v", err)
			}

			media := &Media{
				tmpDir:      tmpDir,
				url:         tt.url,
				parsedUrl:   parsedUrl,
				randomName:  randomName,
				audioOnly:   tt.audioOnly,
				cookiesFile: "",
			}

			result := media.getCommandString(tt.simplified)

			for _, param := range tt.expectedParams {
				assertContainsParam(t, result, param)
			}

			for _, param := range tt.notExpected {
				assertNotContainsParam(t, result, param)
			}

			assertContainsParam(t, result, tt.url)
		})
	}
}

func TestMediaGetCommandStringInstagramPlaylistItems(t *testing.T) {
	parsedUrl, err := url.Parse("https://www.instagram.com/p/DWeuKatiKmT/")
	if err != nil {
		t.Fatalf("Failed to parse URL: %v", err)
	}

	media := &Media{
		tmpDir:        "/tmp/test",
		url:           "https://www.instagram.com/p/DWeuKatiKmT/",
		parsedUrl:     parsedUrl,
		randomName:    "test-uuid",
		playlistIndex: 3,
	}

	result := media.getCommandString(false)

	assertContainsParam(t, result, "--playlist-items")
	assertContainsParam(t, result, "3")
	assertNotContainsParam(t, result, "--no-playlist")
}

func TestMediaGetCommandStringSingleFormatSelector(t *testing.T) {
	parsedUrl, err := url.Parse("https://www.tiktok.com/@user/video/123")
	if err != nil {
		t.Fatalf("Failed to parse URL: %v", err)
	}

	media := &Media{
		tmpDir:     "/tmp/test",
		url:        "https://www.tiktok.com/@user/video/123",
		parsedUrl:  parsedUrl,
		randomName: "test-uuid",
	}

	result := media.getCommandString(false)
	formatFlags := 0
	for _, param := range result {
		if param == "-f" {
			formatFlags++
		}
	}

	if formatFlags != 1 {
		t.Fatalf("Expected exactly one -f flag, got %d in %v", formatFlags, result)
	}
}

func TestPreflightCommandIncludesLimits(t *testing.T) {
	t.Setenv("MAX_MEDIA_FILESIZE", "42M")

	parsedUrl, err := url.Parse("https://example.com/video.mp4")
	if err != nil {
		t.Fatalf("Failed to parse URL: %v", err)
	}

	media := &Media{
		url:       "https://example.com/video.mp4",
		parsedUrl: parsedUrl,
	}

	result := media.getPreflightCommandString()
	expectedParams := []string{
		"yt-dlp",
		"--no-warnings",
		"--no-playlist",
		"--dump-json",
		"--skip-download",
		"-f",
		compatibleVideoFormatSelector,
		"https://example.com/video.mp4",
	}

	for _, param := range expectedParams {
		assertContainsParam(t, result, param)
	}
}

func TestEnforceDownloadedFileSizeLimit(t *testing.T) {
	t.Setenv("MAX_MEDIA_FILESIZE", "5B")

	tmpDir := t.TempDir()
	path := tmpDir + "/media.mp4"
	if err := os.WriteFile(path, []byte("123456"), 0644); err != nil {
		t.Fatalf("Failed to write test media: %v", err)
	}

	media := &Media{Path: path}
	err := media.enforceDownloadedFileSizeLimit()
	if err == nil {
		t.Fatal("Expected downloaded file size limit error")
	}
}

func TestSelectedFileSize(t *testing.T) {
	info := YTDLPPreflightInfo{
		RequestedFormats: []YTDLPPreflightInfo{
			{FileSize: 100},
			{FileSizeApprox: 50},
		},
	}

	if got := info.selectedFileSize(); got != 150 {
		t.Fatalf("Expected selected size 150, got %d", got)
	}
}

func TestSelectedFileSizeUnknownComposite(t *testing.T) {
	info := YTDLPPreflightInfo{
		RequestedFormats: []YTDLPPreflightInfo{
			{FileSize: 100},
			{},
		},
	}

	if got := info.selectedFileSize(); got != 0 {
		t.Fatalf("Expected unknown selected size 0, got %d", got)
	}
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		hasError bool
	}{
		{input: "250M", expected: 250 * 1024 * 1024},
		{input: "1.5G", expected: int64(1.5 * 1024 * 1024 * 1024)},
		{input: "1024", expected: 1024},
		{input: "42B", expected: 42},
		{input: "", hasError: true},
		{input: "bad", hasError: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseByteSize(tt.input)
			if tt.hasError {
				if err == nil {
					t.Fatal("Expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Fatalf("Expected %d, got %d", tt.expected, got)
			}
		})
	}
}

func TestMediaGetCommandStringWithCookies(t *testing.T) {
	tmpDir := "/tmp/test"
	randomName := "test-uuid"
	cookiesFile := "/path/to/cookies.txt"
	testUrl := "https://www.youtube.com/watch?v=test"

	parsedUrl, err := url.Parse(testUrl)
	if err != nil {
		t.Fatalf("Failed to parse URL: %v", err)
	}

	media := &Media{
		tmpDir:      tmpDir,
		url:         testUrl,
		parsedUrl:   parsedUrl,
		randomName:  randomName,
		cookiesFile: cookiesFile,
	}

	result := media.getCommandString(false)

	cookiesFound := false
	pathFound := false
	for i, param := range result {
		if param == "--cookies" {
			cookiesFound = true
			if i+1 < len(result) && result[i+1] == cookiesFile {
				pathFound = true
			}
		}
	}

	if !cookiesFound {
		t.Error("Expected --cookies parameter not found")
	}
	if !pathFound {
		t.Error("Expected cookies file path not found")
	}
}

func BenchmarkGetCommandString(b *testing.B) {
	parsedUrl, _ := url.Parse("https://www.youtube.com/watch?v=test")
	media := &Media{
		tmpDir:     "/tmp/test",
		url:        "https://www.youtube.com/watch?v=test",
		parsedUrl:  parsedUrl,
		randomName: "test-uuid",
	}

	for i := 0; i < b.N; i++ {
		media.getCommandString(false)
	}
}

func assertContainsParam(t *testing.T, params []string, expected string) {
	t.Helper()
	for _, param := range params {
		if param == expected {
			return
		}
	}
	t.Errorf("Expected parameter %s not found in result: %v", expected, params)
}

func assertNotContainsParam(t *testing.T, params []string, unexpected string) {
	t.Helper()
	for _, param := range params {
		if param == unexpected {
			t.Errorf("Unexpected parameter %s found in result: %v", unexpected, params)
		}
	}
}

func TestDownloadWithSizeRetry(t *testing.T) {
	for _, scenario := range []string{"preflight", "actual", "ytdlp", "still-large", "normal"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("MAX_MEDIA_FILESIZE", "5B")
			t.Setenv("SCENARIO", scenario)
			t.Setenv("CALLS", dir+"/calls")
			t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
			script := `#!/bin/sh
preflight=no
reduced=no
while [ "$#" -gt 0 ]; do
 case "$1" in
 --dump-json) preflight=yes ;;
 -f) shift; case "$1" in *'width<=854'*) reduced=yes ;; esac ;;
 -o) shift; output="$1" ;;
 esac
 shift
done
printf '%s %s\n' "$preflight" "$reduced" >> "$CALLS"
if [ "$preflight" = yes ]; then
 size=4
 if [ "$SCENARIO" = still-large ] || { [ "$SCENARIO" = preflight ] && [ "$reduced" = no ]; }; then size=6; fi
 printf '{"width":1280,"height":720,"filesize":%s}\n' "$size"
 exit 0
fi
if [ "$SCENARIO" = ytdlp ] && [ "$reduced" = no ]; then
 echo '[download] File is larger than max-filesize (6 bytes > 5 bytes). Aborting.'
 exit 0
fi
output="${output%.*}.mp4"
if [ "$SCENARIO" = actual ] && [ "$reduced" = no ]; then
 printf 123456 > "$output"
else
 printf 1234 > "$output"
fi
`
			if err := os.WriteFile(dir+"/yt-dlp", []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse("https://youtube.com/watch?v=test")
			media := &Media{url: u.String(), parsedUrl: u, tmpDir: dir, randomName: "video"}
			err := media.downloadWithSizeRetry(context.Background(), nil)
			if scenario == "still-large" {
				if !errors.Is(err, errMediaTooLarge) {
					t.Fatalf("expected size error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(dir + "/calls")
			if err != nil {
				t.Fatal(err)
			}
			want := "yes no\nno no\nyes yes\nno yes\n"
			switch scenario {
			case "preflight":
				want = "yes no\nyes yes\nno yes\n"
			case "still-large":
				want = "yes no\nyes yes\n"
			case "normal":
				want = "yes no\nno no\n"
			}
			if string(calls) != want {
				t.Fatalf("calls = %q, want %q", calls, want)
			}
			if scenario != "still-large" {
				data, err := os.ReadFile(media.Path)
				if err != nil || string(data) != "1234" {
					t.Fatalf("download = %q, err %v", data, err)
				}
			}
		})
	}
}

func TestResolutionReduction(t *testing.T) {
	for _, dims := range [][2]int{{1280, 854}, {1920, 1280}, {854, 640}, {640, 426}, {0, 0}, {256, 0}} {
		media := &Media{selectedMaxDimension: dims[0]}
		got := media.reduceResolution()
		if got != (dims[1] > 0) || media.reducedMaxDimension != dims[1] {
			t.Fatalf("%v: reduced %v to %d", dims, got, media.reducedMaxDimension)
		}
		if media.reduceResolution() {
			t.Fatal("reduced twice")
		}
		if got && media.videoFormatSelector(false) != media.videoFormatSelector(true) {
			t.Fatal("simplified fallback bypasses cap")
		}
	}
	media := &Media{audioOnly: true, selectedMaxDimension: 1280}
	if media.reduceResolution() {
		t.Fatal("reduced audio")
	}
}
