package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// MediaAnalysis contains analysis results for intelligent conversion.
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

// FFProbeResult represents the JSON output from ffprobe.
type FFProbeResult struct {
	Format struct {
		BitRate    string `json:"bit_rate"`
		FormatName string `json:"format_name"`
	} `json:"format"`
	Streams []FFProbeStream `json:"streams"`
}

// FFProbeStream represents a single stream from ffprobe output.
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

func (media *Media) runFFProbe(ctx context.Context) (*FFProbeResult, error) {
	result, err := runCommandWithTimeout(ctx, defaultFFProbeTimeout, "ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", media.Path)
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w: %s", err, result.stderr)
	}

	var probeResult FFProbeResult
	if err := json.Unmarshal([]byte(result.stdout), &probeResult); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}
	log.Printf("[%s]: ffprobe completed in %s", media.logTag, formatElapsed(result.elapsed))

	return &probeResult, nil
}

func (media *Media) analyzeMedia(ctx context.Context) (*MediaAnalysis, error) {
	analysis := &MediaAnalysis{
		OriginalVideoCodec: media.VCodec,
		OriginalAudioCodec: media.ACodec,
	}

	fileInfo, err := os.Stat(media.Path)
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}
	analysis.OriginalFileSize = fileInfo.Size()

	probeResult, err := media.runFFProbe(ctx)
	if err != nil {
		return nil, err
	}

	if probeResult.Format.BitRate != "" {
		if bitrate, err := strconv.ParseInt(probeResult.Format.BitRate, 10, 64); err == nil {
			analysis.OriginalBitrate = bitrate
		}
	}

	bestVideoStream := selectBestVideoStream(probeResult.Streams)
	if bestVideoStream != nil {
		analysis.OriginalVideoCodec = bestVideoStream.CodecName
	}

	bestAudioStream := selectBestAudioStream(probeResult.Streams)
	if bestAudioStream != nil {
		analysis.OriginalAudioCodec = bestAudioStream.CodecName
	}

	log.Printf("[%s]: media analysis: video=%s audio=%s size=%.1fMB bitrate=%dbps",
		media.logTag,
		analysis.OriginalVideoCodec,
		analysis.OriginalAudioCodec,
		bytesToMB(analysis.OriginalFileSize),
		analysis.OriginalBitrate,
	)

	return analysis, nil
}

func (media *Media) determineConversionStrategy(analysis *MediaAnalysis) {
	analysis.NeedsVideoConversion = media.needsVideoConversion(analysis.OriginalVideoCodec)
	analysis.NeedsAudioConversion = media.needsAudioConversion(analysis.OriginalAudioCodec)

	if analysis.NeedsAudioConversion {
		analysis.AudioConversionType = "aac"
	} else {
		analysis.AudioConversionType = "copy"
	}

	analysis.IsAlreadyCompatible = !analysis.NeedsVideoConversion && !analysis.NeedsAudioConversion
}

func (media *Media) needsVideoConversion(codecName string) bool {
	if strings.HasPrefix(codecName, "av01") {
		return true
	}

	if strings.HasPrefix(codecName, "vp9") || strings.HasPrefix(codecName, "vp09") {
		return true
	}

	if strings.HasPrefix(media.VCodec, "av01") || strings.HasPrefix(media.VCodec, "vp09") {
		return true
	}

	return false
}

func (media *Media) needsAudioConversion(codecName string) bool {
	switch codecName {
	case "aac":
		return false
	case "opus", "vorbis", "flac":
		return true
	default:
		return false
	}
}

func selectBestVideoStream(streams []FFProbeStream) *FFProbeStream {
	var videoStreams []*FFProbeStream

	for i := range streams {
		if streams[i].CodecType == "video" {
			videoStreams = append(videoStreams, &streams[i])
		}
	}

	if len(videoStreams) == 0 {
		return nil
	}

	for _, stream := range videoStreams {
		if stream.Disposition.Default == 1 {
			return stream
		}
	}

	bestStream := videoStreams[0]
	for _, stream := range videoStreams[1:] {
		if isVideoStreamBetter(stream, bestStream) {
			bestStream = stream
		}
	}

	return bestStream
}

func selectBestAudioStream(streams []FFProbeStream) *FFProbeStream {
	var audioStreams []*FFProbeStream

	for i := range streams {
		if streams[i].CodecType == "audio" {
			audioStreams = append(audioStreams, &streams[i])
		}
	}

	if len(audioStreams) == 0 {
		return nil
	}

	for _, stream := range audioStreams {
		if stream.Disposition.Default == 1 {
			return stream
		}
	}

	bestStream := audioStreams[0]
	for _, stream := range audioStreams[1:] {
		if isAudioStreamBetter(stream, bestStream) {
			bestStream = stream
		}
	}

	return bestStream
}

func isVideoStreamBetter(stream1, stream2 *FFProbeStream) bool {
	resolution1 := stream1.Width * stream1.Height
	resolution2 := stream2.Width * stream2.Height

	if resolution1 != resolution2 {
		return resolution1 > resolution2
	}

	return parseBitrate(stream1.BitRate) > parseBitrate(stream2.BitRate)
}

func isAudioStreamBetter(stream1, stream2 *FFProbeStream) bool {
	if stream1.Channels != stream2.Channels {
		return stream1.Channels > stream2.Channels
	}

	return parseBitrate(stream1.BitRate) > parseBitrate(stream2.BitRate)
}

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
