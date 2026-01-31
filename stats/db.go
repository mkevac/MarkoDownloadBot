package stats

import (
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

var (
	db      *sql.DB
	once    sync.Once
	dirBase string
)

// Init initializes the stats package with the given base directory
func Init(dir string) {
	dirBase = dir
}

func initDB() {
	if dirBase == "" {
		log.Fatal("stats: dirBase not set. Call stats.Init() before using the package.")
	}

	dbPath := filepath.Join(dirBase, "stats.db")

	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT,
			event_type TEXT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatalf("Error creating events table: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			chat_id INTEGER PRIMARY KEY,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			is_active BOOLEAN DEFAULT 1,
			last_interaction DATETIME DEFAULT CURRENT_TIMESTAMP,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatalf("Error creating users table: %v", err)
	}
}

func getDB() *sql.DB {
	once.Do(initDB)
	return db
}

func addEvent(username, eventType string) error {
	_, err := getDB().Exec("INSERT INTO events (username, event_type) VALUES (?, ?)", username, eventType)
	return err
}

func registerUser(chatID int64, username, firstName, lastName string) error {
	_, err := getDB().Exec(`
		INSERT INTO users (chat_id, username, first_name, last_name, is_active, last_interaction, created_at)
		VALUES (?, ?, ?, ?, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(chat_id) DO UPDATE SET
			username = excluded.username,
			first_name = excluded.first_name,
			last_name = excluded.last_name,
			is_active = 1,
			last_interaction = CURRENT_TIMESTAMP
	`, chatID, username, firstName, lastName)
	return err
}

func getAllUserChatIDs() ([]int64, error) {
	rows, err := getDB().Query("SELECT chat_id FROM users WHERE is_active = 1 ORDER BY last_interaction DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chatIDs []int64
	for rows.Next() {
		var chatID int64
		if err := rows.Scan(&chatID); err != nil {
			return nil, err
		}
		chatIDs = append(chatIDs, chatID)
	}
	return chatIDs, nil
}

func getUserCount() (int, error) {
	var count int
	err := getDB().QueryRow("SELECT COUNT(*) FROM users WHERE is_active = 1").Scan(&count)
	return count, err
}

func markUserInactive(chatID int64) error {
	_, err := getDB().Exec("UPDATE users SET is_active = 0 WHERE chat_id = ?", chatID)
	return err
}

func getStats(period string) (*Stats, error) {
	stats := &Stats{
		VideoRequests:        make(map[string]int),
		AudioRequests:        make(map[string]int),
		DownloadErrors:       make(map[string]int),
		UnrecognizedCommands: make(map[string]int),
	}

	var timeConstraint string
	switch period {
	case "day":
		timeConstraint = "AND timestamp >= datetime('now', '-1 day')"
	case "week":
		timeConstraint = "AND timestamp >= datetime('now', '-7 days')"
	case "month":
		timeConstraint = "AND timestamp >= datetime('now', '-1 month')"
	default:
		timeConstraint = ""
	}

	query := fmt.Sprintf(`
		SELECT username, 
			   SUM(CASE WHEN event_type = 'video_request' THEN 1 ELSE 0 END) as video_requests,
			   SUM(CASE WHEN event_type = 'audio_request' THEN 1 ELSE 0 END) as audio_requests,
			   SUM(CASE WHEN event_type = 'download_error' THEN 1 ELSE 0 END) as download_errors,
			   SUM(CASE WHEN event_type = 'unrecognized_command' THEN 1 ELSE 0 END) as unrecognized_commands
		FROM events
		WHERE 1=1 %s
		GROUP BY username
	`, timeConstraint)

	rows, err := getDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var username string
		var videoRequests, audioRequests, downloadErrors, unrecognizedCommands int
		err := rows.Scan(&username, &videoRequests, &audioRequests, &downloadErrors, &unrecognizedCommands)
		if err != nil {
			return nil, err
		}

		stats.VideoRequests[username] = videoRequests
		stats.AudioRequests[username] = audioRequests
		stats.DownloadErrors[username] = downloadErrors
		stats.UnrecognizedCommands[username] = unrecognizedCommands
	}

	return stats, nil
}
