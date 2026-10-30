// Package sensor turns outside signals into observations, and does nothing
// else: no judging, no deciding what matters. A message's text goes to the
// content store; the log gets that it was seen.
package sensor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/collect"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/ingest"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// KindMessage is a message a collector handed on (gocommon/collect), sealed.
const KindMessage = collect.KindMessage

// KindTurn is a turn of the conversation with jarvis, sealed (input-svc).
const KindTurn = "chat.turn"

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
	// Cards, when set, hands the book each KakaoTalk message: the card
	// companies' alerts are read from them.
	Cards func(ctx context.Context, m ingest.Message) error
}

type collected struct {
	ingest.Message
	RowID  int64  `json:"rowid"`
	ChatID int64  `json:"chat_id"`
	Author string `json:"author"`
}

// Take is one message of a collector's.
func (s *Messages) Take(ctx context.Context, q queue.Message) error {
	m, err := s.open(q)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", queue.ErrRefused, q.SourceKey, err)
	}
	name, sent := q.Source, time.Unix(m.SentAt, 0)
	if s.Cards != nil && name == "kakao" {
		if err := s.Cards(ctx, m.Message); err != nil {
			return err
		}
	}
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
	return m, s.unseal(p.Sealed, &m)
}

// unseal reads what a record carries sealed into into.
func (s *Messages) unseal(sealed string, into any) error {
	body, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return err
	}
	plain, err := s.Seal.Open(body)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, into)
}

func (s *Messages) worth(m collected) bool {
	if strings.TrimSpace(m.Text) == "" {
		return false
	}
	return s.Exclude == nil || !s.Exclude.MatchString(m.ChatName)
}

// A turn of the operator's conversation with jarvis is read as one more room:
// what the operator tells the assistant - "다음 주 화요일 치과 예약했어", what it
// found when asked - is read like any conversation. input-svc leaves each turn
// in the stream, sealed.

type turn struct {
	ID         string   `json:"id"`
	Role       string   `json:"role"` // user | agent
	AtISO      string   `json:"atIso"`
	Text       string   `json:"text"`
	Paragraphs []string `json:"paragraphs"`
	// Origin is set on a turn the assistant posted on its own - a job's
	// news, a reminder at its moment, a checklist shown (input-svc's
	// turns.origin) - rather than said in the conversation.
	Origin *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
}

// TakeTurn is one turn said. Only the operator's thread is read here.
func (s *Messages) TakeTurn(ctx context.Context, q queue.Message) error {
	var p struct {
		Thread string `json:"thread"`
		Sealed string `json:"sealed"`
	}
	if err := json.Unmarshal(q.Payload, &p); err != nil {
		return fmt.Errorf("%w: %s: %v", queue.ErrRefused, q.SourceKey, err)
	}
	if p.Thread != "" {
		return nil
	}
	var t turn
	if err := s.unseal(p.Sealed, &t); err != nil {
		return fmt.Errorf("%w: %s: %v", queue.ErrRefused, q.SourceKey, err)
	}
	msg, ok := t.message()
	if msg.SentAt.IsZero() {
		return fmt.Errorf("%w: %s: 턴의 시각을 읽지 못했습니다", queue.ErrRefused, q.SourceKey)
	}
	if !ok {
		return nil
	}
	return mind.Observe(ctx, s.Store, s.Log, msg)
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
