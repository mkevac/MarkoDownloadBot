package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	tmpDir = "/var/lib/telegram-bot-api"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Use http.FileServer to serve files from the specified directory
	fileServer := http.FileServer(http.Dir(tmpDir))

	// Handle all requests by serving the file from the directory
	http.Handle("/", fileServer)

	log.Println("Serving files on :8080")
	go http.ListenAndServe(":8080", nil)

	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
		bot.WithServerURL("http://telegram-bot-api:8081"),
	}

	b, err := bot.New(os.Getenv("TELEGRAM_BOT_API_TOKEN"), opts...)
	if err != nil {
		panic(err)
	}

	b.Start(ctx)
}

func cleanupAndVerifyInput(input string) (string, error) {
	byLines := strings.Split(input, "\n")
	if len(byLines) > 1 {
		return "", fmt.Errorf("input should be a single line")
	}

	// remove leading and trailing whitespaces
	input = strings.TrimSpace(input)

	// remove leading and trailing quotes
	input = strings.Trim(input, "\"")

	// check if input is a valid URL
	u, err := url.Parse(input)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid URL")
	}

	return input, nil
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	log.Printf("[%s]: received message: '%s'", update.Message.From.Username, update.Message.Text)

	input, err := cleanupAndVerifyInput(update.Message.Text)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Please send me a video link",
		})
		return
	}

	log.Printf("[%s]: video url: '%s'", update.Message.From.Username, input)

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "I will download the video and send it to you shortly.",
	})

	video, err := DownloadVideo(input, update.Message.From.Username, tmpDir)
	if err != nil {
		log.Printf("Error downloading video: %s", err)

		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Error downloading video from %s: %s", input, err.Error()),
		})

		return
	}

	b.SendVideo(ctx, &bot.SendVideoParams{
		ChatID:   update.Message.Chat.ID,
		Video:    &models.InputFileString{Data: "file://" + video.Path},
		Width:    video.Width,
		Height:   video.Height,
		Duration: (int)(video.Duration),
	})

	log.Printf("[%s]: video sent", update.Message.From.Username)

	if err := video.Delete(); err != nil {
		log.Printf("Error removing video file: %s", err)
	}

	log.Printf("[%s]: video removed", update.Message.From.Username)
}
