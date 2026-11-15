package stats

import (
	"database/sql"
	"errors"
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

// Init opens and migrates the stats database in the given base directory.
func Init(dir string) {
	dirBase = dir
	getDB()
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

	if err := initializeSchema(db); err != nil {
		log.Fatalf("Error initializing stats database: %v", err)
	}
}

// initializeSchema adds nullable IDs without attributing legacy events by name.
func initializeSchema(database *sql.DB) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER,
			username TEXT,
			event_type TEXT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	var hasUserID int
	if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'user_id'").Scan(&hasUserID); err != nil {
		return err
	}
	if hasUserID == 0 {
		if _, err := tx.Exec("ALTER TABLE events ADD COLUMN user_id INTEGER"); err != nil {
			return err
		}
	}

	_, err = tx.Exec(`
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
		return err
	}
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS user_profiles (
			user_id INTEGER PRIMARY KEY,
			username TEXT,
			first_name TEXT,
			last_name TEXT
		)
	`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func getDB() *sql.DB {
	once.Do(initDB)
	return db
}

func addEvent(userID int64, username, eventType string) error {
	if userID <= 0 {
		return errors.New("stats: a positive sender user ID is required")
	}
	_, err := getDB().Exec("INSERT INTO events (user_id, username, event_type) VALUES (?, ?, ?)", userID, username, eventType)
	return err
}

func registerUser(chatID, userID int64, username, firstName, lastName string) error {
	if userID <= 0 {
		return errors.New("stats: a positive sender user ID is required")
	}
	tx, err := getDB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
		INSERT INTO users (chat_id, username, first_name, last_name, is_active, last_interaction, created_at)
		VALUES (?, ?, ?, ?, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(chat_id) DO UPDATE SET
			username = excluded.username,
			first_name = excluded.first_name,
			last_name = excluded.last_name,
			is_active = 1,
			last_interaction = CURRENT_TIMESTAMP
	`, chatID, username, firstName, lastName)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO user_profiles (user_id, username, first_name, last_name)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			username = excluded.username,
			first_name = excluded.first_name,
			last_name = excluded.last_name
	`, userID, username, firstName, lastName)
	if err != nil {
		return err
	}
	return tx.Commit()
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
	stats := &Stats{}

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
		SELECT e.user_id,
			   CASE WHEN e.user_id IS NULL THEN COALESCE(e.username, '') ELSE COALESCE(u.username, '') END,
			   COALESCE(u.first_name, ''), COALESCE(u.last_name, ''),
			   SUM(CASE WHEN event_type = 'video_request' THEN 1 ELSE 0 END) as video_requests,
			   SUM(CASE WHEN event_type = 'audio_request' THEN 1 ELSE 0 END) as audio_requests,
			   SUM(CASE WHEN event_type = 'download_error' THEN 1 ELSE 0 END) as download_errors,
			   SUM(CASE WHEN event_type = 'unrecognized_command' THEN 1 ELSE 0 END) as unrecognized_commands
		FROM events e
		LEFT JOIN user_profiles u ON u.user_id = e.user_id
		WHERE 1=1 %s
		GROUP BY e.user_id, CASE WHEN e.user_id IS NULL THEN COALESCE(e.username, '') END
	`, timeConstraint)

	rows, err := getDB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var user UserStats
		var userID sql.NullInt64
		err := rows.Scan(&userID, &user.Username, &user.FirstName, &user.LastName,
			&user.VideoRequests, &user.AudioRequests, &user.DownloadErrors, &user.UnrecognizedCommands)
		if err != nil {
			return nil, err
		}

		user.UserID = userID.Int64
		user.Legacy = !userID.Valid
		stats.Users = append(stats.Users, user)
	}

	return stats, rows.Err()
}
