package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/mkevac/markodownloadbot/stats"
)

func TestExtractGuestURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain url", "@bot https://youtu.be/abc", "https://youtu.be/abc"},
		{"http", "@bot http://example.com/x", "http://example.com/x"},
		{"quoted", `@bot "https://youtu.be/abc"`, "https://youtu.be/abc"},
		{"no url", "@bot hello world", ""},
		{"multiple tokens", "hey @bot please get https://x.com/y for me", "https://x.com/y"},
		{"only mention", "@bot", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractGuestURL(tc.in)
			if got != tc.want {
				t.Fatalf("extractGuestURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStagingVideoParamsPropagatesMetadata(t *testing.T) {
	media := GuestMedia{
		Path:     "/tmp/clip.mp4",
		Title:    "ignored on staging",
		Width:    1080,
		Height:   1920,
		Duration: 14,
	}

	params := stagingVideoParams(-100, "file:///app/clip.mp4", media)

	if params.Width != 1080 || params.Height != 1920 {
		t.Errorf("dimensions = %dx%d, want 1080x1920", params.Width, params.Height)
	}
	if params.Duration != 14 {
		t.Errorf("duration = %d, want 14", params.Duration)
	}
	if !params.SupportsStreaming {
		t.Error("SupportsStreaming should be true so Telegram doesn't have to demux")
	}
	if params.ChatID != int64(-100) {
		t.Errorf("ChatID = %v, want -100", params.ChatID)
	}
	ref, ok := params.Video.(*models.InputFileString)
	if !ok {
		t.Fatalf("Video is not InputFileString: %T", params.Video)
	}
	if ref.Data != "file:///app/clip.mp4" {
		t.Errorf("Video ref = %q, want file:///app/clip.mp4", ref.Data)
	}
}

func TestStagingAudioParamsPropagatesMetadata(t *testing.T) {
	media := GuestMedia{
		Path:     "/tmp/song.mp3",
		Title:    "Track 1",
		Duration: 230,
	}

	params := stagingAudioParams(-100, "file:///app/song.mp3", media)

	if params.Duration != 230 {
		t.Errorf("duration = %d, want 230", params.Duration)
	}
	if params.Title != "Track 1" {
		t.Errorf("title = %q, want Track 1", params.Title)
	}
}

func TestBuildCachedResultVideo(t *testing.T) {
	raw, err := buildCachedResult("result-1", "file-abc", false, "My Video")
	if err != nil {
		t.Fatalf("buildCachedResult: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, string(raw))
	}

	if got["type"] != "video" {
		t.Errorf("type = %v, want video", got["type"])
	}
	if got["id"] != "result-1" {
		t.Errorf("id = %v, want result-1", got["id"])
	}
	if got["video_file_id"] != "file-abc" {
		t.Errorf("video_file_id = %v, want file-abc", got["video_file_id"])
	}
	if got["title"] != "My Video" {
		t.Errorf("title = %v, want My Video", got["title"])
	}
}

func TestBuildCachedResultAudio(t *testing.T) {
	raw, err := buildCachedResult("result-2", "file-xyz", true, "ignored title")
	if err != nil {
		t.Fatalf("buildCachedResult: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, string(raw))
	}

	if got["type"] != "audio" {
		t.Errorf("type = %v, want audio", got["type"])
	}
	if got["audio_file_id"] != "file-xyz" {
		t.Errorf("audio_file_id = %v, want file-xyz", got["audio_file_id"])
	}
	if _, hasVideoID := got["video_file_id"]; hasVideoID {
		t.Errorf("audio result should not carry video_file_id")
	}
}

func TestAnswerGuestQueryPostsExpectedPayload(t *testing.T) {
	var (
		gotPath string
		gotBody map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"inline_message_id":"x"}}`))
	}))
	defer srv.Close()

	r := &httpGuestResponder{
		apiURL:     srv.URL,
		botToken:   "TOKEN",
		httpClient: srv.Client(),
	}

	result, err := buildCachedResult("rid", "fid", false, "T")
	if err != nil {
		t.Fatalf("buildCachedResult: %v", err)
	}

	if err := r.answerGuestQuery(context.Background(), "Q-123", result); err != nil {
		t.Fatalf("answerGuestQuery: %v", err)
	}

	if gotPath != "/botTOKEN/answerGuestQuery" {
		t.Errorf("path = %q, want /botTOKEN/answerGuestQuery", gotPath)
	}
	if gotBody["guest_query_id"] != "Q-123" {
		t.Errorf("guest_query_id = %v", gotBody["guest_query_id"])
	}
	inner, ok := gotBody["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not object: %v", gotBody["result"])
	}
	if inner["type"] != "video" || inner["video_file_id"] != "fid" {
		t.Errorf("inner result wrong: %v", inner)
	}
}

func TestAnswerGuestQueryReturnsTelegramError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"BAD_REQUEST"}`))
	}))
	defer srv.Close()

	r := &httpGuestResponder{apiURL: srv.URL, botToken: "T", httpClient: srv.Client()}

	err := r.answerGuestQuery(context.Background(), "Q", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "BAD_REQUEST") || !strings.Contains(err.Error(), "400") {
		t.Errorf("error missing detail: %v", err)
	}
}

func TestGuestPollerDispatchRoutesGuestMessage(t *testing.T) {
	var (
		mu          sync.Mutex
		gotGuest    *guestMessage
		gotUpdates  []*models.Update
		guestCalled = make(chan struct{}, 1)
	)

	p := &guestPoller{
		onGuest: func(_ context.Context, gm *guestMessage) {
			mu.Lock()
			gotGuest = gm
			mu.Unlock()
			guestCalled <- struct{}{}
		},
		onUpdate: func(_ context.Context, upd *models.Update) {
			mu.Lock()
			gotUpdates = append(gotUpdates, upd)
			mu.Unlock()
		},
	}

	raw := json.RawMessage(`{
		"update_id": 42,
		"guest_message": {
			"message_id": 7,
			"date": 1700000000,
			"text": "@bot https://x.com/y",
			"guest_query_id": "Q-99",
			"from": {"id": 1, "username": "alice", "first_name": "A", "last_name": "L"},
			"chat": {"id": -100, "type": "supergroup", "title": "Demo"}
		}
	}`)

	p.dispatch(context.Background(), raw)

	<-guestCalled
	mu.Lock()
	defer mu.Unlock()

	if gotGuest == nil {
		t.Fatal("guest handler not called")
	}
	if gotGuest.GuestQueryID != "Q-99" {
		t.Errorf("query id = %q", gotGuest.GuestQueryID)
	}
	if gotGuest.From == nil || gotGuest.From.Username != "alice" {
		t.Errorf("from parsed wrong: %+v", gotGuest.From)
	}
	if len(gotUpdates) != 0 {
		t.Errorf("library handler should not have been called: %+v", gotUpdates)
	}
	if p.lastUpdateID != 42 {
		t.Errorf("lastUpdateID = %d, want 42", p.lastUpdateID)
	}
}

func TestGuestPollerDispatchRoutesRegularMessage(t *testing.T) {
	var (
		mu         sync.Mutex
		gotGuest   *guestMessage
		gotUpdates []*models.Update
		done       = make(chan struct{}, 1)
	)

	p := &guestPoller{
		onGuest: func(_ context.Context, gm *guestMessage) {
			mu.Lock()
			gotGuest = gm
			mu.Unlock()
		},
		onUpdate: func(_ context.Context, upd *models.Update) {
			mu.Lock()
			gotUpdates = append(gotUpdates, upd)
			mu.Unlock()
			done <- struct{}{}
		},
	}

	raw := json.RawMessage(`{
		"update_id": 5,
		"message": {
			"message_id": 1,
			"date": 1700000000,
			"text": "hi",
			"chat": {"id": 100, "type": "private"},
			"from": {"id": 100, "username": "bob", "first_name": "B"}
		}
	}`)

	p.dispatch(context.Background(), raw)

	<-done
	mu.Lock()
	defer mu.Unlock()

	if gotGuest != nil {
		t.Errorf("guest handler called for non-guest update: %+v", gotGuest)
	}
	if len(gotUpdates) != 1 {
		t.Fatalf("expected 1 library update, got %d", len(gotUpdates))
	}
	if gotUpdates[0].Message == nil || gotUpdates[0].Message.Text != "hi" {
		t.Errorf("library update parsed wrong: %+v", gotUpdates[0])
	}
	if p.lastUpdateID != 5 {
		t.Errorf("lastUpdateID = %d, want 5", p.lastUpdateID)
	}
}

// fakeGuestResponder lets tests verify the orchestrator's calls without
// touching Telegram. See feedback-testability.
type fakeGuestResponder struct {
	mu        sync.Mutex
	mediaErr  error
	errorErr  error
	mediaCall struct {
		queryID, resultID string
		media             GuestMedia
		audioOnly         bool
		called            bool
	}
	errorCall struct {
		queryID, resultID, message string
		called                     bool
	}
}

func (f *fakeGuestResponder) RespondMedia(_ context.Context, queryID, resultID string, audioOnly bool, media GuestMedia) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mediaCall.queryID = queryID
	f.mediaCall.resultID = resultID
	f.mediaCall.media = media
	f.mediaCall.audioOnly = audioOnly
	f.mediaCall.called = true
	return f.mediaErr
}

func (f *fakeGuestResponder) RespondError(_ context.Context, queryID, resultID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errorCall.queryID = queryID
	f.errorCall.resultID = resultID
	f.errorCall.message = message
	f.errorCall.called = true
	return f.errorErr
}

// Ensure the fake satisfies the interface at compile time.
var _ GuestResponder = (*fakeGuestResponder)(nil)

// withGuestTestEnv swaps in a non-nil downloadQueue and a fake responder for
// the duration of the test. handleGuestMessage gates on both being set, and
// the queue isn't actually exercised on the silent path.
func withGuestTestEnv(t *testing.T) *fakeGuestResponder {
	t.Helper()
	stats.Init(t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	prevQueue := downloadQueue
	downloadQueue = NewDownloadQueue(ctx, newFakeMessenger(), func(context.Context, *DownloadEntry) {})
	t.Cleanup(func() { downloadQueue = prevQueue })

	fake := &fakeGuestResponder{}
	prevResponder := currentGuestResponder
	currentGuestResponder = fake
	t.Cleanup(func() { currentGuestResponder = prevResponder })

	return fake
}

func TestHandleGuestMessage_NoURLStaysSilent(t *testing.T) {
	fake := withGuestTestEnv(t)

	gm := &guestMessage{
		MessageID:    7,
		GuestQueryID: "Q-no-url",
		Text:         "какая у нас похожая лента, Марко 😂",
		From:         &guestUser{ID: 42, Username: "nikita", FirstName: "Nikita"},
	}

	handleGuestMessage(context.Background(), gm)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.errorCall.called {
		t.Errorf("RespondError was called for a no-URL guest message (msg=%q); want silent",
			fake.errorCall.message)
	}
	if fake.mediaCall.called {
		t.Errorf("RespondMedia was called for a no-URL guest message; want silent")
	}
}

func TestHandleGuestMessage_OnlyMentionStaysSilent(t *testing.T) {
	fake := withGuestTestEnv(t)

	gm := &guestMessage{
		MessageID:    8,
		GuestQueryID: "Q-only-mention",
		Text:         "@MarkoDownloadBot",
		From:         &guestUser{ID: 43, Username: "someone"},
	}

	handleGuestMessage(context.Background(), gm)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.errorCall.called {
		t.Errorf("RespondError was called for a mention-only guest message (msg=%q); want silent",
			fake.errorCall.message)
	}
}
