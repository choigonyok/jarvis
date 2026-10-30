// Package ingest reads card alerts out of the messages going by.
//
// Every message of the KakaoTalk collector comes through the stream, sealed,
// and is opened once (sensor.Messages); this takes the rooms that are card
// companies. What came in stays in the stream as it came, so a better parser
// later can read it again, and reading a message twice changes nothing: a
// charge is keyed by its message.
//
// A message that cannot be recorded is the stream's to retry, under the
// stack's rule (gocommon/queue): a database that does not answer costs the
// message nothing and it waits, and one failing on its own goes to the held
// box after five tries, and the alerts behind it go on.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/parse"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/store"
)

// Message is one message as the collector keeps it.
type Message struct {
	MessageID string `json:"message_id"`
	ChatName  string `json:"chat_name"`
	ChatType  string `json:"chat_type"`
	IsMine    bool   `json:"is_mine"`
	Text      string `json:"text"`
	SentAt    int64  `json:"sent_at"`
}

// Status is what the screen shows under the total: is anything arriving.
type Status struct {
	// LatestMessageAt is the newest KakaoTalk message of any kind. If that is
	// old, the KakaoTalk app on the Mac is probably not running - the
	// collector itself cannot tell, it only sees a file that stopped changing.
	LatestMessageAt *time.Time `json:"latestMessageAt"`
	// LastAlertAt is the newest card alert.
	LastAlertAt *time.Time `json:"lastAlertAt"`
	Stale       bool       `json:"stale"`
}

// Ingester reads alerts into the book.
type Ingester struct {
	extra   []string
	store   *store.Store
	staleIn time.Duration
	log     *slog.Logger
	loc     *time.Location
	now     func() time.Time
}

// New makes an Ingester. extraChannels are rooms to read besides those with
// 카드 in the name; staleIn is how long a silence is before the screen says so.
func New(extraChannels []string, st *store.Store, staleIn time.Duration, log *slog.Logger) *Ingester {
	return &Ingester{extra: extraChannels, store: st, staleIn: staleIn, log: log, loc: time.Local, now: time.Now}
}

// Status says what has been seen. A silence longer than a day's usual
// messages means alerts are not coming in either.
func (in *Ingester) Status(ctx context.Context) (Status, error) {
	seen, err := in.store.Seen(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{LatestMessageAt: seen.Message, LastAlertAt: seen.Alert}
	if seen.Message != nil {
		out.Stale = in.now().Sub(*seen.Message) > in.staleIn
	}
	return out, nil
}

// IsCardChannel says whether a room is a card company. Any room with 카드 in
// its name, plus whatever was configured - and never a group chat, where a
// friend saying "카드 승인 났어 12,000원" is not a charge.
func (in *Ingester) IsCardChannel(m Message) bool {
	if m.IsMine || m.ChatType == "group" {
		return false
	}
	if strings.Contains(m.ChatName, "카드") {
		return true
	}
	for _, c := range in.extra {
		if c != "" && strings.Contains(m.ChatName, c) {
			return true
		}
	}
	return false
}

// Take is one message going by.
func (in *Ingester) Take(ctx context.Context, m Message) error {
	sentAt := time.Unix(m.SentAt, 0).In(in.loc)
	card := in.IsCardChannel(m)
	err := in.handle(ctx, m, sentAt, card)
	if err != nil {
		// A database that does not answer is an outage, not this message.
		if perr := in.store.Ping(ctx); perr != nil {
			return fmt.Errorf("%w: %v", queue.ErrOutage, err)
		}
	}
	return err
}

func (in *Ingester) handle(ctx context.Context, m Message, sentAt time.Time, card bool) error {
	if !card {
		return in.store.Saw(ctx, sentAt, false)
	}
	if err := in.store.Saw(ctx, sentAt, true); err != nil {
		return err
	}
	alert, outcome := parse.Parse(m.ChatName, m.Text, sentAt)
	switch outcome {
	case parse.NotPayment:
		return nil
	case parse.Unreadable:
		in.log.Info("읽지 못한 카드 알림을 확인 대기열에 넣습니다", "chat", m.ChatName)
		return in.store.AddUnparsed(ctx, m.MessageID, m.ChatName, m.Text, sentAt)
	}
	amount, estimated := alert.AmountKrw, false
	if amount == 0 && alert.Currency == "USD" {
		rate, ok, err := in.store.USDKrw(ctx)
		if err != nil {
			return err
		}
		if !ok {
			// Without a rate the won figure would be a guess with nothing to
			// anchor it; let a person write it in.
			return in.store.AddUnparsed(ctx, m.MessageID, m.ChatName, m.Text, sentAt)
		}
		amount, estimated = int64(math.Round(alert.ForeignAmount*rate)), true
	}
	added, err := in.store.AddAlert(ctx, m.MessageID, alert, amount, estimated)
	if added {
		in.log.Info("결제를 기록했습니다", "issuer", alert.Issuer, "kind", alert.Kind, "merchant", alert.Merchant, "amount", amount)
	}
	return err
}
