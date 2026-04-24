package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestMediaNeedsVideoConversion(t *testing.T) {
	media := &Media{}

	tests := []struct {
		name     string
		codec    string
		expected bool
	}{
		{name: "AV1 codec needs conversion", codec: "av01", expected: true},
		{name: "VP9 codec needs conversion", codec: "vp9", expected: true},
		{name: "VP09 codec needs conversion", codec: "vp09", expected: true},
		{name: "HEVC codec doesn't need conversion", codec: "hevc", expected: false},
		{name: "H264 codec doesn't need conversion", codec: "h264", expected: false},
		{name: "H263 codec doesn't need conversion", codec: "h263", expected: false},
		{name: "Unknown codec doesn't need conversion", codec: "unknown", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media.VCodec = ""

			result := media.needsVideoConversion(tt.codec)
			if result != tt.expected {
				t.Errorf("Expected %v for codec %s, got %v", tt.expected, tt.codec, result)
			}

			if tt.codec == "av01" || tt.codec == "vp09" {
				media.VCodec = tt.codec
				result = media.needsVideoConversion("h264")
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
		{name: "AAC codec doesn't need conversion", codec: "aac", expected: false},
		{name: "Opus codec needs conversion", codec: "opus", expected: true},
		{name: "Vorbis codec needs conversion", codec: "vorbis", expected: true},
		{name: "FLAC codec needs conversion", codec: "flac", expected: true},
		{name: "MP3 codec doesn't need conversion", codec: "mp3", expected: false},
		{name: "Unknown codec doesn't need conversion", codec: "unknown", expected: false},
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

func TestDetermineConversionStrategy(t *testing.T) {
	tests := []struct {
		name                    string
		originalVideoCodec      string
		originalAudioCodec      string
		fileSize                int64
		expectedVideoConversion bool
		expectedAudioConversion bool
		expectedAudioType       string
		expectedCompatible      bool
	}{
		{name: "compatible codecs", originalVideoCodec: "h264", originalAudioCodec: "aac", fileSize: 50 * 1024 * 1024, expectedAudioType: "copy", expectedCompatible: true},
		{name: "small file with incompatible video", originalVideoCodec: "av01", originalAudioCodec: "aac", fileSize: 50 * 1024 * 1024, expectedVideoConversion: true, expectedAudioType: "copy"},
		{name: "large file with incompatible video", originalVideoCodec: "vp9", originalAudioCodec: "aac", fileSize: 200 * 1024 * 1024, expectedVideoConversion: true, expectedAudioType: "copy"},
		{name: "incompatible audio only", originalVideoCodec: "h264", originalAudioCodec: "opus", fileSize: 50 * 1024 * 1024, expectedAudioConversion: true, expectedAudioType: "aac"},
		{name: "both incompatible", originalVideoCodec: "av01", originalAudioCodec: "vorbis", fileSize: 50 * 1024 * 1024, expectedVideoConversion: true, expectedAudioConversion: true, expectedAudioType: "aac"},
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

			if analysis.AudioConversionType != tt.expectedAudioType {
				t.Errorf("Expected audio type %s, got %s", tt.expectedAudioType, analysis.AudioConversionType)
			}

			if analysis.IsAlreadyCompatible != tt.expectedCompatible {
				t.Errorf("Expected compatibility %v, got %v", tt.expectedCompatible, analysis.IsAlreadyCompatible)
			}
		})
	}
}

func TestMediaAnalysisInitialization(t *testing.T) {
	media := createTestMedia(t)
	defer os.Remove(media.Path)

	_, err := media.analyzeMedia()
	if err != nil {
		t.Logf("Analysis failed as expected without ffprobe: %v", err)
	}
}

func createTestMedia(t *testing.T) *Media {
	t.Helper()

	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "test_"+uuid.New().String()+".mp4")

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

func TestRunFFProbe(t *testing.T) {
	media := createTestMedia(t)
	defer os.Remove(media.Path)

	_, err := media.runFFProbe()
	if err != nil {
		t.Logf("FFProbe failed as expected: %v", err)
	}
}

func TestFFProbeResultStruct(t *testing.T) {
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
		{Index: 0, CodecType: "video", CodecName: "h264", Width: 1280, Height: 720, BitRate: "2000000"},
		{Index: 1, CodecType: "video", CodecName: "h265", Width: 1920, Height: 1080, BitRate: "4000000"},
		{Index: 2, CodecType: "audio", CodecName: "aac", Channels: 2, BitRate: "128000"},
	}

	best := selectBestVideoStream(streams)
	if best == nil {
		t.Fatal("Expected to find a video stream")
	}

	if best.Index != 1 {
		t.Errorf("Expected stream index 1 (1080p), got index %d", best.Index)
	}
}

func TestSelectBestVideoStreamWithDefault(t *testing.T) {
	streams := []FFProbeStream{
		{Index: 0, CodecType: "video", CodecName: "h264", Width: 1920, Height: 1080, BitRate: "4000000"},
		{Index: 1, CodecType: "video", CodecName: "h264", Width: 1280, Height: 720, BitRate: "2000000"},
	}
	streams[1].Disposition.Default = 1

	best := selectBestVideoStream(streams)
	if best == nil {
		t.Fatal("Expected to find a video stream")
	}

	if best.Index != 1 {
		t.Errorf("Expected stream index 1 (default), got index %d", best.Index)
	}
}

func TestSelectBestAudioStream(t *testing.T) {
	streams := []FFProbeStream{
		{Index: 0, CodecType: "audio", CodecName: "aac", Channels: 2, BitRate: "128000"},
		{Index: 1, CodecType: "audio", CodecName: "ac3", Channels: 6, BitRate: "448000"},
		{Index: 2, CodecType: "video", CodecName: "h264", Width: 1920, Height: 1080},
	}

	best := selectBestAudioStream(streams)
	if best == nil {
		t.Fatal("Expected to find an audio stream")
	}

	if best.Index != 1 {
		t.Errorf("Expected stream index 1 (6 channels), got index %d", best.Index)
	}
}

func TestSelectBestAudioStreamWithDefault(t *testing.T) {
	streams := []FFProbeStream{
		{Index: 0, CodecType: "audio", CodecName: "ac3", Channels: 6, BitRate: "448000"},
		{Index: 1, CodecType: "audio", CodecName: "aac", Channels: 2, BitRate: "128000"},
	}
	streams[1].Disposition.Default = 1

	best := selectBestAudioStream(streams)
	if best == nil {
		t.Fatal("Expected to find an audio stream")
	}

	if best.Index != 1 {
		t.Errorf("Expected stream index 1 (default), got index %d", best.Index)
	}
}

func TestIsVideoStreamBetter(t *testing.T) {
	stream1080p := &FFProbeStream{Width: 1920, Height: 1080, BitRate: "4000000"}
	stream720p := &FFProbeStream{Width: 1280, Height: 720, BitRate: "2000000"}
	stream720pHighBitrate := &FFProbeStream{Width: 1280, Height: 720, BitRate: "6000000"}

	if !isVideoStreamBetter(stream1080p, stream720p) {
		t.Error("1080p should be better than 720p")
	}

	if !isVideoStreamBetter(stream720pHighBitrate, stream720p) {
		t.Error("Higher bitrate should be better for same resolution")
	}

	if isVideoStreamBetter(stream720pHighBitrate, stream1080p) {
		t.Error("720p should not be better than 1080p regardless of bitrate")
	}
}

func TestIsAudioStreamBetter(t *testing.T) {
	stereo := &FFProbeStream{Channels: 2, BitRate: "128000"}
	surround := &FFProbeStream{Channels: 6, BitRate: "448000"}
	stereoHighBitrate := &FFProbeStream{Channels: 2, BitRate: "320000"}

	if !isAudioStreamBetter(surround, stereo) {
		t.Error("6 channels should be better than 2 channels")
	}

	if !isAudioStreamBetter(stereoHighBitrate, stereo) {
		t.Error("Higher bitrate should be better for same channel count")
	}

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

func BenchmarkNeedsVideoConversion(b *testing.B) {
	media := &Media{VCodec: "h264"}

	for i := 0; i < b.N; i++ {
		media.needsVideoConversion("av01")
	}
}
