package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
	"github.com/mkevac/markodownloadbot/stats"
)

var (
	adminUsername string
	adminChatID   int64
	tmpDir        string
	isLocal       bool
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Error loading .env file: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	adminUsername = os.Getenv("ADMIN_USERNAME")
	log.Printf("Admin username: %s", adminUsername)

	isLocal = os.Getenv("IS_LOCAL") == "true"
	tmpDirBase := "/app/data"
	if isLocal {
		tmpDirBase = "./data"
	}

	var err error
	tmpDir, err = os.MkdirTemp(tmpDirBase, "telegram-bot-api-*")
	if err != nil {
		log.Fatalf("Failed to create temporary directory: %v", err)
	}
	if err := os.Chmod(tmpDir, 0755); err != nil {
		log.Fatalf("Failed to set permissions on temporary directory: %v", err)
	}
	defer func() {
		log.Printf("Removing temporary directory: %s", tmpDir)
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Printf("Failed to remove temporary directory: %v", err)
		}
	}()

	log.Printf("Using temporary directory: %s", tmpDir)

	// Use http.FileServer to serve files from the specified directory
	fileServer := http.FileServer(http.Dir(tmpDir))

	// Handle all requests by serving the file from the directory
	http.Handle("/", fileServer)

	log.Println("Serving files on :8080")
	go http.ListenAndServe(":8080", nil)

	serverURL := "http://telegram-bot-api:8081"
	if isLocal {
		serverURL = "http://localhost:8081"
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
		bot.WithServerURL(serverURL),
	}

	var b *bot.Bot

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
	b.RegisterHandler(bot.HandlerTypeMessageText, "/audio", bot.MatchTypePrefix, audioHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, helpHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, helpHandler)

	success, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: []models.BotCommand{
			{Command: "start", Description: "Start the bot"},
			{Command: "help", Description: "Show help information"},
			{Command: "audio", Description: "Download audio"},
			{Command: "stats", Description: "Show stats (admin only)"},
		},
	})
	if err != nil {
		log.Printf("Error setting bot commands: %v", err)
	} else if !success {
		log.Println("SetMyCommands did not return true")
	} else {
		log.Println("Bot commands set successfully")
	}

	go b.Start(ctx)

	<-ctx.Done()
	log.Println("Received interrupt signal")
}

func saveAdminChatID(username string, chatID int64) {
	if adminUsername != "" && adminUsername == username {
		adminChatID = chatID
	}
}

func sendMessageToAdmin(ctx context.Context, b *bot.Bot, text string) {
	if adminChatID == 0 {
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: adminChatID,
		Text:   text,
	})
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
	saveAdminChatID(update.Message.From.Username, update.Message.Chat.ID)

	if update.Message.From.Username != adminUsername {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "You are not authorized to use this command",
		})
		sendMessageToAdmin(ctx, b, fmt.Sprintf("Unauthorized access to /stats command from @%s", update.Message.From.Username))
		return
	}

	stats := stats.GetStats()

	totalRequests := 0
	for _, count := range stats.Requests {
		totalRequests += count
	}

	// prepare stats message in Markdown format
	var statsMessage strings.Builder
	statsMessage.WriteString("*Stats*\n\n")
	statsMessage.WriteString(fmt.Sprintf("Total requests: `%d`\n", totalRequests))
	for username, count := range stats.Requests {
		statsMessage.WriteString(fmt.Sprintf("@%s: `%d`\n", username, count))
	}
	statsMessage.WriteString(fmt.Sprintf("Download errors: `%d`\n", stats.DownloadErrors))
	statsMessage.WriteString(fmt.Sprintf("Unrecognized commands: `%d`\n", stats.UnrecognizedCommands))

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      statsMessage.String(),
		ParseMode: models.ParseModeMarkdown,
	})
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		log.Println("Received update with nil Message")
		return
	}
	handleDownload(ctx, b, update, update.Message.Text, false)
}

func audioHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		log.Println("Received audio command with nil Message")
		return
	}
	input := strings.TrimSpace(strings.TrimPrefix(update.Message.Text, "/audio"))
	handleDownload(ctx, b, update, input, true)
}

func handleDownload(ctx context.Context, b *bot.Bot, update *models.Update, input string, audioOnly bool) {
	log.Printf("[%s]: received message: '%s'", update.Message.From.Username, update.Message.Text)

	saveAdminChatID(update.Message.From.Username, update.Message.Chat.ID)

	input, err := cleanupAndVerifyInput(input)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Please send me a valid video or audio link",
		})
		sendMessageToAdmin(ctx, b, fmt.Sprintf("Unrecognized command from @%s: %s", update.Message.From.Username, update.Message.Text))
		stats.AddUnrecognizedCommand()
		return
	}

	stats.AddRequest(update.Message.From.Username)

	var mediaType string
	if audioOnly {
		mediaType = "audio"
	} else {
		mediaType = "video"
	}
	log.Printf("[%s]: %s url: '%s'", update.Message.From.Username, mediaType, input)

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("I will download the %s and send it to you shortly.", mediaType),
	})

	cookiesFile := os.Getenv("COOKIES_FILE")
	if cookiesFile == "" {
		cookiesFile = "/app/cookies.txt"
	}
	log.Printf("Using cookies file: %s", cookiesFile)

	media, err := DownloadMedia(input, update.Message.From.Username, tmpDir, cookiesFile, audioOnly)
	if err != nil {
		log.Printf("Error downloading %s: %s", mediaType, err)
		stats.AddDownloadError()

		errorMsg := fmt.Sprintf("I'm sorry, @%s. I'm afraid I can't do that. Error downloading %s from %s: %s",
			update.Message.From.Username, mediaType, input, err.Error())

		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   errorMsg,
		})

		sendMessageToAdmin(ctx, b, errorMsg)

		return
	}

	fileSize, err := media.GetFileSize()
	if err != nil {
		log.Printf("Error getting file size: %s", err)
	} else {
		log.Printf("[%s]: %s downloaded to '%s' (size: %d bytes)", update.Message.From.Username, mediaType, media.Path, fileSize)
	}

	// fix media path if local
	var pathToSend string
	if isLocal {
		pathToSend = filepath.Join("/app", media.Path)
	} else {
		pathToSend = media.Path
	}

	log.Printf("[%s]: media path to send: %s", update.Message.From.Username, pathToSend)

	if audioOnly {
		b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID: update.Message.Chat.ID,
			Audio:  &models.InputFileString{Data: "file://" + pathToSend},
		})
	} else {
		b.SendVideo(ctx, &bot.SendVideoParams{
			ChatID:   update.Message.Chat.ID,
			Video:    &models.InputFileString{Data: "file://" + pathToSend},
			Width:    media.Width,
			Height:   media.Height,
			Duration: (int)(media.Duration),
		})
	}

	log.Printf("[%s]: %s sent", update.Message.From.Username, mediaType)

	/*
		if err := media.Delete(); err != nil {
			log.Printf("Error removing %s file: %s", mediaType, err)
		}

		log.Printf("[%s]: %s removed", update.Message.From.Username, mediaType)
	*/
}

func helpHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	log.Printf("[%s]: received message: '%s'", update.Message.From.Username, update.Message.Text)

	helpMessage := `<b>Welcome to the Marko Download Bot!</b>

Here's how you can use me:

1. <b>Download Video:</b> 
   Simply send a video URL, and I'll download and send the video to you.

2. <code>/audio [URL]</code>: 
   Use this command followed by an audio URL to download and receive audio files.

3. <code>/stats</code>: 
   (Admin only) View usage statistics of the bot.

4. <code>/help</code> or <code>/start</code>: 
   Display this help message.

To download media, just send me a valid video or audio link. I'll take care of the rest!

Note: Please ensure you have the rights to download and use the media you request.`

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      helpMessage,
		ParseMode: models.ParseModeHTML,
	})
}
