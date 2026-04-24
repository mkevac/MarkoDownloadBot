package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func (media *Media) convertIntelligent(analysis *MediaAnalysis) error {
	var outputFileName string
	if media.FileName != "" {
		baseName := strings.TrimSuffix(media.FileName, filepath.Ext(media.FileName))
		outputFileName = baseName + "_converted.mp4"
	} else {
		outputFileName = media.randomName + "_converted.mp4"
	}
	outputPath := filepath.Join(media.tmpDir, outputFileName)

	cmdSlice := []string{"ffmpeg", "-i", media.Path}

	if analysis.NeedsVideoConversion {
		cmdSlice = append(cmdSlice,
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-profile:v", "baseline",
			"-pix_fmt", "yuv420p",
			"-crf", "23",
			"-maxrate", "4.5M",
			"-vf", "scale=min(1280\\,iw):-2",
		)
		log.Printf("[%s]: using H.264 with CRF 23 and smart scaling", media.user)
	} else {
		cmdSlice = append(cmdSlice, "-c:v", "copy")
		log.Printf("[%s]: copying video stream (no conversion needed)", media.user)
	}

	if analysis.NeedsAudioConversion {
		cmdSlice = append(cmdSlice, "-c:a", "aac", "-ac", "2")
		log.Printf("[%s]: converting audio to AAC stereo", media.user)
	} else {
		cmdSlice = append(cmdSlice, "-c:a", "copy")
		log.Printf("[%s]: copying audio stream (no conversion needed)", media.user)
	}

	cmdSlice = append(cmdSlice, "-movflags", "+faststart", outputPath)

	log.Printf("[%s]: executing intelligent conversion: '%s'", media.user, strings.Join(cmdSlice, " "))

	result, err := runCommandWithTimeout(ffmpegTimeout(), cmdSlice[0], cmdSlice[1:]...)
	if err != nil {
		log.Printf("FFmpeg Output: %s\n", result.stdout)
		log.Printf("FFmpeg Error: %s\n", result.stderr)
		log.Printf("[%s]: ffmpeg conversion failed after %s", media.user, formatElapsed(result.elapsed))
		return fmt.Errorf("ffmpeg conversion failed: %w", err)
	}
	log.Printf("[%s]: ffmpeg conversion command completed in %s", media.user, formatElapsed(result.elapsed))

	newFileInfo, _ := os.Stat(outputPath)
	if newFileInfo != nil {
		compressionRatio := float64(newFileInfo.Size()) / float64(analysis.OriginalFileSize)
		log.Printf("[%s]: conversion complete - size ratio: %.2f (%.1fMB -> %.1fMB)",
			media.user, compressionRatio,
			float64(analysis.OriginalFileSize)/(1024*1024),
			float64(newFileInfo.Size())/(1024*1024))
	}

	inputPath := media.Path
	if err := os.Remove(inputPath); err != nil {
		log.Printf("error deleting original file: %s", err)
	}

	media.Path = outputPath
	media.FileName = outputFileName

	return nil
}
