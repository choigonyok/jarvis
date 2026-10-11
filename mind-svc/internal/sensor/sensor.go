// Package sensor turns outside signals into observations, and does nothing
// else: no judging, no deciding what matters. A message's text goes to the
// content store; the log gets that it was seen.
package sensor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Messenger reads a chat collector (kakaotalk-client, imessage-client) by
// row id, from where it left off.
type Messenger struct {
	Name    string // kakao | imessage
	URL     string
	Token   string
	Store   store.Store
	Log     obs.Log
	Exclude *regexp.Regexp // rooms that are alerts, ads, banks
	// Backfill is how far back a first start reads. A first start does not
	// read the years before it: that is a backlog to replay deliberately,
	// not to interpret on the way in.
	Backfill time.Duration
	Every    time.Duration
	Out      *slog.Logger
	HTTP     *http.Client
}

type collected struct {
	RowID    int64  `json:"rowid"`
	ChatID   int64  `json:"chat_id"`
	ChatName string `json:"chat_name"`
	Author   string `json:"author"`
	IsMine   bool   `json:"is_mine"`
	Text     string `json:"text"`
	SentAt   int64  `json:"sent_at"`
}

func (s *Messenger) Run(ctx context.Context) {
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		if err := s.Poll(ctx); err != nil && ctx.Err() == nil {
			s.Out.Warn("메시지를 읽지 못했습니다", "sensor", s.Name, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Poll reads what is new and observes it.
func (s *Messenger) Poll(ctx context.Context) error {
	key := "sensor:" + s.Name
	cur, err := s.Store.Cursor(ctx, key)
	if err != nil {
		return err
	}
	first := cur == 0
	since := time.Now().Add(-s.Backfill)
	for range 50 {
		batch, err := s.fetch(ctx, cur)
		if err != nil {
			return err
		}
		for _, m := range batch {
			sent := time.Unix(m.SentAt, 0)
			if (!first || sent.After(since)) && s.worth(m) {
				msg := model.Message{
					Ref: fmt.Sprintf("%s:%d", s.Name, m.RowID), Source: s.Name,
					Room: fmt.Sprintf("%s:%d", s.Name, m.ChatID), RoomName: m.ChatName,
					Sender: m.Author, IsMine: m.IsMine, SentAt: sent, Text: m.Text,
				}
				if err := mind.Observe(ctx, s.Store, s.Log, msg); err != nil {
					return err
				}
			}
			cur = m.RowID
		}
		if err := s.Store.SetCursor(ctx, key, cur); err != nil {
			return err
		}
		if len(batch) < 500 {
			return nil
		}
	}
	return nil
}

func (s *Messenger) worth(m collected) bool {
	if strings.TrimSpace(m.Text) == "" {
		return false
	}
	return s.Exclude == nil || !s.Exclude.MatchString(m.ChatName)
}

func (s *Messenger) fetch(ctx context.Context, after int64) ([]collected, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/messages/after?rowid=%d&limit=500", strings.TrimRight(s.URL, "/"), after), nil)
	if err != nil {
		return nil, err
	}
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %d", s.Name, resp.StatusCode)
	}
	var out struct {
		Messages []collected `json:"messages"`
	}
	return out.Messages, json.NewDecoder(resp.Body).Decode(&out)
}
