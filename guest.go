package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// guestUpdate is the subset of Telegram's Update needed to recognize guest
// messages. We decode the rest with the library's models.Update.
type guestUpdate struct {
	ID           int64         `json:"update_id"`
	GuestMessage *guestMessage `json:"guest_message,omitempty"`
}

// guestMessage mirrors the fields of Message that matter for guest replies.
// Bot API 10.0 added guest_query_id, guest_bot_caller_user, guest_bot_caller_chat.
type guestMessage struct {
	MessageID          int64      `json:"message_id"`
	Date               int64      `json:"date"`
	Text               string     `json:"text"`
	GuestQueryID       string     `json:"guest_query_id"`
	From               *guestUser `json:"from"`
	Chat               *guestChat `json:"chat"`
	GuestBotCallerUser *guestUser `json:"guest_bot_caller_user,omitempty"`
	GuestBotCallerChat *guestChat `json:"guest_bot_caller_chat,omitempty"`
}

type guestUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	IsBot     bool   `json:"is_bot"`
}

type guestChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// extractGuestURL pulls the first URL-like token out of the message text after
// stripping a leading bot mention. Returns "" if no plausible URL is found.
func extractGuestURL(text string) string {
	for _, tok := range strings.Fields(text) {
		if strings.HasPrefix(tok, "@") {
			continue
		}
		tok = strings.Trim(tok, "\"'")
		if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
			return tok
		}
	}
	return ""
}

// GuestMedia is the payload uploaded to the staging chat to mint a cached
// file_id. Width/Height/Duration must be passed on the staging send so
// Telegram associates correct aspect ratio and duration with the file_id —
// otherwise InlineQueryResultCachedVideo renders as a black square with
// 00:00 duration.
type GuestMedia struct {
	Path     string
	Title    string
	Width    int
	Height   int
	Duration int
}

// GuestResponder sends a reply to a guest_message. Implementations upload the
// downloaded media to obtain a Telegram file_id and then call answerGuestQuery
// with an InlineQueryResultCached* referencing that file_id.
type GuestResponder interface {
	RespondMedia(ctx context.Context, queryID, resultID string, audioOnly bool, media GuestMedia) error
	RespondError(ctx context.Context, queryID, resultID, message string) error
}

// httpGuestResponder is the production GuestResponder. It uses the bot SDK for
// staging uploads/deletes (because those need multipart) and raw JSON over HTTP
// for answerGuestQuery (the SDK has no helper yet).
type httpGuestResponder struct {
	bot         *bot.Bot
	stagingChat int64
	apiURL      string
	botToken    string
	httpClient  *http.Client
	makeFileRef func(path string) string
}

func newHTTPGuestResponder(b *bot.Bot, stagingChat int64, apiURL, botToken string, makeFileRef func(string) string) *httpGuestResponder {
	return &httpGuestResponder{
		bot:         b,
		stagingChat: stagingChat,
		apiURL:      strings.TrimRight(apiURL, "/"),
		botToken:    botToken,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		makeFileRef: makeFileRef,
	}
}

func (r *httpGuestResponder) RespondMedia(ctx context.Context, queryID, resultID string, audioOnly bool, media GuestMedia) error {
	fileID, stagingMsgID, err := r.uploadStaging(ctx, media, audioOnly)
	if err != nil {
		return fmt.Errorf("staging upload: %w", err)
	}

	defer func() {
		if _, err := r.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    r.stagingChat,
			MessageID: stagingMsgID,
		}); err != nil {
			log.Printf("guest: error deleting staging message %d: %v", stagingMsgID, err)
		}
	}()

	result, err := buildCachedResult(resultID, fileID, audioOnly, media.Title)
	if err != nil {
		return fmt.Errorf("build cached result: %w", err)
	}

	return r.answerGuestQuery(ctx, queryID, result)
}

func (r *httpGuestResponder) RespondError(ctx context.Context, queryID, resultID, message string) error {
	article := &models.InlineQueryResultArticle{
		ID:    resultID,
		Title: "Download failed",
		InputMessageContent: &models.InputTextMessageContent{
			MessageText: message,
		},
	}
	payload, err := article.MarshalCustom()
	if err != nil {
		return fmt.Errorf("marshal error article: %w", err)
	}
	return r.answerGuestQuery(ctx, queryID, payload)
}

func (r *httpGuestResponder) uploadStaging(ctx context.Context, media GuestMedia, audioOnly bool) (fileID string, messageID int, err error) {
	ref := media.Path
	if r.makeFileRef != nil {
		ref = r.makeFileRef(media.Path)
	}

	if audioOnly {
		msg, err := r.bot.SendAudio(ctx, stagingAudioParams(r.stagingChat, ref, media))
		if err != nil {
			return "", 0, err
		}
		if msg.Audio == nil {
			return "", msg.ID, errors.New("staging upload returned no audio metadata")
		}
		return msg.Audio.FileID, msg.ID, nil
	}

	msg, err := r.bot.SendVideo(ctx, stagingVideoParams(r.stagingChat, ref, media))
	if err != nil {
		return "", 0, err
	}
	if msg.Video == nil {
		return "", msg.ID, errors.New("staging upload returned no video metadata")
	}
	return msg.Video.FileID, msg.ID, nil
}

// stagingVideoParams builds the SendVideo payload for the staging upload. The
// Width/Height/Duration fields are critical: without them Telegram caches the
// file_id with 0 duration and unknown aspect ratio, so the cached inline
// result renders as a black square with "00:00" duration.
func stagingVideoParams(chatID int64, ref string, media GuestMedia) *bot.SendVideoParams {
	return &bot.SendVideoParams{
		ChatID:            chatID,
		Video:             &models.InputFileString{Data: ref},
		Width:             media.Width,
		Height:            media.Height,
		Duration:          media.Duration,
		SupportsStreaming: true,
	}
}

func stagingAudioParams(chatID int64, ref string, media GuestMedia) *bot.SendAudioParams {
	return &bot.SendAudioParams{
		ChatID:   chatID,
		Audio:    &models.InputFileString{Data: ref},
		Duration: media.Duration,
		Title:    media.Title,
	}
}

func buildCachedResult(resultID, fileID string, audioOnly bool, title string) (json.RawMessage, error) {
	if audioOnly {
		cached := &models.InlineQueryResultCachedAudio{
			ID:          resultID,
			AudioFileID: fileID,
		}
		return cached.MarshalCustom()
	}
	cached := &models.InlineQueryResultCachedVideo{
		ID:          resultID,
		VideoFileID: fileID,
		Title:       title,
	}
	return cached.MarshalCustom()
}

// answerGuestQuery posts to the answerGuestQuery method directly. The result
// argument must be a JSON-serialized InlineQueryResult (with its type field).
func (r *httpGuestResponder) answerGuestQuery(ctx context.Context, queryID string, result json.RawMessage) error {
	payload := struct {
		GuestQueryID string          `json:"guest_query_id"`
		Result       json.RawMessage `json:"result"`
	}{
		GuestQueryID: queryID,
		Result:       result,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	endpoint := r.apiURL + "/bot" + r.botToken + "/answerGuestQuery"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ar struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		ErrorCode   int    `json:"error_code"`
	}
	if err := json.Unmarshal(respBody, &ar); err != nil {
		return fmt.Errorf("decode response %q: %w", string(respBody), err)
	}
	if !ar.OK {
		return fmt.Errorf("answerGuestQuery failed (%d): %s", ar.ErrorCode, ar.Description)
	}
	return nil
}

// guestPoller replaces the SDK's built-in update loop. It long-polls
// getUpdates with both message and guest_message types, dispatching guest
// messages to a custom handler and everything else to onUpdate.
type guestPoller struct {
	apiURL       string
	botToken     string
	httpClient   *http.Client
	allowed      []string
	onGuest      func(ctx context.Context, gm *guestMessage)
	onUpdate     func(ctx context.Context, upd *models.Update)
	pollTimeout  time.Duration
	lastUpdateID int64
}

func newGuestPoller(b *bot.Bot, apiURL, botToken string, onGuest func(context.Context, *guestMessage)) *guestPoller {
	return &guestPoller{
		apiURL:     strings.TrimRight(apiURL, "/"),
		botToken:   botToken,
		httpClient: &http.Client{Timeout: 65 * time.Second},
		allowed: []string{
			"message", "edited_message", "channel_post", "edited_channel_post",
			"callback_query", "guest_message",
		},
		onGuest:     onGuest,
		onUpdate:    b.ProcessUpdate,
		pollTimeout: 60 * time.Second,
	}
}

// Run loops until ctx is cancelled. Errors are logged and retried with backoff.
func (p *guestPoller) Run(ctx context.Context) {
	log.Println("guest poller: starting")
	var backoff time.Duration

	for {
		if ctx.Err() != nil {
			log.Println("guest poller: stopping")
			return
		}
		if backoff > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}

		raws, err := p.fetchUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("guest poller: fetch error: %v", err)
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = 0

		for _, raw := range raws {
			p.dispatch(ctx, raw)
		}
	}
}

func nextBackoff(prev time.Duration) time.Duration {
	if prev == 0 {
		return time.Second
	}
	next := prev * 2
	if next > 30*time.Second {
		next = 30 * time.Second
	}
	return next
}

func (p *guestPoller) fetchUpdates(ctx context.Context) ([]json.RawMessage, error) {
	params := struct {
		Offset         int64    `json:"offset,omitempty"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates,omitempty"`
	}{
		Offset:         atomic.LoadInt64(&p.lastUpdateID) + 1,
		Timeout:        int((p.pollTimeout - time.Second).Seconds()),
		AllowedUpdates: p.allowed,
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal getUpdates: %w", err)
	}

	endpoint := p.apiURL + "/bot" + p.botToken + "/getUpdates"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var envelope struct {
		OK          bool              `json:"ok"`
		Description string            `json:"description"`
		ErrorCode   int               `json:"error_code"`
		Result      []json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !envelope.OK {
		return nil, fmt.Errorf("getUpdates failed (%d): %s", envelope.ErrorCode, envelope.Description)
	}
	return envelope.Result, nil
}

func (p *guestPoller) dispatch(ctx context.Context, raw json.RawMessage) {
	var meta guestUpdate
	if err := json.Unmarshal(raw, &meta); err != nil {
		log.Printf("guest poller: parse meta: %v", err)
		return
	}

	if meta.ID > atomic.LoadInt64(&p.lastUpdateID) {
		atomic.StoreInt64(&p.lastUpdateID, meta.ID)
	}

	if meta.GuestMessage != nil {
		if p.onGuest != nil {
			go p.onGuest(ctx, meta.GuestMessage)
		}
		return
	}

	if p.onUpdate == nil {
		return
	}
	var upd models.Update
	if err := json.Unmarshal(raw, &upd); err != nil {
		log.Printf("guest poller: parse library update: %v", err)
		return
	}
	p.onUpdate(ctx, &upd)
}
