package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"

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
	Title    string `json:"title"`

	randomName           string
	tmpDir               string
	url                  string
	parsedUrl            *url.URL
	logTag               string
	cookiesFile          string
	audioOnly            bool
	selectedMaxDimension int
	reducedMaxDimension  int
	playlistIndex        int // 0 means single item; >0 means carousel item index for --playlist-items
}

func DownloadMedia(ctx context.Context, mediaUrl string, logTag string, tmpDir string, cookiesFile string, audioOnly bool, onProgress func(progressUpdate)) (*Media, error) {
	res := &Media{
		tmpDir:      tmpDir,
		url:         mediaUrl,
		randomName:  uuid.New().String(),
		logTag:      logTag,
		cookiesFile: cookiesFile,
		audioOnly:   audioOnly,
	}

	u, err := url.Parse(mediaUrl)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid URL")
	}
	res.parsedUrl = u

	if err := res.downloadWithSizeRetry(ctx, onProgress); err != nil {
		return nil, err
	}

	if err := res.populateInfo(); err != nil {
		return nil, fmt.Errorf("error populating info: %w", err)
	}

	if err := res.renameToReadableName(); err != nil {
		log.Printf("[%s]: warning - could not rename to readable name: %s, keeping UUID name", res.logTag, err)
	}

	if audioOnly {
		log.Printf("[%s]: audio format '%s'", res.logTag, res.ACodec)
		return res, nil
	}

	log.Printf("[%s]: video format '%s'", res.logTag, res.VCodec)

	analysis, err := res.analyzeMedia(ctx)
	if err != nil {
		log.Printf("[%s]: warning - could not analyze media: %s, skipping conversion", res.logTag, err)
		return res, nil
	}

	res.determineConversionStrategy(analysis)
	if analysis.IsAlreadyCompatible {
		log.Printf("[%s]: media is already iPhone compatible, no conversion needed", res.logTag)
		return res, nil
	}

	videoAction := "copy"
	if analysis.NeedsVideoConversion {
		videoAction = "h264"
	}
	log.Printf("[%s]: media needs conversion - video: %s, audio: %s", res.logTag, videoAction, analysis.AudioConversionType)
	if err := res.convertIntelligent(ctx, analysis); err != nil {
		return nil, fmt.Errorf("error converting video: %w", err)
	}

	return res, nil
}

// downloadWithSizeRetry permits exactly one resolution reduction for oversized videos.
func (media *Media) downloadWithSizeRetry(ctx context.Context, onProgress func(progressUpdate)) error {
	for {
		err := media.downloadAttempt(ctx, onProgress)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, errMediaTooLarge) {
			return err
		}
		media.deleteDownloadedFiles()
		if !media.reduceResolution() {
			return err
		}
		log.Printf("[%s]: media exceeds %s; retrying with both dimensions <= %d", media.logTag, maxMediaFileSize(), media.reducedMaxDimension)
	}
}

func (media *Media) reduceResolution() bool {
	if media.audioOnly || media.reducedMaxDimension > 0 || media.selectedMaxDimension <= 0 {
		return false
	}
	for _, dimension := range []int{1920, 1280, 854, 640, 426, 256} {
		if dimension < media.selectedMaxDimension {
			media.reducedMaxDimension = dimension
			return true
		}
	}
	return false
}

func (media *Media) downloadAttempt(ctx context.Context, onProgress func(progressUpdate)) error {
	if !media.audioOnly {
		if err := media.checkMediaBeforeDownload(ctx); err != nil {
			return err
		}
	}
	err := media.executeDownload(ctx, false, onProgress)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errMediaTooLarge) || media.reducedMaxDimension > 0 {
			return err
		}
		log.Printf("[%s]: First download attempt failed: %s; retrying with simplified arguments", media.logTag, err)
		err = media.executeDownload(ctx, true, onProgress)
		if err != nil {
			return fmt.Errorf("both download attempts failed: %w", err)
		}
	}
	media.Path, err = media.findDownloadedMediaPath()
	if err != nil {
		return err
	}
	return media.enforceDownloadedFileSizeLimit()
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
