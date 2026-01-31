package stats

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/go-telegram/bot"
)

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

// RegisterUser registers or updates a user in the database
func RegisterUser(chatID int64, username, firstName, lastName string) {
	err := registerUser(chatID, username, firstName, lastName)
	if err != nil {
		log.Printf("Error registering user: %v", err)
	}
}

// GetAllUserChatIDs returns all user chat IDs from the database
func GetAllUserChatIDs() ([]int64, error) {
	return getAllUserChatIDs()
}

// GetUserCount returns the total number of users
func GetUserCount() (int, error) {
	return getUserCount()
}

// BroadcastResult contains the results of a broadcast operation
type BroadcastResult struct {
	Sent            int
	Failed          int
	BlockedByUser   int
	Errors          []string
}

// SendMessageFunc is a function type for sending messages
type SendMessageFunc func(ctx context.Context, chatID int64, message string) error

// BroadcastMessage sends a message to all users
func BroadcastMessage(ctx context.Context, message string, sendFunc SendMessageFunc) *BroadcastResult {
	result := &BroadcastResult{
		Errors: make([]string, 0),
	}

	chatIDs, err := getAllUserChatIDs()
	if err != nil {
		log.Printf("Error getting user chat IDs: %v", err)
		result.Errors = append(result.Errors, err.Error())
		return result
	}

	for _, chatID := range chatIDs {
		if err := sendFunc(ctx, chatID, message); err != nil {
			result.Failed++

			// Check if user blocked the bot or deleted their account
			if isUserBlockedError(err) {
				result.BlockedByUser++
				if markErr := markUserInactive(chatID); markErr != nil {
					log.Printf("Error marking user %d as inactive: %v", chatID, markErr)
				} else {
					log.Printf("User %d marked as inactive (blocked bot or deleted account)", chatID)
				}
			} else {
				result.Errors = append(result.Errors, err.Error())
			}

			log.Printf("Error sending message to chat %d: %v", chatID, err)
		} else {
			result.Sent++
		}
	}

	return result
}

// isUserBlockedError checks if the error indicates the user blocked the bot
// Uses proper error type checking instead of just string parsing
func isUserBlockedError(err error) bool {
	// First check if it's a Forbidden error (403)
	if !errors.Is(err, bot.ErrorForbidden) {
		return false
	}

	// Then check the error description for specific blocked/deactivated messages
	errMsg := err.Error()
	blockIndicators := []string{
		"bot was blocked by the user",
		"user is deactivated",
		"bot can't initiate conversation with a user",
	}

	for _, indicator := range blockIndicators {
		if strings.Contains(errMsg, indicator) {
			return true
		}
	}
	return false
}
