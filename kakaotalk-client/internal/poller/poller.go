// Package poller drives the incremental collection loop: every tick it reads
// source rows newer than the stored cursor and upserts them into the store.
package poller

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/kakaotalk-client/internal/kakao"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/store"
)

type Poller struct {
	reader   *kakao.Reader
	store    *store.Store
	interval time.Duration
	batch    int

	mu       sync.RWMutex
	status   Status
}

type Status struct {
	Running       bool      `json:"running"`
	Interval      string    `json:"interval"`
	LastPollAt    time.Time `json:"last_poll_at"`
	LastError     string    `json:"last_error,omitempty"`
	Cursor        int64     `json:"cursor_rowid"`
	TotalStored   int64     `json:"total_stored"`
	LastInserted  int       `json:"last_inserted"`
	PollCount     int64     `json:"poll_count"`
	LastLatencyMs int64     `json:"last_latency_ms"`
}

func New(r *kakao.Reader, s *store.Store, interval time.Duration, batch int) *Poller {
	if batch <= 0 {
		batch = 500
	}
	return &Poller{reader: r, store: s, interval: interval, batch: batch,
		status: Status{Interval: interval.String()}}
}

// Run blocks until ctx is cancelled, polling on the configured interval.
func (p *Poller) Run(ctx context.Context) {
	cursor, err := p.store.Cursor()
	if err != nil {
		log.Printf("poller: read cursor: %v", err)
	}
	// A fresh store starts from the current tail so we do not backfill the
	// entire history on first run; historical import is a separate concern.
	if cursor == 0 {
		if max, err := p.reader.MaxRowID(); err == nil {
			cursor = max
			_ = p.store.SetCursor(cursor)
			log.Printf("poller: baseline cursor set to rowid=%d", cursor)
		}
	}

	p.repairTruncated()

	p.setRunning(true)
	defer p.setRunning(false)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Poll once immediately, then on each tick.
	cursor = p.pollOnce(cursor)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cursor = p.pollOnce(cursor)
		}
	}
}

const repairFlag = "repair_attachment_body_v2"

// repairTruncated re-reads, once, already-stored rows whose full body lives in
// the attachment (AlimTalk previews were stored cut at 200 chars) and upserts
// them so their text is replaced with the full body.
func (p *Poller) repairTruncated() {
	if done, err := p.store.Flag(repairFlag); err != nil || done {
		return
	}
	lo, hi, err := p.store.RowIDRange()
	if err != nil {
		log.Printf("poller: repair: %v", err)
		return
	}
	if hi > 0 {
		msgs, err := p.reader.MessagesWithAttachmentBody(lo, hi)
		if err != nil {
			log.Printf("poller: repair: %v", err)
			return
		}
		n, err := p.store.UpsertMessages(msgs, time.Now().Unix())
		if err != nil {
			log.Printf("poller: repair: %v", err)
			return
		}
		log.Printf("poller: repair: re-read %d attachment-body messages (rowid %d..%d)", n, lo, hi)
	}
	_ = p.store.SetFlag(repairFlag)
}

// pollOnce drains all rows newer than cursor (in batches) and returns the new
// cursor. Errors are recorded in status and the cursor is left unchanged so the
// next tick retries the same range.
func (p *Poller) pollOnce(cursor int64) int64 {
	start := time.Now()
	total := 0
	for {
		msgs, err := p.reader.MessagesSinceRowID(cursor, p.batch)
		if err != nil {
			p.recordError(err, start)
			return cursor
		}
		if len(msgs) == 0 {
			break
		}
		n, err := p.store.UpsertMessages(msgs, time.Now().Unix())
		if err != nil {
			p.recordError(err, start)
			return cursor
		}
		total += n
		cursor = msgs[len(msgs)-1].RowID
		if err := p.store.SetCursor(cursor); err != nil {
			p.recordError(err, start)
			return cursor
		}
		if len(msgs) < p.batch {
			break
		}
	}
	p.recordSuccess(cursor, total, start)
	return cursor
}

func (p *Poller) recordSuccess(cursor int64, inserted int, start time.Time) {
	stored, _ := p.store.TotalMessages()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.LastPollAt = time.Now()
	p.status.LastError = ""
	p.status.Cursor = cursor
	p.status.LastInserted = inserted
	p.status.TotalStored = stored
	p.status.PollCount++
	p.status.LastLatencyMs = time.Since(start).Milliseconds()
	if inserted > 0 {
		log.Printf("poller: +%d messages (cursor=%d, %dms)", inserted, cursor, p.status.LastLatencyMs)
	}
}

func (p *Poller) recordError(err error, start time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.LastPollAt = time.Now()
	p.status.LastError = err.Error()
	p.status.PollCount++
	p.status.LastLatencyMs = time.Since(start).Milliseconds()
	log.Printf("poller: error: %v", err)
}

func (p *Poller) setRunning(v bool) {
	p.mu.Lock()
	p.status.Running = v
	p.mu.Unlock()
}

func (p *Poller) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}
