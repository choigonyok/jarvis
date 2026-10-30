// Package sensor turns outside signals into observations, and does nothing
// else: no judging, no deciding what matters. A message's text goes to the
// content store; the log gets that it was seen.
package sensor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/collect"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// KindMessage is a message a collector handed on (gocommon/collect), sealed.
const KindMessage = collect.KindMessage

// Messages takes what the chat collectors (kakaotalk-client, imessage-client)
// hand on. Every message comes; which are worth reading is said here.
type Messages struct {
	Store   store.Store
	Log     obs.Log
	Seal    *seal.Sealer
	Exclude *regexp.Regexp // rooms that are alerts, ads, banks
	// Payments are the payment apps' rooms ("카카오페이"): not read as
	// conversation, but a notice of money received in them is a transfer
	// (Occur records it).
	Payments *regexp.Regexp
	Occur    Observer
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

// Take is one message of a collector's.
func (s *Messages) Take(ctx context.Context, q queue.Message) error {
	m, err := s.open(q)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", queue.ErrRefused, q.SourceKey, err)
	}
	name, sent := q.Source, time.Unix(m.SentAt, 0)
	if s.Payments != nil && s.Occur != nil && s.Payments.MatchString(m.ChatName) {
		if o, ok := Transfer(fmt.Sprintf("%s-pay:%d", name, m.RowID), sent, m.Text); ok {
			if err := s.Occur.Observe(ctx, o); err != nil {
				return err
			}
		}
	}
	if !s.worth(m) {
		return nil
	}
	return mind.Observe(ctx, s.Store, s.Log, model.Message{
		Ref: fmt.Sprintf("%s:%d", name, m.RowID), Source: name,
		Room: fmt.Sprintf("%s:%d", name, m.ChatID), RoomName: m.ChatName,
		Sender: m.Author, IsMine: m.IsMine, SentAt: sent, Text: m.Text,
	})
}

// open is the message as the collector kept it.
func (s *Messages) open(q queue.Message) (collected, error) {
	var m collected
	var p struct {
		Sealed string `json:"sealed"`
	}
	if err := json.Unmarshal(q.Payload, &p); err != nil {
		return m, err
	}
	body, err := base64.StdEncoding.DecodeString(p.Sealed)
	if err != nil {
		return m, err
	}
	plain, err := s.Seal.Open(body)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(plain, &m)
}

func (s *Messages) worth(m collected) bool {
	if strings.TrimSpace(m.Text) == "" {
		return false
	}
	return s.Exclude == nil || !s.Exclude.MatchString(m.ChatName)
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
	// Origin is set on a turn the assistant posted on its own - a job's
	// news, a reminder at its moment, a checklist shown (chat-svc's
	// turns.origin) - rather than said in the conversation.
	Origin *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
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
	turns, err := s.turns(ctx, since)
	if err != nil {
		return err
	}
	last := since
	for _, t := range turns {
		msg, ok := t.message()
		if msg.SentAt.IsZero() {
			if s.Out != nil {
				s.Out.Warn("chat turn time parse failed", "id", t.ID, "at", t.AtISO)
			}
			continue
		}
		if !msg.SentAt.After(since) {
			continue
		}
		if ok {
			if err := mind.Observe(ctx, s.Store, s.Log, msg); err != nil {
				return err
			}
		}
		if msg.SentAt.After(last) {
			last = msg.SentAt
		}
	}
	if last.After(since) || cur == 0 {
		return s.Store.SetCursor(ctx, "sensor:chat", last.UnixMicro())
	}
	return nil
}

func (s *Chat) turns(ctx context.Context, since time.Time) ([]turn, error) {
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.URL, "/")+"/turns?thread=&since="+url.QueryEscape(since.Format(time.RFC3339Nano)), nil)
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
		return nil, fmt.Errorf("chat-svc: %d", resp.StatusCode)
	}
	var out struct {
		Turns []turn `json:"turns"`
	}
	return out.Turns, json.NewDecoder(resp.Body).Decode(&out)
}

// message is a turn as a message of the jarvis room; ok is false when it has
// no text. A turn whose time does not parse has a zero SentAt. A turn with an
// origin is mind's own output come back - a reminder it rang, a checklist it
// showed, a job's news - and is kept as a notice: context for what the
// operator says to it, never read as something said.
func (t turn) message() (model.Message, bool) {
	at, err := time.Parse(time.RFC3339Nano, t.AtISO)
	if err != nil {
		return model.Message{}, false
	}
	text := strings.TrimSpace(t.Text)
	if text == "" {
		text = strings.TrimSpace(strings.Join(t.Paragraphs, "\n"))
	}
	msg := model.Message{
		Ref: "chat:" + t.ID, Source: "chat", Room: model.OwnerRoom, RoomName: "jarvis",
		IsMine: t.Role == "user", SentAt: at, Text: text,
	}
	switch {
	case t.Origin != nil:
		msg.IsMine, msg.Sender, msg.Notice = false, model.NoticeSender, true
	case !msg.IsMine:
		msg.Sender = "Jarvis(비서)"
	}
	return msg, text != ""
}
