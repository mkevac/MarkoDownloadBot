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

type Video struct {
	Width    int            `json:"width"`
	Height   int            `json:"height"`
	Duration CustomDuration `json:"duration_string"`
	VCodec   string         `json:"vcodec"`
	Path     string
	FileName string

	randomName string
	tmpDir     string
	url        string
	parsedUrl  *url.URL
	user       string
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

func DownloadVideo(videoUrl string, user string, tmpDir string) (*Video, error) {
	res := &Video{
		tmpDir:     tmpDir,
		url:        videoUrl,
		randomName: uuid.New().String(),
		user:       user,
	}

	u, err := url.Parse(videoUrl)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid URL")
	}
	res.parsedUrl = u

	commandString := res.getCommandString()

	log.Printf("[%s]: executing command: '%s'", res.user, strings.Join(commandString, " "))

	cmd := exec.Command(commandString[0], commandString[1:]...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Printf("Output: %s\n", out.String())
		log.Printf("Error: %s\n", stderr.String())
		return nil, fmt.Errorf("command execution failed with %s", err)
	}

	res.Path = filepath.Join(tmpDir, res.randomName+".mp4")

	if err := res.populateInfo(); err != nil {
		return nil, fmt.Errorf("error populating info: %s", err)
	}

	if strings.HasPrefix(res.VCodec, "av01") {
		if err := res.convert(); err != nil {
			return nil, fmt.Errorf("error converting video: %s", err)
		}
	}

	return res, nil
}

func (video *Video) Delete() error {
	if err := os.Remove(video.Path); err != nil {
		return fmt.Errorf("error deleting file: %s", err)
	}

	return nil
}

func (video *Video) convert() error {
	// we need to use ffmpeg to do some conversions
	// this is the command to do that:
	// ffmpeg -i downloaded_video.mp4 -c:v libx264 -c:a aac -strict -2 -movflags +faststart -vf "scale=1080:-2" -b:v 5000k output_video.mp4

	outputPath := filepath.Join(video.tmpDir, video.randomName+"_converted.mp4")

	var cmdSlice []string

	cmdSlice = append(cmdSlice, "ffmpeg")
	cmdSlice = append(cmdSlice, "-i")
	cmdSlice = append(cmdSlice, video.Path)
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

	log.Printf("[%s]: executing command: '%s'", video.user, strings.Join(cmdSlice, " "))

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

	video.Path = outputPath
	video.FileName = video.randomName + "_converted.mp4"

	if err := os.Remove(filepath.Join(video.tmpDir, video.randomName+".mp4")); err != nil {
		log.Printf("error deleting original file: %s", err)
	}

	return nil
}

func (video *Video) populateInfo() error {
	jsonPath := filepath.Join(video.tmpDir, video.randomName+".info.json")

	buf, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("error reading json file '%s': %s", jsonPath, err)
	}

	if err := json.Unmarshal(buf, video); err != nil {
		return fmt.Errorf("error parsing json content: %s", err)
	}

	if err := os.Remove(jsonPath); err != nil {
		return fmt.Errorf("error deleting json file '%s': %s", jsonPath, err)
	}

	return nil
}

func (video *Video) getCommandString() []string {
	var res []string

	res = append(res, "yt-dlp")

	res = append(res, "--recode-video")
	res = append(res, "mp4")

	res = append(res, "--write-info-json")

	if video.parsedUrl.Host == "www.youtube.com" || video.parsedUrl.Host == "youtube.com" || video.parsedUrl.Host == "youtu.be" {
		res = append(res, "-f")
		res = append(res, "bv[filesize<=1700M]+ba[filesize<=300M]")
		res = append(res, "-S")
		res = append(res, "ext,res:720")
	}

	res = append(res, "-o")
	res = append(res, tmpDir+"/"+video.randomName+".%(ext)s")
	res = append(res, video.url)

	return res
}
