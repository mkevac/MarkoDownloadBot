package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"

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

	randomName    string
	tmpDir        string
	url           string
	parsedUrl     *url.URL
	logTag        string
	cookiesFile   string
	audioOnly     bool
	playlistIndex int // 0 means single item; >0 means carousel item index for --playlist-items
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

	if !audioOnly {
		if err := res.checkMediaBeforeDownload(ctx); err != nil {
			return nil, err
		}
	}

	err = res.executeDownload(ctx, false, onProgress)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Printf("[%s]: First download attempt failed: %s", res.logTag, err)

		log.Printf("[%s]: Retrying with simplified arguments", res.logTag)
		err = res.executeDownload(ctx, true, onProgress)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("both download attempts failed: %w", err)
		}
	}

	res.Path, err = res.findDownloadedMediaPath()
	if err != nil {
		if audioOnly {
			res.Path = filepath.Join(tmpDir, res.randomName+".mp3")
		} else {
			res.Path = filepath.Join(tmpDir, res.randomName+".mp4")
		}
		return nil, err
	}

	if err := res.enforceDownloadedFileSizeLimit(); err != nil {
		res.deleteDownloadedFiles()
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
