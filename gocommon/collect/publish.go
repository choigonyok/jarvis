package collect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/seal"
)

// KindMessage is a message as a collector hands it on.
const KindMessage = "message.received"

// Item is one stored message, on its way out.
type Item struct {
	RowID  int64
	ChatID int64
	SentAt int64 // unix seconds
	Body   any   // the message as the collector keeps it
}

// Publisher hands what a collector stored to the stream: every message, sealed.
// Which of them are worth reading is mind's to say. It goes forward from a
// cursor of its own, moved only past what input-svc took, so a stack that is
// down costs a delay and nothing else.
type Publisher struct {
	Source string // kakao | imessage
	After  func(rowid int64, limit int) ([]Item, error)
	// Latest is the newest row: where a first start begins. What came before
	// a publisher existed was read by the one before it.
	Latest     func() (int64, error)
	CursorPath string
	Send       func(ctx context.Context, r record.Record) error
	Seal       *seal.Sealer
	Every      time.Duration
	Loops      *health.Loops
	Log        *slog.Logger
}

const publishBatch = 200

// LoopName is the Publisher's loop on /health.
const LoopName = "publish"

// Run hands on what is new until ctx ends.
func (p *Publisher) Run(ctx context.Context) {
	p.Loops.Add(LoopName, "메시지를 기록으로", 5*time.Minute)
	t := time.NewTicker(p.Every)
	defer t.Stop()
	for {
		if err := p.Round(ctx); err != nil && ctx.Err() == nil {
			p.Log.Warn("메시지를 넘기지 못했습니다", "source", p.Source, "err", err)
			p.Loops.Fail(LoopName, err)
		} else if err == nil {
			p.Loops.OK(LoopName)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Round hands on everything past the cursor.
func (p *Publisher) Round(ctx context.Context) error {
	cur, ok, err := p.cursor()
	if err != nil {
		return err
	}
	if !ok {
		if cur, err = p.Latest(); err != nil {
			return err
		}
		if err := p.save(cur); err != nil {
			return err
		}
	}
	for {
		items, err := p.After(cur, publishBatch)
		if err != nil {
			return err
		}
		for _, it := range items {
			r, err := p.record(it)
			if err != nil {
				return err
			}
			if err := p.Send(ctx, r); err != nil {
				return err
			}
			cur = it.RowID
			if err := p.save(cur); err != nil {
				return err
			}
		}
		if len(items) < publishBatch {
			return nil
		}
	}
}

// record is a message sealed whole: who said it and in which room are as much
// the conversation as what was said.
func (p *Publisher) record(it Item) (record.Record, error) {
	raw, err := json.Marshal(it.Body)
	if err != nil {
		return record.Record{}, err
	}
	body, err := p.Seal.Seal(raw)
	if err != nil {
		return record.Record{}, err
	}
	return record.Record{
		Kind: KindMessage, Source: p.Source, SourceKey: fmt.Sprintf("%s:%d", p.Source, it.RowID), Actor: p.Source,
		Channel: "collector", Subject: fmt.Sprintf("%s:%d", p.Source, it.ChatID), OccurredAt: time.Unix(it.SentAt, 0), Tier: record.Raw,
		Payload: map[string]string{"sealed": base64.StdEncoding.EncodeToString(body)},
	}, nil
}

func (p *Publisher) cursor() (int64, bool, error) {
	raw, err := os.ReadFile(p.CursorPath)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return n, err == nil, err
}

// save writes the cursor whole or not at all.
func (p *Publisher) save(cur int64) error {
	tmp := filepath.Join(filepath.Dir(p.CursorPath), "."+filepath.Base(p.CursorPath)+".tmp")
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(cur, 10)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.CursorPath)
}
