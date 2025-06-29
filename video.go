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

	randomName  string
	tmpDir      string
	url         string
	parsedUrl   *url.URL
	user        string
	cookiesFile string
	audioOnly   bool
}

type CustomDuration int

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
			return nil, fmt.Errorf("both download attempts failed: %s", err)
		}
	}

	if audioOnly {
		res.Path = filepath.Join(tmpDir, res.randomName+".mp3")
	} else {
		res.Path = filepath.Join(tmpDir, res.randomName+".mp4")
	}

	if err := res.populateInfo(); err != nil {
		return nil, fmt.Errorf("error populating info: %s", err)
	}

	if audioOnly {
		log.Printf("[%s]: audio format '%s'", res.user, res.ACodec)
	} else {
		log.Printf("[%s]: video format '%s'", res.user, res.VCodec)

		if strings.HasPrefix(res.VCodec, "av01") || strings.HasPrefix(res.VCodec, "vp09") {
			log.Printf("[%s]: video codec is not supported by iOS, converting video", res.user)
			if err := res.convert(); err != nil {
				return nil, fmt.Errorf("error converting video: %s", err)
			}
		}
	}

	return res, nil
}

func (media *Media) Delete() error {
	if err := os.Remove(media.Path); err != nil {
		return fmt.Errorf("error deleting file: %s", err)
	}

	return nil
}

func (media *Media) GetFileSize() (int64, error) {
	info, err := os.Stat(media.Path)
	if err != nil {
		return 0, fmt.Errorf("error getting file info: %s", err)
	}
	return info.Size(), nil
}

func (media *Media) convert() error {
	// we need to use ffmpeg to do some conversions
	// this is the command to do that:
	// ffmpeg -i downloaded_video.mp4 -c:v libx264 -c:a aac -strict -2 -movflags +faststart -vf "scale=1080:-2" -b:v 5000k output_video.mp4

	outputPath := filepath.Join(media.tmpDir, media.randomName+"_converted.mp4")

	var cmdSlice []string

	cmdSlice = append(cmdSlice, "ffmpeg")
	cmdSlice = append(cmdSlice, "-i")
	cmdSlice = append(cmdSlice, media.Path)
	cmdSlice = append(cmdSlice, "-c:v")
	cmdSlice = append(cmdSlice, "libx264")
	cmdSlice = append(cmdSlice, "-c:a")
	cmdSlice = append(cmdSlice, "aac")
	cmdSlice = append(cmdSlice, "-strict")
	cmdSlice = append(cmdSlice, "-2")
	cmdSlice = append(cmdSlice, "-movflags")
	cmdSlice = append(cmdSlice, "+faststart")
	cmdSlice = append(cmdSlice, "-vf")
	cmdSlice = append(cmdSlice, "scale=1080:-2")
	cmdSlice = append(cmdSlice, "-b:v")
	cmdSlice = append(cmdSlice, "5000k")
	cmdSlice = append(cmdSlice, outputPath)

	log.Printf("[%s]: executing command: '%s'", media.user, strings.Join(cmdSlice, " "))

	cmd := exec.Command(cmdSlice[0], cmdSlice[1:]...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Printf("Output: %s\n", out.String())
		log.Printf("Error: %s\n", stderr.String())
		return err
	}

	media.Path = outputPath
	media.FileName = media.randomName + "_converted.mp4"

	if err := os.Remove(filepath.Join(media.tmpDir, media.randomName+".mp4")); err != nil {
		log.Printf("error deleting original file: %s", err)
	}

	return nil
}

func (media *Media) populateInfo() error {
	jsonPath := filepath.Join(media.tmpDir, media.randomName+".info.json")

	buf, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("error reading json file '%s': %s", jsonPath, err)
	}

	if err := json.Unmarshal(buf, media); err != nil {
		return fmt.Errorf("error parsing json content: %s", err)
	}

	if err := os.Remove(jsonPath); err != nil {
		return fmt.Errorf("error deleting json file '%s': %s", jsonPath, err)
	}

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
		return fmt.Errorf("command execution failed with %s", err)
	}

	return nil
}
