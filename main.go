package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
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

// download video from url using yt-dlp and save it in a temporary directory
// return the path to the downloaded video
func downloadVideo(url string) (string, error) {
	id := uuid.New()
	randomName := id.String()

	cmd := exec.Command("yt-dlp", "-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/mp4",
		"-o", tmpDir+"/"+randomName+".%(ext)s", "--print-to-file", randomName+".%(ext)s", tmpDir+"/"+randomName+".txt", url)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("command execution failed with %s", err)
	}

	log.Printf("Output: %s\n", out.String())
	log.Printf("Error: %s\n", stderr.String())

	// open text file with video filename and get contents
	textFile := tmpDir + "/" + randomName + ".txt"

	buf, err := os.ReadFile(textFile)
	if err != nil {
		return "", fmt.Errorf("error reading text file '%s': %s", textFile, err)
	}

	filename := strings.TrimSpace(string(buf))

	if err := os.Remove(textFile); err != nil {
		log.Printf("Error removing text file: %s", err)
	}

	return filename, nil
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
	log.Printf("[%s]: received message: \"%s\"", update.Message.From.Username, update.Message.Text)

	input, err := cleanupAndVerifyInput(update.Message.Text)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Please send me a video link",
		})
		return
	}

	log.Printf("[%s]: video url: %s", update.Message.From.Username, input)

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "I will download the video and send it to you shortly.",
	})

	path, err := downloadVideo(input)
	if err != nil {
		log.Printf("Error downloading video: %s", err)

		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   fmt.Sprintf("Error downloading video from %s: %s", input, err.Error()),
		})

		return
	}

	fullUrl := fmt.Sprintf("file://%s/%s", tmpDir, path)

	b.SendVideo(ctx, &bot.SendVideoParams{
		ChatID: update.Message.Chat.ID,
		Video:  &models.InputFileString{Data: fullUrl},
	})

	// remove downloaded video and text file
	err = os.Remove(tmpDir + "/" + path)
	if err != nil {
		log.Printf("Error removing video file: %s", err)
	}

	err = os.Remove(tmpDir + "/" + strings.TrimSuffix(path, ".mp4") + ".txt")
	if err != nil {
		log.Printf("Error removing text file: %s", err)
	}
}
