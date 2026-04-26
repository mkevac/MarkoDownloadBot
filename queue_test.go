package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeMessage struct {
	chatID    int64
	messageID int
	text      string
	cancelID  string
	deleted   bool
}

type fakeMessenger struct {
	mu       sync.Mutex
	messages map[int]*fakeMessage
	nextID   int
	sendErr  error
}

func newFakeMessenger() *fakeMessenger {
	return &fakeMessenger{messages: map[int]*fakeMessage{}}
}

func (f *fakeMessenger) Send(_ context.Context, chatID int64, text, withCancelID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return 0, f.sendErr
	}
	f.nextID++
	id := f.nextID
	f.messages[id] = &fakeMessage{chatID: chatID, messageID: id, text: text, cancelID: withCancelID}
	return id, nil
}

func (f *fakeMessenger) Edit(_ context.Context, chatID int64, messageID int, text, withCancelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.messages[messageID]
	if !ok {
		return fmt.Errorf("no message %d", messageID)
	}
	m.text = text
	m.cancelID = withCancelID
	m.chatID = chatID
	return nil
}

func (f *fakeMessenger) Delete(_ context.Context, _ int64, messageID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.messages[messageID]; ok {
		m.deleted = true
	}
	return nil
}

func (f *fakeMessenger) get(messageID int) fakeMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.messages[messageID]; ok {
		return *m
	}
	return fakeMessage{}
}

func waitFor(t *testing.T, deadline time.Duration, cond func() bool, why string) {
	t.Helper()
	start := time.Now()
	for {
		if cond() {
			return
		}
		if time.Since(start) > deadline {
			t.Fatalf("timeout waiting for: %s", why)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestQueue_SingleEntry_DeletesOnSuccess(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newFakeMessenger()

	done := make(chan struct{})
	worker := func(_ context.Context, e *DownloadEntry) {
		_ = m.Delete(ctx, e.ChatID, e.statusMessageID)
		close(done)
	}
	q := NewDownloadQueue(ctx, m, worker)
	go q.Run()

	e := &DownloadEntry{ID: "a", ChatID: 1, URL: "u"}
	if err := q.Add(e); err != nil {
		t.Fatal(err)
	}

	if got := m.get(e.statusMessageID).text; got != statusStarting {
		t.Fatalf("expected first status=%q, got %q", statusStarting, got)
	}

	<-done
	waitFor(t, time.Second, func() bool { return m.get(e.statusMessageID).deleted }, "message deleted")
}

func TestQueue_PositionDisplayAndBump(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newFakeMessenger()

	releaseHead := make(chan struct{})
	worker := func(workerCtx context.Context, _ *DownloadEntry) {
		<-releaseHead
	}
	q := NewDownloadQueue(ctx, m, worker)
	go q.Run()

	a := &DownloadEntry{ID: "a", ChatID: 1, URL: "u"}
	b := &DownloadEntry{ID: "b", ChatID: 2, URL: "u"}
	c := &DownloadEntry{ID: "c", ChatID: 3, URL: "u"}
	for _, e := range []*DownloadEntry{a, b, c} {
		if err := q.Add(e); err != nil {
			t.Fatal(err)
		}
	}

	if got := m.get(a.statusMessageID).text; got != statusStarting {
		t.Errorf("a: want %q got %q", statusStarting, got)
	}
	if got, want := m.get(b.statusMessageID).text, queuedAtText(1); got != want {
		t.Errorf("b initial: want %q got %q", want, got)
	}
	if got, want := m.get(c.statusMessageID).text, queuedAtText(2); got != want {
		t.Errorf("c initial: want %q got %q", want, got)
	}

	close(releaseHead)

	// After a finishes, b becomes head (no edit by us — worker would do it),
	// and c (was at #2) bumps down to #1.
	waitFor(t, time.Second, func() bool {
		return m.get(c.statusMessageID).text == queuedAtText(1)
	}, "c bumped to #1 after a finishes")
}

func TestQueue_CancelQueuedRemovesAndBumps(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newFakeMessenger()

	releaseHead := make(chan struct{})
	defer close(releaseHead)
	worker := func(workerCtx context.Context, _ *DownloadEntry) {
		select {
		case <-releaseHead:
		case <-workerCtx.Done():
		}
	}
	q := NewDownloadQueue(ctx, m, worker)
	go q.Run()

	a := &DownloadEntry{ID: "a", ChatID: 1, URL: "u"}
	b := &DownloadEntry{ID: "b", ChatID: 2, URL: "u"}
	c := &DownloadEntry{ID: "c", ChatID: 3, URL: "u"}
	for _, e := range []*DownloadEntry{a, b, c} {
		if err := q.Add(e); err != nil {
			t.Fatal(err)
		}
	}

	q.Cancel("b")

	waitFor(t, time.Second, func() bool {
		return m.get(b.statusMessageID).deleted &&
			m.get(c.statusMessageID).text == queuedAtText(1)
	}, "b's status deleted and c bumped to #1")

	if !b.canceled {
		t.Error("b.canceled should be true")
	}
	if a.canceled || c.canceled {
		t.Error("only b should be marked canceled")
	}
}

func TestQueue_CancelRunningPropagatesCtx(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newFakeMessenger()

	observed := make(chan error, 1)
	worker := func(workerCtx context.Context, e *DownloadEntry) {
		<-workerCtx.Done()
		observed <- workerCtx.Err()
		_ = m.Delete(ctx, e.ChatID, e.statusMessageID)
	}
	q := NewDownloadQueue(ctx, m, worker)
	go q.Run()

	a := &DownloadEntry{ID: "a", ChatID: 1, URL: "u"}
	if err := q.Add(a); err != nil {
		t.Fatal(err)
	}

	// Wait until processing starts.
	waitFor(t, time.Second, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.processing
	}, "queue to start processing head")

	q.Cancel("a")

	select {
	case err := <-observed:
		if err == nil {
			t.Fatal("worker observed nil ctx error after Cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not observe cancellation within 1s")
	}

	waitFor(t, time.Second, func() bool { return m.get(a.statusMessageID).deleted }, "status deleted on cancel")
}

func TestQueue_CancelButtonAttachedToInitialMessage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newFakeMessenger()

	block := make(chan struct{})
	defer close(block)
	worker := func(_ context.Context, _ *DownloadEntry) { <-block }
	q := NewDownloadQueue(ctx, m, worker)
	go q.Run()

	a := &DownloadEntry{ID: "abc-123", ChatID: 1, URL: "u"}
	if err := q.Add(a); err != nil {
		t.Fatal(err)
	}
	if got := m.get(a.statusMessageID).cancelID; got != "abc-123" {
		t.Errorf("expected cancelID=abc-123 got %q", got)
	}
}

func TestQueue_AddSendErrorRollsBack(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newFakeMessenger()
	m.sendErr = fmt.Errorf("boom")

	q := NewDownloadQueue(ctx, m, func(context.Context, *DownloadEntry) {})

	a := &DownloadEntry{ID: "a", ChatID: 1, URL: "u"}
	err := q.Add(a)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got %v", err)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.entries) != 0 {
		t.Fatalf("entries should be empty after rollback, got %d", len(q.entries))
	}
}
