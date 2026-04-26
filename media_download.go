package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxMediaFileSize = "250M"
	defaultYTDLPTimeout     = 15 * time.Minute
	defaultFFProbeTimeout   = 30 * time.Second
	defaultFFmpegTimeout    = 5 * time.Minute
)

const compatibleVideoFormatSelector = "b[vcodec^=avc1][acodec^=mp4a][ext=mp4][width<=1280][height<=1280]/" +
	"bv*[vcodec^=avc1][ext=mp4][width<=1280][height<=1280]+ba[acodec^=mp4a][ext=m4a]/" +
	"b[vcodec^=avc1][ext=mp4][width<=1280][height<=1280]/" +
	"b[ext=mp4][vcodec=unknown][width<=1080]/" +
	"b[ext=mp4][vcodec=unknown]/" +
	"bv*[vcodec^=avc1][ext=mp4]+ba[acodec^=mp4a][ext=m4a]/" +
	"b[vcodec^=avc1][ext=mp4]/" +
	"best[ext=mp4]/best"

type YTDLPPreflightInfo struct {
	FormatID         string               `json:"format_id"`
	FormatNote       string               `json:"format_note"`
	Ext              string               `json:"ext"`
	VCodec           string               `json:"vcodec"`
	ACodec           string               `json:"acodec"`
	Protocol         string               `json:"protocol"`
	Width            int                  `json:"width"`
	Height           int                  `json:"height"`
	FileSize         int64                `json:"filesize"`
	FileSizeApprox   int64                `json:"filesize_approx"`
	RequestedFormats []YTDLPPreflightInfo `json:"requested_formats"`
}

type commandResult struct {
	stdout  string
	stderr  string
	elapsed time.Duration
}

func runCommandWithTimeout(parentCtx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	startedAt := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := commandResult{
		stdout:  out.String(),
		stderr:  stderr.String(),
		elapsed: time.Since(startedAt),
	}

	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		return result, err
	}

	return result, nil
}

func runCommandStreamingStdout(parentCtx context.Context, timeout time.Duration, onLine func(string), name string, args ...string) (commandResult, error) {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	startedAt := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return commandResult{elapsed: time.Since(startedAt)}, fmt.Errorf("creating stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return commandResult{elapsed: time.Since(startedAt)}, fmt.Errorf("starting command: %w", err)
	}

	var stdoutBuf bytes.Buffer
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		stdoutBuf.WriteString(line)
		stdoutBuf.WriteByte('\n')
		if onLine != nil {
			onLine(line)
		}
	}

	waitErr := cmd.Wait()
	result := commandResult{
		stdout:  stdoutBuf.String(),
		stderr:  stderrBuf.String(),
		elapsed: time.Since(startedAt),
	}

	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("command timed out after %s", timeout)
	}
	if waitErr != nil {
		return result, waitErr
	}
	return result, nil
}

const ytdlpProgressPrefix = "ytprog|"
const ytdlpProgressTemplate = ytdlpProgressPrefix + "%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.total_bytes_estimate)s|%(info.format_id)s|%(progress.fragment_index)s|%(progress.fragment_count)s"

type ytdlpProgressTick struct {
	downloaded    int64
	total         int64
	estimate      int64
	formatID      string
	fragmentIndex int64
	fragmentCount int64
}

func parseYTDLPProgressTick(line string) (ytdlpProgressTick, bool) {
	if !strings.HasPrefix(line, ytdlpProgressPrefix) {
		return ytdlpProgressTick{}, false
	}
	parts := strings.Split(strings.TrimPrefix(line, ytdlpProgressPrefix), "|")
	if len(parts) != 6 {
		return ytdlpProgressTick{}, false
	}
	return ytdlpProgressTick{
		downloaded:    parseYTDLPNumber(parts[0]),
		total:         parseYTDLPNumber(parts[1]),
		estimate:      parseYTDLPNumber(parts[2]),
		formatID:      parts[3],
		fragmentIndex: parseYTDLPNumber(parts[4]),
		fragmentCount: parseYTDLPNumber(parts[5]),
	}, true
}

func stripProgressLines(s string) string {
	if !strings.Contains(s, ytdlpProgressPrefix) {
		return s
	}
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(line, ytdlpProgressPrefix) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func parseYTDLPNumber(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	if f < 0 {
		return 0
	}
	return int64(f)
}

type progressUpdate struct {
	percent     int
	streamIndex int
}

type streamAggregator struct {
	currentFormat string
	streamIndex   int
}

func (s *streamAggregator) update(tick ytdlpProgressTick) (progressUpdate, bool) {
	if tick.formatID != s.currentFormat {
		s.currentFormat = tick.formatID
		s.streamIndex++
	}

	var pct int
	switch {
	case tick.fragmentCount > 0:
		pct = int(tick.fragmentIndex * 100 / tick.fragmentCount)
	case tick.total > 0:
		pct = int(tick.downloaded * 100 / tick.total)
	case tick.estimate > 0:
		pct = int(tick.downloaded * 100 / tick.estimate)
	default:
		return progressUpdate{}, false
	}

	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	return progressUpdate{percent: pct, streamIndex: s.streamIndex}, true
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		log.Printf("invalid %s=%q, using default %d", name, raw, fallback)
		return fallback
	}

	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	seconds := envInt(name, int(fallback/time.Second))
	return time.Duration(seconds) * time.Second
}

func maxMediaFileSize() string {
	value := strings.TrimSpace(os.Getenv("MAX_MEDIA_FILESIZE"))
	if value == "" {
		return defaultMaxMediaFileSize
	}
	return value
}

func ytDLPTimeout() time.Duration {
	return envDuration("YT_DLP_TIMEOUT_SECONDS", defaultYTDLPTimeout)
}

func ffmpegTimeout() time.Duration {
	return envDuration("FFMPEG_TIMEOUT_SECONDS", defaultFFmpegTimeout)
}

func (media *Media) checkMediaBeforeDownload(ctx context.Context) error {
	commandString := media.getPreflightCommandString()
	log.Printf("[%s]: running yt-dlp preflight", media.logTag)

	result, err := runCommandWithTimeout(ctx, ytDLPTimeout(), commandString[0], commandString[1:]...)
	if err != nil {
		if ctx.Err() != nil {
			log.Printf("[%s]: preflight canceled after %s", media.logTag, formatElapsed(result.elapsed))
			return ctx.Err()
		}
		log.Printf("[%s]: preflight command: '%s'", media.logTag, strings.Join(commandString, " "))
		log.Printf("Preflight Output: %s\n", result.stdout)
		log.Printf("Preflight Error: %s\n", result.stderr)
		log.Printf("[%s]: preflight failed after %s", media.logTag, formatElapsed(result.elapsed))
		return fmt.Errorf("media preflight failed: %w", err)
	}
	log.Printf("[%s]: preflight completed in %s", media.logTag, formatElapsed(result.elapsed))

	var info YTDLPPreflightInfo
	if err := json.Unmarshal([]byte(result.stdout), &info); err != nil {
		return fmt.Errorf("media preflight returned invalid JSON: %w", err)
	}

	log.Printf("[%s]: selected format: %s", media.logTag, info.selectedFormatSummary())

	if selectedSize := info.selectedFileSize(); selectedSize > 0 {
		if err := checkMediaSizeLimit(selectedSize, "selected media"); err != nil {
			return err
		}
	}

	return nil
}

func (media *Media) getPreflightCommandString() []string {
	res := []string{
		"yt-dlp",
		"--no-warnings",
		"--no-playlist",
		"--dump-json",
		"--skip-download",
	}

	if !media.audioOnly {
		res = append(res, "-f", media.videoFormatSelector(false))
	}

	if media.cookiesFile != "" {
		res = append(res, "--cookies", media.cookiesFile)
	}

	res = append(res, media.url)
	return res
}

func (info YTDLPPreflightInfo) selectedFormatSummary() string {
	formats := info.RequestedFormats
	if len(formats) == 0 {
		formats = []YTDLPPreflightInfo{info}
	}

	parts := make([]string, 0, len(formats))
	for _, format := range formats {
		parts = append(parts, format.formatSummary())
	}

	summary := strings.Join(parts, " + ")
	if len(formats) > 1 {
		summary += " action=merge/remux"
	} else {
		summary += " action=direct-download"
	}
	return summary
}

func (info YTDLPPreflightInfo) formatSummary() string {
	size := info.FileSize
	if size == 0 {
		size = info.FileSizeApprox
	}

	sizeLabel := "size=unknown"
	if size > 0 {
		sizeLabel = fmt.Sprintf("size=%.1fMB", bytesToMB(size))
	}

	resolution := "unknown-res"
	if info.Width > 0 && info.Height > 0 {
		resolution = fmt.Sprintf("%dx%d", info.Width, info.Height)
	}

	formatID := info.FormatID
	if formatID == "" {
		formatID = "unknown-format"
	}

	return fmt.Sprintf("%s %s %s/%s %s protocol=%s %s", formatID, info.Ext, info.VCodec, info.ACodec, resolution, info.Protocol, sizeLabel)
}

func (info YTDLPPreflightInfo) selectedFileSize() int64 {
	if len(info.RequestedFormats) > 0 {
		var total int64
		for _, format := range info.RequestedFormats {
			size := format.FileSize
			if size == 0 {
				size = format.FileSizeApprox
			}
			if size == 0 {
				return 0
			}
			total += size
		}
		return total
	}

	if info.FileSize != 0 {
		return info.FileSize
	}
	return info.FileSizeApprox
}

func (media *Media) getCommandString(simplified bool) []string {
	res := []string{"yt-dlp", "--no-playlist", "--newline", "--progress-template", ytdlpProgressTemplate, "--max-filesize", maxMediaFileSize()}

	if media.audioOnly {
		res = append(res, "-x", "--audio-format", "mp3")
	} else {
		res = append(res, "--merge-output-format", "mp4")
	}

	res = append(res, "--write-info-json")

	if !media.audioOnly {
		res = append(res, "-f", media.videoFormatSelector(simplified))
	}

	res = append(res, "-o", media.tmpDir+"/"+media.randomName+".%(ext)s", media.url)

	if media.cookiesFile != "" {
		res = append(res, "--cookies", media.cookiesFile)
	}

	return res
}

func (media *Media) videoFormatSelector(simplified bool) string {
	if simplified {
		return "best[ext=mp4]/best"
	}

	if strings.Contains(media.parsedUrl.Host, "tiktok.com") {
		return "b[url!^=\"https://www.tiktok.com/\"]"
	}

	return compatibleVideoFormatSelector
}

func (media *Media) executeDownload(ctx context.Context, simplified bool, onProgress func(progressUpdate)) error {
	commandString := media.getCommandString(simplified)

	if simplified {
		log.Printf("[%s]: running yt-dlp download with simplified selector", media.logTag)
	} else {
		log.Printf("[%s]: running yt-dlp download", media.logTag)
	}

	agg := &streamAggregator{}
	onLine := func(line string) {
		if onProgress == nil {
			return
		}
		tick, ok := parseYTDLPProgressTick(line)
		if !ok {
			return
		}
		if upd, ok := agg.update(tick); ok {
			onProgress(upd)
		}
	}

	result, err := runCommandStreamingStdout(ctx, ytDLPTimeout(), onLine, commandString[0], commandString[1:]...)
	if err != nil {
		if ctx.Err() != nil {
			log.Printf("[%s]: yt-dlp download canceled after %s", media.logTag, formatElapsed(result.elapsed))
			return ctx.Err()
		}
		log.Printf("[%s]: yt-dlp command: '%s'", media.logTag, strings.Join(commandString, " "))
		log.Printf("Output: %s\n", stripProgressLines(result.stdout))
		log.Printf("Error: %s\n", result.stderr)
		log.Printf("[%s]: yt-dlp download failed after %s", media.logTag, formatElapsed(result.elapsed))
		return fmt.Errorf("command execution failed with %w", err)
	}

	log.Printf("[%s]: yt-dlp download completed in %s", media.logTag, formatElapsed(result.elapsed))
	return nil
}

func (media *Media) findDownloadedMediaPath() (string, error) {
	matches, err := filepath.Glob(filepath.Join(media.tmpDir, media.randomName+".*"))
	if err != nil {
		return "", fmt.Errorf("error locating downloaded media: %w", err)
	}

	for _, match := range matches {
		if strings.HasSuffix(match, ".info.json") || strings.HasSuffix(match, ".part") || strings.HasSuffix(match, ".ytdl") {
			continue
		}
		return match, nil
	}

	return "", fmt.Errorf("downloaded media file not found for %s", media.randomName)
}

func (media *Media) enforceDownloadedFileSizeLimit() error {
	info, err := os.Stat(media.Path)
	if err != nil {
		return fmt.Errorf("error getting downloaded media file info: %w", err)
	}

	if err := checkMediaSizeLimit(info.Size(), "downloaded media"); err != nil {
		return err
	}

	log.Printf("[%s]: downloaded media size accepted: %.1fMB <= %s", media.logTag, bytesToMB(info.Size()), maxMediaFileSize())
	return nil
}

func checkMediaSizeLimit(size int64, label string) error {
	maxSize, err := parseByteSize(maxMediaFileSize())
	if err != nil {
		log.Printf("invalid MAX_MEDIA_FILESIZE=%q, skipping size check: %v", maxMediaFileSize(), err)
		return nil
	}

	if size > maxSize {
		return fmt.Errorf("%s size %.1fMB exceeds limit %.1fMB", label, bytesToMB(size), bytesToMB(maxSize))
	}

	return nil
}

func (media *Media) deleteDownloadedFiles() {
	matches, err := filepath.Glob(filepath.Join(media.tmpDir, media.randomName+".*"))
	if err != nil {
		log.Printf("[%s]: error locating files for cleanup: %s", media.logTag, err)
		return
	}

	for _, match := range matches {
		if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
			log.Printf("[%s]: error removing oversized download artifact %s: %s", media.logTag, match, err)
		}
	}
}

func parseByteSize(value string) (int64, error) {
	raw := strings.TrimSpace(strings.ToUpper(value))
	if raw == "" {
		return 0, fmt.Errorf("empty size")
	}

	multiplier := int64(1)
	last := raw[len(raw)-1]
	switch last {
	case 'K':
		multiplier = 1024
		raw = strings.TrimSpace(raw[:len(raw)-1])
	case 'M':
		multiplier = 1024 * 1024
		raw = strings.TrimSpace(raw[:len(raw)-1])
	case 'G':
		multiplier = 1024 * 1024 * 1024
		raw = strings.TrimSpace(raw[:len(raw)-1])
	case 'B':
		raw = strings.TrimSpace(raw[:len(raw)-1])
	}

	size, err := strconv.ParseFloat(raw, 64)
	if err != nil || size <= 0 {
		return 0, fmt.Errorf("invalid size %q", value)
	}

	return int64(size * float64(multiplier)), nil
}

func bytesToMB(value int64) float64 {
	return float64(value) / 1024 / 1024
}

func formatElapsed(elapsed time.Duration) string {
	return elapsed.Round(100 * time.Millisecond).String()
}
