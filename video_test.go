package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestCustomDurationUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		hasError bool
	}{
		{
			name:     "seconds only",
			input:    "30",
			expected: 30,
			hasError: false,
		},
		{
			name:     "minutes and seconds",
			input:    "5:30",
			expected: 330,
			hasError: false,
		},
		{
			name:     "hours, minutes, and seconds",
			input:    "1:30:45",
			expected: 5445,
			hasError: false,
		},
		{
			name:     "zero duration",
			input:    "0",
			expected: 0,
			hasError: false,
		},
		{
			name:     "invalid format",
			input:    "invalid",
			expected: 0,
			hasError: true,
		},
		{
			name:     "too many parts",
			input:    "1:2:3:4",
			expected: 0,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var duration CustomDuration
			jsonInput := fmt.Sprintf(`"%s"`, tt.input)
			err := json.Unmarshal([]byte(jsonInput), &duration)

			if tt.hasError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if int(duration) != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, int(duration))
			}
		})
	}
}

func TestMediaNeedsVideoConversion(t *testing.T) {
	media := &Media{}

	tests := []struct {
		name     string
		codec    string
		expected bool
	}{
		{
			name:     "AV1 codec needs conversion",
			codec:    "av01",
			expected: true,
		},
		{
			name:     "VP9 codec needs conversion",
			codec:    "vp9",
			expected: true,
		},
		{
			name:     "VP09 codec needs conversion",
			codec:    "vp09",
			expected: true,
		},
		{
			name:     "HEVC codec doesn't need conversion",
			codec:    "hevc",
			expected: false,
		},
		{
			name:     "H264 codec doesn't need conversion",
			codec:    "h264",
			expected: false,
		},
		{
			name:     "H263 codec doesn't need conversion",
			codec:    "h263",
			expected: false,
		},
		{
			name:     "Unknown codec doesn't need conversion",
			codec:    "unknown",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset media for each test
			media.VCodec = ""
			
			// Test with ffprobe detected codec
			result := media.needsVideoConversion(tt.codec)
			if result != tt.expected {
				t.Errorf("Expected %v for codec %s, got %v", tt.expected, tt.codec, result)
			}

			// Test with yt-dlp detected codec (only for codecs that are still checked)
			if tt.codec == "av01" || tt.codec == "vp09" {
				media.VCodec = tt.codec
				result = media.needsVideoConversion("h264") // Pass different codec to test VCodec check
				if !result {
					t.Errorf("Expected true for VCodec %s, got false", tt.codec)
				}
			}
		})
	}
}

func TestMediaNeedsAudioConversion(t *testing.T) {
	media := &Media{}

	tests := []struct {
		name     string
		codec    string
		expected bool
	}{
		{
			name:     "AAC codec doesn't need conversion",
			codec:    "aac",
			expected: false,
		},
		{
			name:     "Opus codec needs conversion",
			codec:    "opus",
			expected: true,
		},
		{
			name:     "Vorbis codec needs conversion",
			codec:    "vorbis",
			expected: true,
		},
		{
			name:     "FLAC codec needs conversion",
			codec:    "flac",
			expected: true,
		},
		{
			name:     "MP3 codec doesn't need conversion",
			codec:    "mp3",
			expected: false,
		},
		{
			name:     "Unknown codec doesn't need conversion",
			codec:    "unknown",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := media.needsAudioConversion(tt.codec)
			if result != tt.expected {
				t.Errorf("Expected %v for codec %s, got %v", tt.expected, tt.codec, result)
			}
		})
	}
}

func TestMediaCalculateTargetBitrate(t *testing.T) {
	media := &Media{
		Duration: CustomDuration(60), // 1 minute
	}

	tests := []struct {
		name           string
		fileSize       int64
		expectedMin    int64
		expectedMax    int64
		description    string
	}{
		{
			name:           "small file",
			fileSize:       10 * 1024 * 1024, // 10MB
			expectedMin:    200000,            // minimum bitrate
			expectedMax:    2000000,           // maximum bitrate
			description:    "should be within bounds",
		},
		{
			name:           "medium file",
			fileSize:       100 * 1024 * 1024, // 100MB
			expectedMin:    200000,             // minimum bitrate
			expectedMax:    2000000,            // maximum bitrate
			description:    "should calculate reasonable bitrate",
		},
		{
			name:           "large file",
			fileSize:       1000 * 1024 * 1024, // 1GB
			expectedMin:    200000,              // minimum bitrate
			expectedMax:    2000000,             // maximum bitrate (capped)
			description:    "should be capped at maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := &MediaAnalysis{
				OriginalFileSize: tt.fileSize,
			}

			result := media.calculateTargetBitrate(analysis)

			if result < tt.expectedMin {
				t.Errorf("Target bitrate %d is below minimum %d", result, tt.expectedMin)
			}

			if result > tt.expectedMax {
				t.Errorf("Target bitrate %d is above maximum %d", result, tt.expectedMax)
			}

			// Verify bitrate is reasonable (not zero)
			if result <= 0 {
				t.Errorf("Target bitrate should be positive, got %d", result)
			}
		})
	}
}

func TestDetermineConversionStrategy(t *testing.T) {
	tests := []struct {
		name                    string
		originalVideoCodec      string
		originalAudioCodec      string
		fileSize                int64
		expectedVideoConversion bool
		expectedAudioConversion bool
		expectedVideoType       string
		expectedAudioType       string
		expectedCompatible      bool
	}{
		{
			name:                    "compatible codecs",
			originalVideoCodec:      "h264",
			originalAudioCodec:      "aac",
			fileSize:                50 * 1024 * 1024,
			expectedVideoConversion: false,
			expectedAudioConversion: false,
			expectedVideoType:       "none",
			expectedAudioType:       "copy",
			expectedCompatible:      true,
		},
		{
			name:                    "small file with incompatible video",
			originalVideoCodec:      "av01",
			originalAudioCodec:      "aac",
			fileSize:                50 * 1024 * 1024,
			expectedVideoConversion: true,
			expectedAudioConversion: false,
			expectedVideoType:       "h265",
			expectedAudioType:       "copy",
			expectedCompatible:      false,
		},
		{
			name:                    "large file with incompatible video",
			originalVideoCodec:      "vp9",
			originalAudioCodec:      "aac",
			fileSize:                200 * 1024 * 1024,
			expectedVideoConversion: true,
			expectedAudioConversion: false,
			expectedVideoType:       "h265",
			expectedAudioType:       "copy",
			expectedCompatible:      false,
		},
		{
			name:                    "incompatible audio only",
			originalVideoCodec:      "h264",
			originalAudioCodec:      "opus",
			fileSize:                50 * 1024 * 1024,
			expectedVideoConversion: false,
			expectedAudioConversion: true,
			expectedVideoType:       "none",
			expectedAudioType:       "aac",
			expectedCompatible:      false,
		},
		{
			name:                    "both incompatible",
			originalVideoCodec:      "av01",
			originalAudioCodec:      "vorbis",
			fileSize:                50 * 1024 * 1024,
			expectedVideoConversion: true,
			expectedAudioConversion: true,
			expectedVideoType:       "h265",
			expectedAudioType:       "aac",
			expectedCompatible:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media := &Media{
				Duration: CustomDuration(60),
				VCodec:   tt.originalVideoCodec,
				ACodec:   tt.originalAudioCodec,
			}

			analysis := &MediaAnalysis{
				OriginalFileSize:   tt.fileSize,
				OriginalVideoCodec: tt.originalVideoCodec,
				OriginalAudioCodec: tt.originalAudioCodec,
			}

			media.determineConversionStrategy(analysis)

			if analysis.NeedsVideoConversion != tt.expectedVideoConversion {
				t.Errorf("Expected video conversion %v, got %v", tt.expectedVideoConversion, analysis.NeedsVideoConversion)
			}

			if analysis.NeedsAudioConversion != tt.expectedAudioConversion {
				t.Errorf("Expected audio conversion %v, got %v", tt.expectedAudioConversion, analysis.NeedsAudioConversion)
			}

			if analysis.VideoConversionType != tt.expectedVideoType {
				t.Errorf("Expected video type %s, got %s", tt.expectedVideoType, analysis.VideoConversionType)
			}

			if analysis.AudioConversionType != tt.expectedAudioType {
				t.Errorf("Expected audio type %s, got %s", tt.expectedAudioType, analysis.AudioConversionType)
			}

			if analysis.IsAlreadyCompatible != tt.expectedCompatible {
				t.Errorf("Expected compatibility %v, got %v", tt.expectedCompatible, analysis.IsAlreadyCompatible)
			}
		})
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
			name:           "YouTube video with format selection",
			url:            "https://www.youtube.com/watch?v=test",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--recode-video", "mp4", "-f", "bv[filesize<=1700M]+ba[filesize<=300M]", "-S", "ext,res:720"},
			notExpected:    []string{"-x", "--audio-format"},
		},
		{
			name:           "YouTube video simplified",
			url:            "https://www.youtube.com/watch?v=test",
			audioOnly:      false,
			simplified:     true,
			expectedParams: []string{"yt-dlp", "--recode-video", "mp4"},
			notExpected:    []string{"-f", "-S", "-x"},
		},
		{
			name:           "YouTube Shorts",
			url:            "https://www.youtube.com/shorts/test",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--recode-video", "mp4"},
			notExpected:    []string{"-f", "-S"},
		},
		{
			name:           "YouTube audio only",
			url:            "https://www.youtube.com/watch?v=test",
			audioOnly:      true,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "-x", "--audio-format", "mp3"},
			notExpected:    []string{"--recode-video", "-f", "-S"},
		},
		{
			name:           "TikTok video",
			url:            "https://www.tiktok.com/@user/video/123",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--recode-video", "mp4", "-f", "b[url!^=\"https://www.tiktok.com/\"]"},
			notExpected:    []string{"-x", "-S"},
		},
		{
			name:           "Generic URL",
			url:            "https://example.com/video.mp4",
			audioOnly:      false,
			simplified:     false,
			expectedParams: []string{"yt-dlp", "--recode-video", "mp4"},
			notExpected:    []string{"-f", "-S", "-x"},
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

			// Check expected parameters
			for _, param := range tt.expectedParams {
				found := false
				for _, resultParam := range result {
					if resultParam == param {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected parameter %s not found in result: %v", param, result)
				}
			}

			// Check parameters that should not be present
			for _, param := range tt.notExpected {
				for _, resultParam := range result {
					if resultParam == param {
						t.Errorf("Unexpected parameter %s found in result: %v", param, result)
					}
				}
			}

			// Verify URL is included
			found := false
			for _, resultParam := range result {
				if resultParam == tt.url {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("URL %s not found in result: %v", tt.url, result)
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

	// Check that cookies parameters are included
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

func TestMediaDelete(t *testing.T) {
	// Create a temporary file
	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "test_delete_"+uuid.New().String()+".txt")
	
	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	file.Close()

	media := &Media{
		Path: testFile,
	}

	// Test successful deletion
	err = media.Delete()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify file is deleted
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("File should have been deleted")
	}

	// Test deletion of non-existent file
	err = media.Delete()
	if err == nil {
		t.Error("Expected error when deleting non-existent file")
	}
}

func TestMediaGetFileSize(t *testing.T) {
	// Create a temporary file with known content
	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "test_size_"+uuid.New().String()+".txt")
	testContent := "Hello, World!"
	
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	media := &Media{
		Path: testFile,
	}

	// Test getting file size
	size, err := media.GetFileSize()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	expectedSize := int64(len(testContent))
	if size != expectedSize {
		t.Errorf("Expected size %d, got %d", expectedSize, size)
	}

	// Test getting size of non-existent file
	media.Path = "/non/existent/file.txt"
	_, err = media.GetFileSize()
	if err == nil {
		t.Error("Expected error when getting size of non-existent file")
	}
}

func TestDownloadMediaInvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{
			name: "empty URL",
			url:  "",
		},
		{
			name: "malformed URL",
			url:  "not-a-url",
		},
		{
			name: "URL without scheme",
			url:  "example.com/video",
		},
		{
			name: "URL without host",
			url:  "https://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := os.TempDir()
			_, err := DownloadMedia(tt.url, "testuser", tmpDir, "", false)
			if err == nil {
				t.Error("Expected error for invalid URL")
			}
		})
	}
}

// Benchmark tests
func BenchmarkCustomDurationUnmarshal(b *testing.B) {
	jsonInput := `"1:30:45"`
	
	for i := 0; i < b.N; i++ {
		var duration CustomDuration
		json.Unmarshal([]byte(jsonInput), &duration)
	}
}

func BenchmarkNeedsVideoConversion(b *testing.B) {
	media := &Media{VCodec: "h264"}
	
	for i := 0; i < b.N; i++ {
		media.needsVideoConversion("av01")
	}
}

func BenchmarkCalculateTargetBitrate(b *testing.B) {
	media := &Media{Duration: CustomDuration(60)}
	analysis := &MediaAnalysis{
		OriginalFileSize: 100 * 1024 * 1024,
	}
	
	for i := 0; i < b.N; i++ {
		media.calculateTargetBitrate(analysis)
	}
}

func BenchmarkGetCommandString(b *testing.B) {
	parsedUrl, _ := url.Parse("https://www.youtube.com/watch?v=test")
	media := &Media{
		tmpDir:    "/tmp/test",
		url:       "https://www.youtube.com/watch?v=test",
		parsedUrl: parsedUrl,
		randomName: "test-uuid",
	}
	
	for i := 0; i < b.N; i++ {
		media.getCommandString(false)
	}
}

// Test helper functions
func createTestMedia(t *testing.T) *Media {
	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "test_"+uuid.New().String()+".mp4")
	
	// Create a dummy file
	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	file.Close()

	parsedUrl, err := url.Parse("https://www.youtube.com/watch?v=test")
	if err != nil {
		t.Fatalf("Failed to parse URL: %v", err)
	}

	return &Media{
		Width:       1920,
		Height:      1080,
		Duration:    CustomDuration(60),
		VCodec:      "h264",
		ACodec:      "aac",
		Path:        testFile,
		FileName:    "test.mp4",
		randomName:  "test-uuid",
		tmpDir:      tmpDir,
		url:         "https://www.youtube.com/watch?v=test",
		parsedUrl:   parsedUrl,
		user:        "testuser",
		cookiesFile: "",
		audioOnly:   false,
	}
}

func TestMediaAnalysisInitialization(t *testing.T) {
	media := createTestMedia(t)
	defer os.Remove(media.Path)

	// This test would require ffprobe to be installed
	// For now, just test that the method exists and handles errors gracefully
	_, err := media.analyzeMedia()
	if err != nil {
		// Expected to fail without ffprobe, just ensure it doesn't panic
		t.Logf("Analysis failed as expected without ffprobe: %v", err)
	}
}

func TestRunFFProbe(t *testing.T) {
	media := createTestMedia(t)
	defer os.Remove(media.Path)

	// Test that runFFProbe exists and handles errors gracefully
	_, err := media.runFFProbe()
	if err != nil {
		// Expected to fail without ffprobe or with empty test file
		t.Logf("FFProbe failed as expected: %v", err)
	}
}

func TestFFProbeResultStruct(t *testing.T) {
	// Test that FFProbeResult can be unmarshaled from JSON
	jsonData := `{
		"format": {
			"bit_rate": "1000000"
		},
		"streams": [
			{
				"index": 0,
				"codec_type": "video",
				"codec_name": "h264",
				"bit_rate": "800000",
				"width": 1920,
				"height": 1080,
				"disposition": {
					"default": 1
				}
			},
			{
				"index": 1,
				"codec_type": "audio", 
				"codec_name": "aac",
				"bit_rate": "128000",
				"channels": 2,
				"disposition": {
					"default": 0
				}
			}
		]
	}`

	var result FFProbeResult
	err := json.Unmarshal([]byte(jsonData), &result)
	if err != nil {
		t.Errorf("Failed to unmarshal FFProbeResult: %v", err)
	}

	if result.Format.BitRate != "1000000" {
		t.Errorf("Expected bitrate 1000000, got %s", result.Format.BitRate)
	}

	if len(result.Streams) != 2 {
		t.Errorf("Expected 2 streams, got %d", len(result.Streams))
	}

	videoStream := result.Streams[0]
	if videoStream.CodecType != "video" || videoStream.CodecName != "h264" {
		t.Errorf("First stream should be h264 video")
	}
	if videoStream.Width != 1920 || videoStream.Height != 1080 {
		t.Errorf("Video stream should be 1920x1080")
	}
	if videoStream.Disposition.Default != 1 {
		t.Errorf("Video stream should be marked as default")
	}

	audioStream := result.Streams[1]
	if audioStream.CodecType != "audio" || audioStream.CodecName != "aac" {
		t.Errorf("Second stream should be aac audio")
	}
	if audioStream.Channels != 2 {
		t.Errorf("Audio stream should have 2 channels")
	}
	if audioStream.Disposition.Default != 0 {
		t.Errorf("Audio stream should not be marked as default")
	}
}

func TestSelectBestVideoStream(t *testing.T) {
	streams := []FFProbeStream{
		{
			Index: 0, CodecType: "video", CodecName: "h264",
			Width: 1280, Height: 720, BitRate: "2000000",
		},
		{
			Index: 1, CodecType: "video", CodecName: "h265",
			Width: 1920, Height: 1080, BitRate: "4000000",
		},
		{
			Index: 2, CodecType: "audio", CodecName: "aac",
			Channels: 2, BitRate: "128000",
		},
	}

	best := selectBestVideoStream(streams)
	if best == nil {
		t.Fatal("Expected to find a video stream")
	}

	// Should select the higher resolution stream
	if best.Index != 1 {
		t.Errorf("Expected stream index 1 (1080p), got index %d", best.Index)
	}
}

func TestSelectBestVideoStreamWithDefault(t *testing.T) {
	streams := []FFProbeStream{
		{
			Index: 0, CodecType: "video", CodecName: "h264",
			Width: 1920, Height: 1080, BitRate: "4000000",
		},
		{
			Index: 1, CodecType: "video", CodecName: "h264",
			Width: 1280, Height: 720, BitRate: "2000000",
		},
	}
	// Set disposition after creation
	streams[1].Disposition.Default = 1

	best := selectBestVideoStream(streams)
	if best == nil {
		t.Fatal("Expected to find a video stream")
	}

	// Should select the default stream even if it's lower quality
	if best.Index != 1 {
		t.Errorf("Expected stream index 1 (default), got index %d", best.Index)
	}
}

func TestSelectBestAudioStream(t *testing.T) {
	streams := []FFProbeStream{
		{
			Index: 0, CodecType: "audio", CodecName: "aac",
			Channels: 2, BitRate: "128000",
		},
		{
			Index: 1, CodecType: "audio", CodecName: "ac3",
			Channels: 6, BitRate: "448000", // 5.1 surround
		},
		{
			Index: 2, CodecType: "video", CodecName: "h264",
			Width: 1920, Height: 1080,
		},
	}

	best := selectBestAudioStream(streams)
	if best == nil {
		t.Fatal("Expected to find an audio stream")
	}

	// Should select the stream with more channels
	if best.Index != 1 {
		t.Errorf("Expected stream index 1 (6 channels), got index %d", best.Index)
	}
}

func TestSelectBestAudioStreamWithDefault(t *testing.T) {
	streams := []FFProbeStream{
		{
			Index: 0, CodecType: "audio", CodecName: "ac3",
			Channels: 6, BitRate: "448000",
		},
		{
			Index: 1, CodecType: "audio", CodecName: "aac",
			Channels: 2, BitRate: "128000",
		},
	}
	// Set disposition after creation
	streams[1].Disposition.Default = 1

	best := selectBestAudioStream(streams)
	if best == nil {
		t.Fatal("Expected to find an audio stream")
	}

	// Should select the default stream even if it has fewer channels
	if best.Index != 1 {
		t.Errorf("Expected stream index 1 (default), got index %d", best.Index)
	}
}

func TestIsVideoStreamBetter(t *testing.T) {
	stream1080p := &FFProbeStream{Width: 1920, Height: 1080, BitRate: "4000000"}
	stream720p := &FFProbeStream{Width: 1280, Height: 720, BitRate: "2000000"}
	stream720pHighBitrate := &FFProbeStream{Width: 1280, Height: 720, BitRate: "6000000"}

	// Higher resolution should win
	if !isVideoStreamBetter(stream1080p, stream720p) {
		t.Error("1080p should be better than 720p")
	}

	// Same resolution, higher bitrate should win
	if !isVideoStreamBetter(stream720pHighBitrate, stream720p) {
		t.Error("Higher bitrate should be better for same resolution")
	}

	// Lower resolution should lose even with higher bitrate
	if isVideoStreamBetter(stream720pHighBitrate, stream1080p) {
		t.Error("720p should not be better than 1080p regardless of bitrate")
	}
}

func TestIsAudioStreamBetter(t *testing.T) {
	stereo := &FFProbeStream{Channels: 2, BitRate: "128000"}
	surround := &FFProbeStream{Channels: 6, BitRate: "448000"}
	stereoHighBitrate := &FFProbeStream{Channels: 2, BitRate: "320000"}

	// More channels should win
	if !isAudioStreamBetter(surround, stereo) {
		t.Error("6 channels should be better than 2 channels")
	}

	// Same channels, higher bitrate should win
	if !isAudioStreamBetter(stereoHighBitrate, stereo) {
		t.Error("Higher bitrate should be better for same channel count")
	}

	// Fewer channels should lose even with higher bitrate
	if isAudioStreamBetter(stereoHighBitrate, surround) {
		t.Error("2 channels should not be better than 6 channels regardless of bitrate")
	}
}

func TestParseBitrate(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"1000000", 1000000},
		{"128000", 128000},
		{"", 0},
		{"invalid", 0},
		{"123abc", 0},
	}

	for _, tt := range tests {
		result := parseBitrate(tt.input)
		if result != tt.expected {
			t.Errorf("parseBitrate(%s) = %d, expected %d", tt.input, result, tt.expected)
		}
	}
}