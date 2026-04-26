package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/mkevac/markodownloadbot/stats"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var (
	adminChatID   int64
	tmpDir        string
	isLocal       bool
	downloadQueue *DownloadQueue
)

func updatePackages(ctx context.Context, b *bot.Bot) {
	if isLocal {
		log.Println("Skipping package update in local mode")
		return
	}

	log.Println("Checking for package updates...")

	cmd := exec.CommandContext(ctx, "apk", "update")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error updating package index: %v", err)
		sendMessageToAdmin(ctx, b, fmt.Sprintf("❌ Package index update failed: %v", err))
		return
	}
	log.Printf("Package index updated: %s", strings.TrimSpace(string(output)))

	cmd = exec.CommandContext(ctx, "apk", "upgrade", "yt-dlp")
	output, err = cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	if err != nil {
		log.Printf("Error upgrading yt-dlp: %v", err)
		sendMessageToAdmin(ctx, b, fmt.Sprintf("❌ yt-dlp upgrade failed: %v", err))
		return
	}

	if strings.Contains(outputStr, "Upgrading") || strings.Contains(outputStr, "Installing") {
		log.Printf("yt-dlp updated: %s", outputStr)
		sendMessageToAdmin(ctx, b, fmt.Sprintf("✅ yt-dlp updated successfully:\n%s", outputStr))
	} else {
		log.Printf("yt-dlp already up to date: %s", outputStr)
	}
}

func startUpdateScheduler(ctx context.Context, b *bot.Bot) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	updatePackages(ctx, b)

	for {
		select {
		case <-ctx.Done():
			log.Println("Update scheduler stopped")
			return
		case <-ticker.C:
			updatePackages(ctx, b)
		}
	}
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Error loading .env file: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if raw := strings.TrimSpace(os.Getenv("ADMIN_CHAT_ID")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			log.Printf("invalid ADMIN_CHAT_ID=%q: %v (admin features disabled)", raw, err)
		} else {
			adminChatID = id
			log.Printf("Admin chat ID: %d", adminChatID)
		}
	} else {
		log.Println("ADMIN_CHAT_ID not set — admin features disabled")
	}

	isLocal = os.Getenv("IS_LOCAL") == "true"

	dirBase := "/app/data"
	if isLocal {
		dirBase = "./data"
	}

	// Initialize the stats package with the calculated dirBase
	stats.Init(dirBase)

	var err error
	tmpDir, err = os.MkdirTemp(dirBase, "telegram-bot-api-*")
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
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatalf("File server failed: %v", err)
		}
	}()

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
			select {
			case <-ctx.Done():
				log.Println("Shutdown signal received during bot initialization")
				return
			case <-time.After(time.Second * 5):
				// Retry after 5 seconds
			}
		} else {
			break
		}
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/stats", bot.MatchTypeExact, statsHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/audio", bot.MatchTypePrefix, audioHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypeExact, helpHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, helpHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/broadcast", bot.MatchTypePrefix, broadcastHandler)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/users", bot.MatchTypeExact, usersHandler)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "cancel:", bot.MatchTypePrefix, cancelCallbackHandler)

	messenger := &botMessenger{b: b}
	processor := &downloadProcessor{bot: b, messenger: messenger, botCtx: ctx}
	downloadQueue = NewDownloadQueue(ctx, messenger, processor.process)
	go downloadQueue.Run()

	publicCommands := []models.BotCommand{
		{Command: "start", Description: "Start the bot"},
		{Command: "help", Description: "Show help information"},
		{Command: "audio", Description: "Download audio"},
	}
	adminCommands := append([]models.BotCommand{}, publicCommands...)
	adminCommands = append(adminCommands,
		models.BotCommand{Command: "stats", Description: "Show stats"},
		models.BotCommand{Command: "users", Description: "Show user count"},
		models.BotCommand{Command: "broadcast", Description: "Broadcast message"},
	)

	if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: publicCommands,
	}); err != nil {
		log.Printf("Error setting default bot commands: %v", err)
	} else {
		log.Println("Default bot commands set")
	}

	if adminChatID != 0 {
		if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
			Commands: adminCommands,
			Scope:    &models.BotCommandScopeChat{ChatID: adminChatID},
		}); err != nil {
			log.Printf("Error setting admin bot commands: %v", err)
		} else {
			log.Println("Admin-scoped bot commands set")
		}
	}

	go b.Start(ctx)

	go startUpdateScheduler(ctx, b)

	<-ctx.Done()
	log.Println("Received interrupt signal")
}

const (
	progressBarLength      = 10
	progressUpdateInterval = time.Second
)

func progressBar(percent int) string {
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	filled := percent * progressBarLength / 100
	return strings.Repeat("▰", filled) + strings.Repeat("▱", progressBarLength-filled) + fmt.Sprintf(" %d%%", percent)
}

type botMessenger struct {
	b *bot.Bot
}

func cancelKeyboard(id string) *models.InlineKeyboardMarkup {
	if id == "" {
		return nil
	}
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "❌ Cancel", CallbackData: "cancel:" + id},
		}},
	}
}

func (m *botMessenger) Send(ctx context.Context, chatID int64, text, withCancelID string) (int, error) {
	params := &bot.SendMessageParams{ChatID: chatID, Text: text}
	if kb := cancelKeyboard(withCancelID); kb != nil {
		params.ReplyMarkup = kb
	}
	msg, err := m.b.SendMessage(ctx, params)
	if err != nil {
		return 0, err
	}
	return msg.ID, nil
}

func (m *botMessenger) Edit(ctx context.Context, chatID int64, messageID int, text, withCancelID string) error {
	params := &bot.EditMessageTextParams{ChatID: chatID, MessageID: messageID, Text: text}
	if kb := cancelKeyboard(withCancelID); kb != nil {
		params.ReplyMarkup = kb
	}
	_, err := m.b.EditMessageText(ctx, params)
	return err
}

func (m *botMessenger) Delete(ctx context.Context, chatID int64, messageID int) error {
	_, err := m.b.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: messageID})
	return err
}

func cancelCallbackHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.CallbackQuery == nil || downloadQueue == nil {
		return
	}
	id := strings.TrimPrefix(update.CallbackQuery.Data, "cancel:")
	if id == "" {
		return
	}
	downloadQueue.Cancel(id)
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	}); err != nil {
		log.Printf("error answering cancel callback: %v", err)
	}
}

func sendMessageToAdmin(ctx context.Context, b *bot.Bot, text string) {
	if adminChatID == 0 {
		return
	}

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: adminChatID,
		Text:   text,
	}); err != nil {
		log.Printf("Error sending message to admin: %v", err)
	}
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
	log.Printf("[%s]: received stats command", update.Message.From.Username)

	stats.RegisterUser(update.Message.Chat.ID, update.Message.From.Username, update.Message.From.FirstName, update.Message.From.LastName)

	if update.Message.Chat.ID != adminChatID {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "You are not authorized to use this command",
		}); err != nil {
			log.Printf("Error sending unauthorized message: %v", err)
		}
		sendMessageToAdmin(ctx, b, fmt.Sprintf("Unauthorized access to /stats command from @%s", update.Message.From.Username))
		return
	}

	periods := []string{"day", "week", "month", "overall"}

	// Send summary stats first
	var summaryMsg strings.Builder
	summaryMsg.WriteString("*Summary Stats*\n\n")

	for _, period := range periods {
		stats := stats.GetStats(period)
		totalVideoRequests := sum(stats.VideoRequests)
		totalAudioRequests := sum(stats.AudioRequests)

		caser := cases.Title(language.English)
		summaryMsg.WriteString(fmt.Sprintf("*%s:* V:`%d` A:`%d` E:`%d`\n",
			caser.String(period),
			totalVideoRequests,
			totalAudioRequests,
			sum(stats.DownloadErrors)))
	}

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      summaryMsg.String(),
		ParseMode: models.ParseModeMarkdown,
	}); err != nil {
		log.Printf("Error sending stats summary: %v", err)
	}

	// Send detailed per-period stats
	for _, period := range periods {
		stats := stats.GetStats(period)

		var detailMsg strings.Builder
		detailMsg.WriteString(fmt.Sprintf("*Detailed Stats \\- %s*\n\n", cases.Title(language.English).String(period)))

		// Get top 10 users by total activity
		type userStats struct {
			username string
			total    int
		}

		users := make([]userStats, 0)
		for username, videoCount := range stats.VideoRequests {
			total := videoCount +
				stats.AudioRequests[username] +
				stats.DownloadErrors[username] +
				stats.UnrecognizedCommands[username]
			users = append(users, userStats{username, total})
		}

		// Sort users by total activity
		sort.Slice(users, func(i, j int) bool {
			return users[i].total > users[j].total
		})

		// Show top 10 users
		maxUsers := 10
		if len(users) < maxUsers {
			maxUsers = len(users)
		}

		detailMsg.WriteString("Top Users:\n")
		for i := 0; i < maxUsers; i++ {
			username := users[i].username
			escapedUsername := bot.EscapeMarkdown(username)
			detailMsg.WriteString(fmt.Sprintf("@%s: V:`%d` A:`%d` E:`%d`\n",
				escapedUsername,
				stats.VideoRequests[username],
				stats.AudioRequests[username],
				stats.DownloadErrors[username]))
		}

		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    update.Message.Chat.ID,
			Text:      detailMsg.String(),
			ParseMode: models.ParseModeMarkdown,
		}); err != nil {
			log.Printf("Error sending detailed stats: %v", err)
		}
	}
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

	stats.RegisterUser(update.Message.Chat.ID, update.Message.From.Username, update.Message.From.FirstName, update.Message.From.LastName)

	input, err := cleanupAndVerifyInput(input)
	if err != nil {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Please send me a valid video or audio link",
		}); err != nil {
			log.Printf("[%s]: error sending invalid URL message: %v", update.Message.From.Username, err)
		}
		sendMessageToAdmin(ctx, b, fmt.Sprintf("Unrecognized command from @%s: %s", update.Message.From.Username, update.Message.Text))
		stats.AddUnrecognizedCommand(update.Message.From.Username)
		return
	}

	if audioOnly {
		stats.AddAudioRequest(update.Message.From.Username)
	} else {
		stats.AddVideoRequest(update.Message.From.Username)
	}

	mediaType := "video"
	if audioOnly {
		mediaType = "audio"
	}
	log.Printf("[%s]: %s url: '%s'", update.Message.From.Username, mediaType, input)

	entry := &DownloadEntry{
		ID:        uuid.New().String(),
		ChatID:    update.Message.Chat.ID,
		URL:       input,
		Username:  update.Message.From.Username,
		AudioOnly: audioOnly,
	}
	if err := downloadQueue.Add(entry); err != nil {
		log.Printf("[%s]: failed to enqueue: %v", entry.Username, err)
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: entry.ChatID,
			Text:   "Sorry, couldn't queue your request. Please try again.",
		}); err != nil {
			log.Printf("error sending enqueue failure: %v", err)
		}
	}
}

type downloadProcessor struct {
	bot       *bot.Bot
	messenger Messenger
	botCtx    context.Context
}

func (p *downloadProcessor) process(entryCtx context.Context, e *DownloadEntry) {
	mediaType := "video"
	if e.AudioOnly {
		mediaType = "audio"
	}

	if err := p.messenger.Edit(p.botCtx, e.ChatID, e.StatusMessageID(), fmt.Sprintf("⬇️ Downloading %s...", mediaType), e.ID); err != nil {
		log.Printf("[%s]: error setting downloading status: %v", e.LogTag(), err)
	}

	cookiesFile := os.Getenv("COOKIES_FILE")
	if cookiesFile == "" {
		cookiesFile = "/app/cookies.txt"
	}

	var (
		lastUpdateAt    time.Time
		lastPercent     = -1
		lastStream      = 0
		lastLoggedTenth = -1
	)
	onProgress := func(upd progressUpdate) {
		streamChanged := upd.streamIndex != lastStream
		if !streamChanged && upd.percent == lastPercent {
			return
		}
		if !streamChanged && upd.percent < 100 && time.Since(lastUpdateAt) < progressUpdateInterval {
			return
		}
		lastPercent = upd.percent
		lastStream = upd.streamIndex
		lastUpdateAt = time.Now()

		tenth := upd.percent / 10
		if tenth != lastLoggedTenth {
			lastLoggedTenth = tenth
			log.Printf("[%s]: download progress %d%% (stream %d)", e.LogTag(), upd.percent, upd.streamIndex)
		}

		header := fmt.Sprintf("⬇️ Downloading %s", mediaType)
		if upd.streamIndex >= 2 {
			header += fmt.Sprintf(" (stream %d)", upd.streamIndex)
		}
		if err := p.messenger.Edit(p.botCtx, e.ChatID, e.StatusMessageID(), fmt.Sprintf("%s\n%s", header, progressBar(upd.percent)), e.ID); err != nil {
			log.Printf("[%s]: error editing progress: %v", e.LogTag(), err)
		}
	}

	media, err := DownloadMedia(entryCtx, e.URL, e.LogTag(), tmpDir, cookiesFile, e.AudioOnly, onProgress)
	if err != nil {
		if entryCtx.Err() != nil {
			_ = p.messenger.Delete(p.botCtx, e.ChatID, e.StatusMessageID())
			return
		}
		log.Printf("[%s]: error downloading %s: %s", e.LogTag(), mediaType, err)
		stats.AddDownloadError(e.Username)
		errorMsg := fmt.Sprintf("I'm sorry, @%s. I'm afraid I can't do that. Error downloading %s from %s: %s",
			e.Username, mediaType, e.URL, err.Error())
		_ = p.messenger.Edit(p.botCtx, e.ChatID, e.StatusMessageID(), errorMsg, "")
		sendMessageToAdmin(p.botCtx, p.bot, errorMsg)
		return
	}

	if fileSize, err := media.GetFileSize(); err != nil {
		log.Printf("[%s]: error getting file size: %s", e.LogTag(), err)
	} else {
		log.Printf("[%s]: %s downloaded to '%s' (size: %d bytes)", e.LogTag(), mediaType, media.Path, fileSize)
	}

	pathToSend := media.Path
	if isLocal {
		pathToSend = filepath.Join("/app", media.Path)
	}
	log.Printf("[%s]: media path to send: %s", e.LogTag(), pathToSend)

	if err := p.messenger.Edit(p.botCtx, e.ChatID, e.StatusMessageID(), fmt.Sprintf("☁️ Sending %s to Telegram...", mediaType), e.ID); err != nil {
		log.Printf("[%s]: error setting sending status: %v", e.LogTag(), err)
	}

	var sendErr error
	if e.AudioOnly {
		_, sendErr = p.bot.SendAudio(entryCtx, &bot.SendAudioParams{
			ChatID: e.ChatID,
			Audio:  &models.InputFileString{Data: "file://" + pathToSend},
		})
	} else {
		_, sendErr = p.bot.SendVideo(entryCtx, &bot.SendVideoParams{
			ChatID:   e.ChatID,
			Video:    &models.InputFileString{Data: "file://" + pathToSend},
			Width:    media.Width,
			Height:   media.Height,
			Duration: (int)(media.Duration),
		})
	}

	if sendErr != nil {
		if entryCtx.Err() != nil {
			_ = p.messenger.Delete(p.botCtx, e.ChatID, e.StatusMessageID())
		} else {
			log.Printf("[%s]: error sending %s: %v", e.LogTag(), mediaType, sendErr)
			_ = p.messenger.Edit(p.botCtx, e.ChatID, e.StatusMessageID(), fmt.Sprintf("❌ Failed to send %s: %v", mediaType, sendErr), "")
		}
	} else {
		_ = p.messenger.Delete(p.botCtx, e.ChatID, e.StatusMessageID())
		log.Printf("[%s]: %s sent", e.LogTag(), mediaType)
	}

	if err := media.Delete(); err != nil {
		log.Printf("[%s]: error removing %s file: %s", e.LogTag(), mediaType, err)
	}
	log.Printf("[%s]: %s removed", e.LogTag(), mediaType)
}

func broadcastHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	log.Printf("[%s]: received broadcast command", update.Message.From.Username)

	stats.RegisterUser(update.Message.Chat.ID, update.Message.From.Username, update.Message.From.FirstName, update.Message.From.LastName)

	if update.Message.Chat.ID != adminChatID {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "You are not authorized to use this command",
		}); err != nil {
			log.Printf("Error sending unauthorized message: %v", err)
		}
		sendMessageToAdmin(ctx, b, fmt.Sprintf("Unauthorized access to /broadcast command from @%s", update.Message.From.Username))
		return
	}

	// Extract the message to broadcast
	message := strings.TrimSpace(strings.TrimPrefix(update.Message.Text, "/broadcast"))
	if message == "" {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Usage: /broadcast <message>\nExample: /broadcast Hello everyone!",
		}); err != nil {
			log.Printf("Error sending broadcast usage: %v", err)
		}
		return
	}

	// Send confirmation
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Broadcasting message to all users...",
	}); err != nil {
		log.Printf("Error sending broadcast confirmation: %v", err)
	}

	// Create send function
	sendFunc := func(ctx context.Context, chatID int64, msg string) error {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   msg,
		})
		return err
	}

	// Broadcast the message
	result := stats.BroadcastMessage(ctx, message, sendFunc)

	// Send result to admin
	resultMsg := fmt.Sprintf("Broadcast complete!\n\nSent: %d\nFailed: %d", result.Sent, result.Failed)

	if result.BlockedByUser > 0 {
		resultMsg += fmt.Sprintf("\nBlocked/Inactive: %d (marked as inactive)", result.BlockedByUser)
	}

	if len(result.Errors) > 0 && len(result.Errors) <= 5 {
		resultMsg += fmt.Sprintf("\n\nOther Errors:\n%s", strings.Join(result.Errors, "\n"))
	} else if len(result.Errors) > 5 {
		resultMsg += fmt.Sprintf("\n\nOther Errors: %d (showing first 5):\n%s", len(result.Errors), strings.Join(result.Errors[:5], "\n"))
	}

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   resultMsg,
	}); err != nil {
		log.Printf("Error sending broadcast result: %v", err)
	}
}

func usersHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	log.Printf("[%s]: received users command", update.Message.From.Username)

	stats.RegisterUser(update.Message.Chat.ID, update.Message.From.Username, update.Message.From.FirstName, update.Message.From.LastName)

	if update.Message.Chat.ID != adminChatID {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "You are not authorized to use this command",
		}); err != nil {
			log.Printf("Error sending unauthorized message: %v", err)
		}
		sendMessageToAdmin(ctx, b, fmt.Sprintf("Unauthorized access to /users command from @%s", update.Message.From.Username))
		return
	}

	count, err := stats.GetUserCount()
	if err != nil {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Error getting user count: %v", err),
		}); err != nil {
			log.Printf("Error sending user count error: %v", err)
		}
		return
	}

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("Total registered users: %d", count),
	}); err != nil {
		log.Printf("Error sending user count: %v", err)
	}
}

func helpHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	log.Printf("[%s]: received message: '%s'", update.Message.From.Username, update.Message.Text)

	stats.RegisterUser(update.Message.Chat.ID, update.Message.From.Username, update.Message.From.FirstName, update.Message.From.LastName)

	helpMessage := `<b>Welcome to the Marko Download Bot!</b>

Here's how you can use me:

1. <b>Download Video:</b>
   Simply send a video URL, and I'll download and send the video to you.

2. <code>/audio [URL]</code>:
   Use this command followed by an audio URL to download and receive audio files.

3. <code>/stats</code>:
   (Admin only) View usage statistics of the bot.

4. <code>/users</code>:
   (Admin only) View total number of registered users.

5. <code>/broadcast [message]</code>:
   (Admin only) Send a message to all bot users.

6. <code>/help</code> or <code>/start</code>:
   Display this help message.

To download media, just send me a valid video or audio link. I'll take care of the rest!

Note: Please ensure you have the rights to download and use the media you request.`

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      helpMessage,
		ParseMode: models.ParseModeHTML,
	}); err != nil {
		log.Printf("[%s]: error sending help message: %v", update.Message.From.Username, err)
	}
}

// Helper function to sum map values
func sum(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}
