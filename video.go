package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type Media struct {
	Width    int            `json:"width"`
	Height   int            `json:"height"`
	Duration CustomDuration `json:"duration_string"`
	VCodec   string         `json:"vcodec"`
	ACodec   string         `json:"acodec"`
	Path     string
	FileName string
	Title    string         `json:"title"`

	randomName  string
	tmpDir      string
	url         string
	parsedUrl   *url.URL
	user        string
	cookiesFile string
	audioOnly   bool
}

// MediaAnalysis contains analysis results for intelligent conversion
type MediaAnalysis struct {
	OriginalBitrate      int64
	OriginalFileSize     int64
	NeedsVideoConversion bool
	NeedsAudioConversion bool
	AudioConversionType  string // "aac", "copy", "none"
	OriginalVideoCodec   string
	OriginalAudioCodec   string
	IsAlreadyCompatible  bool
}

type CustomDuration int

// FFProbeResult represents the JSON output from ffprobe
type FFProbeResult struct {
	Format struct {
		BitRate string `json:"bit_rate"`
	} `json:"format"`
	Streams []FFProbeStream `json:"streams"`
}

// FFProbeStream represents a single stream from ffprobe output
type FFProbeStream struct {
	Index       int    `json:"index"`
	CodecType   string `json:"codec_type"`
	CodecName   string `json:"codec_name"`
	BitRate     string `json:"bit_rate"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Channels    int    `json:"channels,omitempty"`
	Disposition struct {
		Default int `json:"default"`
	} `json:"disposition"`
}

// runFFProbe executes ffprobe and returns parsed JSON output
func (media *Media) runFFProbe() (*FFProbeResult, error) {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", media.Path)
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var probeResult FFProbeResult
	if err := json.Unmarshal(out.Bytes(), &probeResult); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	return &probeResult, nil
}

// selectBestVideoStream chooses the best video stream from available streams
func selectBestVideoStream(streams []FFProbeStream) *FFProbeStream {
	var videoStreams []*FFProbeStream

	// Collect all video streams
	for i := range streams {
		if streams[i].CodecType == "video" {
			videoStreams = append(videoStreams, &streams[i])
		}
	}

	if len(videoStreams) == 0 {
		return nil
	}

	// First, check for default disposition
	for _, stream := range videoStreams {
		if stream.Disposition.Default == 1 {
			return stream
		}
	}

	// If no default, select by quality (resolution)
	bestStream := videoStreams[0]
	for _, stream := range videoStreams[1:] {
		if isVideoStreamBetter(stream, bestStream) {
			bestStream = stream
		}
	}

	return bestStream
}

// selectBestAudioStream chooses the best audio stream from available streams
func selectBestAudioStream(streams []FFProbeStream) *FFProbeStream {
	var audioStreams []*FFProbeStream

	// Collect all audio streams
	for i := range streams {
		if streams[i].CodecType == "audio" {
			audioStreams = append(audioStreams, &streams[i])
		}
	}

	if len(audioStreams) == 0 {
		return nil
	}

	// First, check for default disposition
	for _, stream := range audioStreams {
		if stream.Disposition.Default == 1 {
			return stream
		}
	}

	// If no default, select by quality (channels, then bitrate)
	bestStream := audioStreams[0]
	for _, stream := range audioStreams[1:] {
		if isAudioStreamBetter(stream, bestStream) {
			bestStream = stream
		}
	}

	return bestStream
}

// isVideoStreamBetter compares two video streams and returns true if stream1 is better
func isVideoStreamBetter(stream1, stream2 *FFProbeStream) bool {
	// Compare by resolution (width * height)
	resolution1 := stream1.Width * stream1.Height
	resolution2 := stream2.Width * stream2.Height

	if resolution1 != resolution2 {
		return resolution1 > resolution2
	}

	// If resolution is the same, compare by bitrate
	bitrate1 := parseBitrate(stream1.BitRate)
	bitrate2 := parseBitrate(stream2.BitRate)

	return bitrate1 > bitrate2
}

// isAudioStreamBetter compares two audio streams and returns true if stream1 is better
func isAudioStreamBetter(stream1, stream2 *FFProbeStream) bool {
	// Compare by channel count first
	if stream1.Channels != stream2.Channels {
		return stream1.Channels > stream2.Channels
	}

	// If channel count is the same, compare by bitrate
	bitrate1 := parseBitrate(stream1.BitRate)
	bitrate2 := parseBitrate(stream2.BitRate)

	return bitrate1 > bitrate2
}

// parseBitrate converts bitrate string to int64, returns 0 if invalid
func parseBitrate(bitrateStr string) int64 {
	if bitrateStr == "" {
		return 0
	}

	bitrate, err := strconv.ParseInt(bitrateStr, 10, 64)
	if err != nil {
		return 0
	}

	return bitrate
}

// analyzeMedia uses ffprobe to analyze video properties for intelligent conversion
func (media *Media) analyzeMedia() (*MediaAnalysis, error) {
	analysis := &MediaAnalysis{
		OriginalVideoCodec: media.VCodec,
		OriginalAudioCodec: media.ACodec,
	}

	// Get file size
	fileInfo, err := os.Stat(media.Path)
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}
	analysis.OriginalFileSize = fileInfo.Size()

	// Use ffprobe to get detailed media information
	probeResult, err := media.runFFProbe()
	if err != nil {
		return nil, err
	}

	// Parse overall bitrate
	if probeResult.Format.BitRate != "" {
		if bitrate, err := strconv.ParseInt(probeResult.Format.BitRate, 10, 64); err == nil {
			analysis.OriginalBitrate = bitrate
		}
	}

	// Select best video and audio streams
	bestVideoStream := selectBestVideoStream(probeResult.Streams)
	if bestVideoStream != nil {
		analysis.OriginalVideoCodec = bestVideoStream.CodecName
	}

	bestAudioStream := selectBestAudioStream(probeResult.Streams)
	if bestAudioStream != nil {
		analysis.OriginalAudioCodec = bestAudioStream.CodecName
	}

	return analysis, nil
}

// determineConversionStrategy analyzes media and decides what conversions are needed
func (media *Media) determineConversionStrategy(analysis *MediaAnalysis) {
	// Check if video conversion is needed
	analysis.NeedsVideoConversion = media.needsVideoConversion(analysis.OriginalVideoCodec)
	analysis.NeedsAudioConversion = media.needsAudioConversion(analysis.OriginalAudioCodec)

	// Video conversion strategy is determined by NeedsVideoConversion boolean
	// When true: convert to H.264, when false: copy stream

	// Set audio conversion type
	if analysis.NeedsAudioConversion {
		analysis.AudioConversionType = "aac"
	} else {
		analysis.AudioConversionType = "copy"
	}

	// CRF-based encoding will handle quality automatically

	// Check if already iPhone/mobile compatible
	analysis.IsAlreadyCompatible = !analysis.NeedsVideoConversion && !analysis.NeedsAudioConversion
}

// needsVideoConversion determines if video codec conversion is required for mobile/iOS compatibility
func (media *Media) needsVideoConversion(codecName string) bool {
	// AV1 (av01): Not supported on older iOS/Safari versions, limited hardware decode support
	if strings.HasPrefix(codecName, "av01") {
		return true
	}

	// VP9 (vp9, vp09): Poor hardware decode support on mobile devices, causes battery drain
	if strings.HasPrefix(codecName, "vp9") || strings.HasPrefix(codecName, "vp09") {
		return true
	}

	// Note: HEVC is kept as-is since it's well supported on modern iOS devices (iOS 11+)
	// and provides excellent compression efficiency

	// Also check the original yt-dlp detected codec for AV1/VP9
	if strings.HasPrefix(media.VCodec, "av01") || strings.HasPrefix(media.VCodec, "vp09") {
		return true
	}

	return false
}

// needsAudioConversion determines if audio codec conversion is required for mobile/web compatibility
func (media *Media) needsAudioConversion(codecName string) bool {
	// AAC is widely compatible across all platforms, no conversion needed
	if codecName == "aac" {
		return false
	}

	// Opus: Not supported in Safari/iOS browsers, mainly used in WebRTC/Discord
	if codecName == "opus" {
		return true
	}

	// Vorbis: Limited mobile browser support, primarily desktop/Linux format
	if codecName == "vorbis" {
		return true
	}

	// FLAC: Lossless format not supported on mobile browsers, creates large files
	if codecName == "flac" {
		return true
	}

	// Other codecs (MP3, etc.) are generally compatible and don't need conversion
	return false
}

// sanitizeFileName converts a video title to a safe, human-readable filename
// Returns empty string if the title cannot be sanitized to a valid filename
func sanitizeFileName(title string) string {
	if title == "" {
		return ""
	}

	// Define character replacements for filesystem safety
	replacements := map[rune]rune{
		'/':  '-',
		'\\': '-',
		'<':  '-',
		'>':  '-',
		':':  '-',
		'"':  '-',
		'|':  '-',
		'?':  0,
		'*':  0,
	}

	var result strings.Builder
	for _, char := range title {
		// Skip control characters (ASCII 0-31)
		if char < 32 {
			continue
		}

		// Replace problematic characters
		if replacement, exists := replacements[char]; exists {
			if replacement != 0 {
				result.WriteRune(replacement)
			}
			continue
		}

		result.WriteRune(char)
	}

	sanitized := result.String()

	// Trim leading/trailing spaces and dots
	sanitized = strings.Trim(sanitized, " .")

	// Collapse multiple spaces into one
	for strings.Contains(sanitized, "  ") {
		sanitized = strings.ReplaceAll(sanitized, "  ", " ")
	}

	// Aggressive truncation at 100 chars for mobile display
	if len(sanitized) > 100 {
		// Try to truncate at word boundary
		truncated := sanitized[:100]
		if lastSpace := strings.LastIndex(truncated, " "); lastSpace > 70 {
			sanitized = truncated[:lastSpace]
		} else {
			sanitized = truncated
		}
	}

	// Final validation - ensure not empty after sanitization
	sanitized = strings.TrimSpace(sanitized)
	if sanitized == "" {
		return ""
	}

	return sanitized
}

func (d *CustomDuration) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}

	parts := strings.Split(v, ":")
	var seconds int
	var err error

	switch len(parts) {
	case 1: // "ss"
		seconds, err = strconv.Atoi(parts[0])
	case 2: // "mm:ss"
		mm, err := strconv.Atoi(parts[0])
		if err != nil {
			return err
		}
		ss, err := strconv.Atoi(parts[1])
		if err != nil {
			return err
		}
		seconds = mm*60 + ss
	case 3: // "hh:mm:ss"
		hh, err := strconv.Atoi(parts[0])
		if err != nil {
			return err
		}
		mm, err := strconv.Atoi(parts[1])
		if err != nil {
			return err
		}
		ss, err := strconv.Atoi(parts[2])
		if err != nil {
			return err
		}
		seconds = hh*3600 + mm*60 + ss
	default:
		return fmt.Errorf("invalid time format")
	}

	if err != nil {
		return err
	}

	*d = CustomDuration(seconds)
	return nil
}

func DownloadMedia(mediaUrl string, user string, tmpDir string, cookiesFile string, audioOnly bool) (*Media, error) {
	res := &Media{
		tmpDir:      tmpDir,
		url:         mediaUrl,
		randomName:  uuid.New().String(),
		user:        user,
		cookiesFile: cookiesFile,
		audioOnly:   audioOnly,
	}

	u, err := url.Parse(mediaUrl)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid URL")
	}
	res.parsedUrl = u

	// First attempt with full arguments
	err = res.executeDownload(false)
	if err != nil {
		log.Printf("[%s]: First download attempt failed: %s", res.user, err)

		// Second attempt with simplified arguments (no -f and -S)
		log.Printf("[%s]: Retrying with simplified arguments", res.user)
		err = res.executeDownload(true)
		if err != nil {
			return nil, fmt.Errorf("both download attempts failed: %w", err)
		}
	}

	if audioOnly {
		res.Path = filepath.Join(tmpDir, res.randomName+".mp3")
	} else {
		res.Path = filepath.Join(tmpDir, res.randomName+".mp4")
	}

	if err := res.populateInfo(); err != nil {
		return nil, fmt.Errorf("error populating info: %w", err)
	}

	// Rename file to human-readable name if title is available
	if err := res.renameToReadableName(); err != nil {
		// Log warning but continue - UUID name still works fine
		log.Printf("[%s]: warning - could not rename to readable name: %s, keeping UUID name", res.user, err)
	}

	if audioOnly {
		log.Printf("[%s]: audio format '%s'", res.user, res.ACodec)
	} else {
		log.Printf("[%s]: video format '%s'", res.user, res.VCodec)

		// Perform intelligent analysis and conversion
		analysis, err := res.analyzeMedia()
		if err != nil {
			log.Printf("[%s]: warning - could not analyze media: %s, skipping conversion", res.user, err)
		} else {
			res.determineConversionStrategy(analysis)

			if analysis.IsAlreadyCompatible {
				log.Printf("[%s]: media is already iPhone compatible, no conversion needed", res.user)
			} else {
				videoAction := "copy"
				if analysis.NeedsVideoConversion {
					videoAction = "h264"
				}
				log.Printf("[%s]: media needs conversion - video: %s, audio: %s",
					res.user, videoAction, analysis.AudioConversionType)
				if err := res.convertIntelligent(analysis); err != nil {
					return nil, fmt.Errorf("error converting video: %w", err)
				}
			}
		}
	}

	return res, nil
}

func (media *Media) Delete() error {
	if err := os.Remove(media.Path); err != nil {
		return fmt.Errorf("error deleting file: %w", err)
	}

	return nil
}

func (media *Media) GetFileSize() (int64, error) {
	info, err := os.Stat(media.Path)
	if err != nil {
		return 0, fmt.Errorf("error getting file info: %w", err)
	}
	return info.Size(), nil
}

// convertIntelligent performs intelligent conversion based on analysis
func (media *Media) convertIntelligent(analysis *MediaAnalysis) error {
	// If we have a readable filename, maintain it with _converted suffix
	var outputFileName string
	if media.FileName != "" {
		// Remove extension, add _converted suffix, add extension back
		baseName := strings.TrimSuffix(media.FileName, filepath.Ext(media.FileName))
		outputFileName = baseName + "_converted.mp4"
	} else {
		outputFileName = media.randomName + "_converted.mp4"
	}
	outputPath := filepath.Join(media.tmpDir, outputFileName)

	var cmdSlice []string
	cmdSlice = append(cmdSlice, "ffmpeg", "-i", media.Path)

	// Video codec settings
	if analysis.NeedsVideoConversion {
		// H.264 with best practices for compatibility
		cmdSlice = append(cmdSlice, "-c:v", "libx264")
		cmdSlice = append(cmdSlice, "-profile:v", "baseline")
		cmdSlice = append(cmdSlice, "-pix_fmt", "yuv420p")
		cmdSlice = append(cmdSlice, "-crf", "23")
		cmdSlice = append(cmdSlice, "-maxrate", "4.5M")

		// Smart scaling - cap at 1280px width, maintain aspect ratio, ensure even dimensions
		cmdSlice = append(cmdSlice, "-vf", "scale='min(1280,iw)':-2")

		log.Printf("[%s]: using H.264 with CRF 23 and smart scaling", media.user)
	} else {
		// Copy video stream if no conversion needed
		cmdSlice = append(cmdSlice, "-c:v", "copy")
		log.Printf("[%s]: copying video stream (no conversion needed)", media.user)
	}

	// Audio codec settings
	if analysis.NeedsAudioConversion {
		cmdSlice = append(cmdSlice, "-c:a", "aac", "-ac", "2")
		log.Printf("[%s]: converting audio to AAC stereo", media.user)
	} else {
		cmdSlice = append(cmdSlice, "-c:a", "copy")
		log.Printf("[%s]: copying audio stream (no conversion needed)", media.user)
	}

	// Common settings for mobile compatibility
	cmdSlice = append(cmdSlice, "-movflags", "+faststart")
	cmdSlice = append(cmdSlice, outputPath)

	log.Printf("[%s]: executing intelligent conversion: '%s'", media.user, strings.Join(cmdSlice, " "))

	cmd := exec.Command(cmdSlice[0], cmdSlice[1:]...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Printf("FFmpeg Output: %s\n", out.String())
		log.Printf("FFmpeg Error: %s\n", stderr.String())

		return fmt.Errorf("ffmpeg conversion failed: %w", err)
	}

	// Get size comparison
	newFileInfo, _ := os.Stat(outputPath)
	if newFileInfo != nil {
		compressionRatio := float64(newFileInfo.Size()) / float64(analysis.OriginalFileSize)
		log.Printf("[%s]: conversion complete - size ratio: %.2f (%.1fMB → %.1fMB)",
			media.user, compressionRatio,
			float64(analysis.OriginalFileSize)/(1024*1024),
			float64(newFileInfo.Size())/(1024*1024))
	}

	media.Path = outputPath
	media.FileName = outputFileName

	// Clean up original file
	if err := os.Remove(filepath.Join(media.tmpDir, media.randomName+".mp4")); err != nil {
		log.Printf("error deleting original file: %s", err)
	}

	return nil
}

func (media *Media) populateInfo() error {
	jsonPath := filepath.Join(media.tmpDir, media.randomName+".info.json")

	buf, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("error reading json file '%s': %w", jsonPath, err)
	}

	if err := json.Unmarshal(buf, media); err != nil {
		return fmt.Errorf("error parsing json content: %w", err)
	}

	if err := os.Remove(jsonPath); err != nil {
		return fmt.Errorf("error deleting json file '%s': %w", jsonPath, err)
	}

	return nil
}

// renameToReadableName renames the media file from UUID to human-readable name based on title
func (media *Media) renameToReadableName() error {
	if media.Title == "" {
		return fmt.Errorf("no title available")
	}

	sanitizedTitle := sanitizeFileName(media.Title)
	if sanitizedTitle == "" {
		return fmt.Errorf("title sanitization resulted in empty string")
	}

	// Determine extension
	extension := ".mp4"
	if media.audioOnly {
		extension = ".mp3"
	}

	// Build new filename
	newFileName := sanitizedTitle + extension
	newPath := filepath.Join(media.tmpDir, newFileName)

	// Handle collision (unlikely in temp directory but possible)
	if _, err := os.Stat(newPath); err == nil {
		// File exists, append short UUID to make unique
		newFileName = sanitizedTitle + "_" + media.randomName[:8] + extension
		newPath = filepath.Join(media.tmpDir, newFileName)
	}

	// Rename the file
	if err := os.Rename(media.Path, newPath); err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}

	// Update media struct
	oldPath := media.Path
	media.Path = newPath
	media.FileName = newFileName

	log.Printf("[%s]: renamed file from '%s' to '%s'", media.user, filepath.Base(oldPath), newFileName)

	return nil
}

func (media *Media) getCommandString(simplified bool) []string {
	var res []string

	res = append(res, "yt-dlp")

	if media.audioOnly {
		res = append(res, "-x")
		res = append(res, "--audio-format")
		res = append(res, "mp3")
	} else {
		res = append(res, "--recode-video")
		res = append(res, "mp4")
	}

	res = append(res, "--write-info-json")

	if media.parsedUrl.Host == "www.youtube.com" || media.parsedUrl.Host == "youtube.com" || media.parsedUrl.Host == "youtu.be" {
		if !media.audioOnly && !strings.Contains(media.parsedUrl.Path, "shorts") && !simplified {
			res = append(res, "-f")
			res = append(res, "bv[filesize<=1700M]+ba[filesize<=300M]")
			res = append(res, "-S")
			res = append(res, "ext,res:720")
		}
	}

	if strings.Contains(media.parsedUrl.Host, "tiktok.com") {
		res = append(res, "-f")
		res = append(res, "b[url!^=\"https://www.tiktok.com/\"]")
	}

	res = append(res, "-o")
	res = append(res, media.tmpDir+"/"+media.randomName+".%(ext)s")
	res = append(res, media.url)

	if media.cookiesFile != "" {
		res = append(res, "--cookies")
		res = append(res, media.cookiesFile)
	}

	return res
}

func (media *Media) executeDownload(simplified bool) error {
	commandString := media.getCommandString(simplified)

	log.Printf("[%s]: executing command: '%s'", media.user, strings.Join(commandString, " "))

	cmd := exec.Command(commandString[0], commandString[1:]...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Printf("Output: %s\n", out.String())
		log.Printf("Error: %s\n", stderr.String())
		return fmt.Errorf("command execution failed with %w", err)
	}

	return nil
}
