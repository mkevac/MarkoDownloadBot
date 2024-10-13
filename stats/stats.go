package stats

import "log"

type Stats struct {
	VideoRequests        map[string]int `json:"video_requests"`
	AudioRequests        map[string]int `json:"audio_requests"`
	DownloadErrors       map[string]int `json:"download_errors"`
	UnrecognizedCommands map[string]int `json:"unrecognized_commands"`
}

func AddVideoRequest(username string) {
	err := addEvent(username, "video_request")
	if err != nil {
		log.Printf("Error adding video request event to database: %v", err)
	}
}

func AddAudioRequest(username string) {
	err := addEvent(username, "audio_request")
	if err != nil {
		log.Printf("Error adding audio request event to database: %v", err)
	}
}

func AddDownloadError(username string) {
	err := addEvent(username, "download_error")
	if err != nil {
		log.Printf("Error adding download error event to database: %v", err)
	}
}

func AddUnrecognizedCommand(username string) {
	err := addEvent(username, "unrecognized_command")
	if err != nil {
		log.Printf("Error adding unrecognized command event to database: %v", err)
	}
}

func GetStats(period string) *Stats {
	stats, err := getStats(period)
	if err != nil {
		log.Printf("Error getting stats from database: %v", err)
		return &Stats{
			VideoRequests:        make(map[string]int),
			AudioRequests:        make(map[string]int),
			DownloadErrors:       make(map[string]int),
			UnrecognizedCommands: make(map[string]int),
		}
	}
	return stats
}
