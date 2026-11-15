package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/mkevac/markodownloadbot/stats"
)

func TestMain(m *testing.M) {
	// The stats singleton must outlive individual handler tests.
	dir, err := os.MkdirTemp("", "markodownloadbot-test-stats-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stats.Init(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestDownloadStatsTrackSenderAcrossGroupAndGuestRequests(t *testing.T) {
	withGuestTestEnv(t)
	ctx := context.Background()
	for _, id := range []int64{9001, 9002} {
		update := &models.Update{Message: &models.Message{
			Chat: models.Chat{ID: -100123},
			From: &models.User{ID: id, FirstName: "Alex"},
			Text: "https://example.com/video",
		}}
		handleDownload(ctx, nil, update, update.Message.Text, false)
	}
	handleGuestMessage(ctx, &guestMessage{
		MessageID:    1,
		GuestQueryID: "stats-guest",
		Text:         "/audio https://example.com/audio",
		From:         &guestUser{ID: 9001, Username: "new_name", FirstName: "Alex"},
	})

	entries := downloadQueue.entries
	if len(entries) != 3 {
		t.Fatalf("want three queued requests, got %d", len(entries))
	}
	for i, wantID := range []int64{9001, 9002, 9001} {
		if entries[i].UserID != wantID {
			t.Errorf("queue entry %d lost sender ID: %+v", i, entries[i])
		}
	}
	if entries[0].ChatID != -100123 || entries[1].ChatID != -100123 || entries[2].ChatID != 9001 {
		t.Fatalf("queue destinations changed: %+v", entries)
	}

	users := make(map[int64]stats.UserStats)
	for _, u := range stats.GetStats("day").Users {
		users[u.UserID] = u
	}
	if u := users[9001]; u.VideoRequests != 1 || u.AudioRequests != 1 || u.DisplayName() != "@new_name" {
		t.Errorf("same sender split across modes or rename: %+v", u)
	}
	if u := users[9002]; u.VideoRequests != 1 || u.AudioRequests != 0 || u.DisplayName() != "Alex (ID 9002)" {
		t.Errorf("other group sender's stats merged or mislabeled: %+v", u)
	}
}
