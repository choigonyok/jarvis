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

// Chat reads the operator's conversation with jarvis (chat-svc) as one more
// room: what the operator tells the assistant - "다음 주 화요일 치과 예약했어",
// what it found when asked - is read like any conversation.
type Chat struct {
	URL      string
	Token    string
	Store    store.Store
	Log      obs.Log
	Backfill time.Duration
	Every    time.Duration
	Out      *slog.Logger
	HTTP     *http.Client
}

type turn struct {
	ID         string   `json:"id"`
	Role       string   `json:"role"` // user | agent
	AtISO      string   `json:"atIso"`
	Text       string   `json:"text"`
	Paragraphs []string `json:"paragraphs"`
}

func (s *Chat) Run(ctx context.Context) {
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		if err := s.Poll(ctx); err != nil && ctx.Err() == nil {
			s.Out.Warn("jarvis 대화를 읽지 못했습니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Poll observes the turns after the last one seen (by time). The cursor is
// the last turn's time in microseconds.
func (s *Chat) Poll(ctx context.Context) error {
	cur, err := s.Store.Cursor(ctx, "sensor:chat")
	if err != nil {
		return err
	}
	since := time.Now().Add(-s.Backfill)
	if cur > 0 {
		since = time.UnixMicro(cur)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.URL, "/")+"/turns?thread=", nil)
	if err != nil {
		return err
	}
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("chat-svc: %d", resp.StatusCode)
	}
	var out struct {
		Turns []turn `json:"turns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	last := since
	for _, t := range out.Turns {
		at, err := time.Parse(time.RFC3339Nano, t.AtISO)
		if err != nil || !at.After(since) {
			continue
		}
		text := strings.TrimSpace(t.Text)
		if text == "" {
			text = strings.TrimSpace(strings.Join(t.Paragraphs, "\n"))
		}
		if text != "" {
			msg := model.Message{
				Ref: "chat:" + t.ID, Source: "chat", Room: "chat:owner", RoomName: "jarvis",
				IsMine: t.Role == "user", SentAt: at, Text: text,
			}
			if !msg.IsMine {
				msg.Sender = "Jarvis(비서)"
			}
			if err := mind.Observe(ctx, s.Store, s.Log, msg); err != nil {
				return err
			}
		}
		if at.After(last) {
			last = at
		}
	}
	if last.After(since) || cur == 0 {
		return s.Store.SetCursor(ctx, "sensor:chat", last.UnixMicro())
	}
	return nil
}
