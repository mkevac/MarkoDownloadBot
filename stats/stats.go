package stats

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/go-telegram/bot"
)

type Stats struct {
	Users []UserStats `json:"users"`
}

type Counts struct {
	VideoRequests        int `json:"video_requests"`
	AudioRequests        int `json:"audio_requests"`
	DownloadErrors       int `json:"download_errors"`
	UnrecognizedCommands int `json:"unrecognized_commands"`
}

func (c Counts) Total() int {
	return c.VideoRequests + c.AudioRequests + c.DownloadErrors + c.UnrecognizedCommands
}

type UserStats struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Legacy    bool   `json:"legacy"`
	Counts
}

// DisplayName is a label only; aggregation always uses UserID for new events.
func (u UserStats) DisplayName() string {
	if u.Legacy {
		if u.Username == "" {
			return "Legacy users without username"
		}
		return "Legacy @" + u.Username
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name != "" {
		return fmt.Sprintf("%s (ID %d)", name, u.UserID)
	}
	return fmt.Sprintf("User ID %d", u.UserID)
}

func (s *Stats) Totals() Counts {
	var totals Counts
	for _, u := range s.Users {
		totals.VideoRequests += u.VideoRequests
		totals.AudioRequests += u.AudioRequests
		totals.DownloadErrors += u.DownloadErrors
		totals.UnrecognizedCommands += u.UnrecognizedCommands
	}
	return totals
}

func AddVideoRequest(userID int64, username string) {
	err := addEvent(userID, username, "video_request")
	if err != nil {
		log.Printf("Error adding video request event to database: %v", err)
	}
}

func AddAudioRequest(userID int64, username string) {
	err := addEvent(userID, username, "audio_request")
	if err != nil {
		log.Printf("Error adding audio request event to database: %v", err)
	}
}

func AddDownloadError(userID int64, username string) {
	err := addEvent(userID, username, "download_error")
	if err != nil {
		log.Printf("Error adding download error event to database: %v", err)
	}
}

func AddUnrecognizedCommand(userID int64, username string) {
	err := addEvent(userID, username, "unrecognized_command")
	if err != nil {
		log.Printf("Error adding unrecognized command event to database: %v", err)
	}
}

func GetStats(period string) *Stats {
	stats, err := getStats(period)
	if err != nil {
		log.Printf("Error getting stats from database: %v", err)
		return &Stats{}
	}
	return stats
}

// RegisterUser updates the sender's profile separately from the broadcast chat.
// In a group, chatID is the group's ID and userID is the sender's personal ID.
func RegisterUser(chatID, userID int64, username, firstName, lastName string) {
	err := registerUser(chatID, userID, username, firstName, lastName)
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
	Sent          int
	Failed        int
	BlockedByUser int
	Errors        []string
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
