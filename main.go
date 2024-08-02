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
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mkevac/markodownloadbot/stats"
)

const (
	tmpDir = "/var/lib/telegram-bot-api"
)

var (
	adminUsername string
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	adminUsername = os.Getenv("ADMIN_USERNAME")
	log.Printf("Admin username: %s", adminUsername)

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

	var b *bot.Bot
	var err error

	for {
		b, err = bot.New(os.Getenv("TELEGRAM_BOT_API_TOKEN"), opts...)
		if err != nil {
			log.Printf("Error creating bot: %s", err)
			time.Sleep(time.Second * 5)
		} else {
			break
		}
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/stats", bot.MatchTypeExact, statsHandler)

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

func statsHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message.From.Username != adminUsername {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "You are not authorized to use this command",
		})
		return
	}

	stats := stats.GetStats()

	totalRequests := 0
	for _, count := range stats.Requests {
		totalRequests += count
	}

	// prepare stats message in Markdown format
	var statsMessage strings.Builder
	statsMessage.WriteString("*Stats*\n")
	statsMessage.WriteString("```\n")
	statsMessage.WriteString(fmt.Sprintf("Total requests: %d\n", totalRequests))
	for username, count := range stats.Requests {
		statsMessage.WriteString(fmt.Sprintf("%s: %d\n", username, count))
	}
	statsMessage.WriteString(fmt.Sprintf("Download errors: %d\n", stats.DownloadErrors))
	statsMessage.WriteString(fmt.Sprintf("Unrecognized commands: %d\n", stats.UnrecognizedCommands))
	statsMessage.WriteString("```")

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      statsMessage.String(),
		ParseMode: models.ParseModeMarkdown,
	})
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	log.Printf("[%s]: received message: '%s'", update.Message.From.Username, update.Message.Text)

	input, err := cleanupAndVerifyInput(update.Message.Text)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Please send me a video link",
		})
		stats.AddUnrecognizedCommand()
		return
	}

	stats.AddRequest(update.Message.From.Username)
	log.Printf("[%s]: video url: '%s'", update.Message.From.Username, input)

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "I will download the video and send it to you shortly.",
	})

	video, err := DownloadVideo(input, update.Message.From.Username, tmpDir)
	if err != nil {
		log.Printf("Error downloading video: %s", err)
		stats.AddDownloadError()

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
