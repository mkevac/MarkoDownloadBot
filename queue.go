package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const statusStarting = "🔍 Starting..."

type Messenger interface {
	Send(ctx context.Context, chatID int64, text string, withCancelID string) (messageID int, err error)
	Edit(ctx context.Context, chatID int64, messageID int, text string, withCancelID string) error
	Delete(ctx context.Context, chatID int64, messageID int) error
}

type DownloadEntry struct {
	ID        string
	ChatID    int64
	URL       string
	Username  string
	AudioOnly bool

	statusMessageID int
	ctx             context.Context
	cancel          context.CancelFunc
	canceled        bool
}

func (e *DownloadEntry) Ctx() context.Context { return e.ctx }
func (e *DownloadEntry) StatusMessageID() int { return e.statusMessageID }
func (e *DownloadEntry) Canceled() bool       { return e.canceled }

func (e *DownloadEntry) ShortID() string {
	if len(e.ID) >= 8 {
		return e.ID[:8]
	}
	return e.ID
}

func (e *DownloadEntry) LogTag() string {
	return e.ShortID() + " " + e.Username
}

type DownloadQueue struct {
	mu         sync.Mutex
	entries    []*DownloadEntry
	wakeup     chan struct{}
	messenger  Messenger
	worker     func(ctx context.Context, e *DownloadEntry)
	parentCtx  context.Context
	processing bool
}

func NewDownloadQueue(parentCtx context.Context, messenger Messenger, worker func(ctx context.Context, e *DownloadEntry)) *DownloadQueue {
	return &DownloadQueue{
		entries:   nil,
		wakeup:    make(chan struct{}, 1),
		messenger: messenger,
		worker:    worker,
		parentCtx: parentCtx,
	}
}

func (q *DownloadQueue) Add(e *DownloadEntry) error {
	e.ctx, e.cancel = context.WithCancel(q.parentCtx)

	q.mu.Lock()
	posSnapshot := len(q.entries)
	q.mu.Unlock()

	text := statusStarting
	if posSnapshot > 0 {
		text = queuedAtText(posSnapshot)
	}

	msgID, err := q.messenger.Send(q.parentCtx, e.ChatID, text, e.ID)
	if err != nil {
		e.cancel()
		return fmt.Errorf("sending status message: %w", err)
	}
	e.statusMessageID = msgID

	q.mu.Lock()
	q.entries = append(q.entries, e)
	size := len(q.entries)
	q.mu.Unlock()

	log.Printf("queue: enqueued [%s] url=%q position=#%d size=%d", e.LogTag(), e.URL, posSnapshot, size)

	select {
	case q.wakeup <- struct{}{}:
	default:
	}

	return nil
}

func (q *DownloadQueue) Cancel(id string) {
	q.mu.Lock()

	idx := indexOfEntry(q.entries, id)
	if idx < 0 {
		q.mu.Unlock()
		return
	}

	e := q.entries[idx]
	e.canceled = true

	if idx == 0 && q.processing {
		q.mu.Unlock()
		log.Printf("queue: cancel running [%s]", e.LogTag())
		e.cancel()
		return
	}

	q.entries = append(q.entries[:idx], q.entries[idx+1:]...)
	followers := append([]*DownloadEntry(nil), q.entries...)
	size := len(q.entries)
	q.mu.Unlock()

	log.Printf("queue: cancel queued [%s] size=%d", e.LogTag(), size)
	e.cancel()
	if e.statusMessageID != 0 {
		if err := q.messenger.Delete(q.parentCtx, e.ChatID, e.statusMessageID); err != nil {
			log.Printf("queue: error deleting canceled status: %v", err)
		}
	}
	q.bumpFollowerPositions(followers)
}

func (q *DownloadQueue) Run() {
	for {
		q.mu.Lock()
		if len(q.entries) == 0 {
			q.mu.Unlock()
			select {
			case <-q.parentCtx.Done():
				return
			case <-q.wakeup:
				continue
			}
		}
		e := q.entries[0]
		q.processing = true
		q.mu.Unlock()

		if e.ctx.Err() == nil {
			log.Printf("queue: starting [%s] url=%q", e.LogTag(), e.URL)
			start := time.Now()
			q.worker(e.ctx, e)
			outcome := "completed"
			if e.ctx.Err() != nil {
				outcome = "canceled"
			}
			log.Printf("queue: %s [%s] in %s", outcome, e.LogTag(), time.Since(start).Round(100*time.Millisecond))
		}

		q.mu.Lock()
		q.processing = false
		if len(q.entries) > 0 && q.entries[0].ID == e.ID {
			q.entries = q.entries[1:]
		}
		followers := append([]*DownloadEntry(nil), q.entries...)
		q.mu.Unlock()

		e.cancel()
		q.bumpFollowerPositions(followers)
	}
}

func (q *DownloadQueue) bumpFollowerPositions(followers []*DownloadEntry) {
	for i, f := range followers {
		if i == 0 {
			continue
		}
		if f.statusMessageID == 0 {
			continue
		}
		if err := q.messenger.Edit(q.parentCtx, f.ChatID, f.statusMessageID, queuedAtText(i), f.ID); err != nil {
			log.Printf("queue: error bumping follower position: %v", err)
		}
	}
}

func queuedAtText(position int) string {
	return fmt.Sprintf("👨‍👦‍👦 Queued at #%d", position)
}

func indexOfEntry(entries []*DownloadEntry, id string) int {
	for i, e := range entries {
		if e.ID == id {
			return i
		}
	}
	return -1
}

