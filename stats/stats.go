package stats

var (
	requests             = make(map[string]int)
	downloadErrors       int
	unrecognizedCommands int
)

func AddRequest(username string) {
	requests[username]++
}

func AddDownloadError() {
	downloadErrors++
}

func AddUnrecognizedCommand() {
	unrecognizedCommands++
}

type Stats struct {
	Requests             map[string]int `json:"requests"`
	DownloadErrors       int            `json:"download_errors"`
	UnrecognizedCommands int            `json:"unrecognized_commands"`
}

func GetStats() *Stats {
	return &Stats{
		Requests:             requests,
		DownloadErrors:       downloadErrors,
		UnrecognizedCommands: unrecognizedCommands,
	}
}
