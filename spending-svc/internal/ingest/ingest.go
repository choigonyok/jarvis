// Package ingest pulls card alerts out of the KakaoTalk collector.
//
// It reads the collector's feed by source ROWID, keeps its own cursor, and
// only looks at rooms that are card companies. Every alert is recorded raw
// before it is read, so a better parser later can replay what came in.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/spending-svc/internal/parse"
	"github.com/choigonyok/jarvis/spending-svc/internal/store"
)

type message struct {
	RowID     int64  `json:"rowid"`
	MessageID string `json:"message_id"`
	ChatName  string `json:"chat_name"`
	ChatType  string `json:"chat_type"`
	IsMine    bool   `json:"is_mine"`
	Text      string `json:"text"`
	SentAt    int64  `json:"sent_at"`
}

// Status is what the screen shows under the total: is anything arriving.
type Status struct {
	// LastPollAt / LastError are about reaching the collector.
	LastPollAt time.Time `json:"lastPollAt"`
	LastError  string    `json:"lastError,omitempty"`
	// LatestMessageAt is the newest KakaoTalk message of any kind. If that is
	// old, the KakaoTalk app on the Mac is probably not running - the
	// collector itself cannot tell, it only sees a file that stopped changing.
	LatestMessageAt *time.Time `json:"latestMessageAt"`
	// LastAlertAt is the newest card alert.
	LastAlertAt *time.Time `json:"lastAlertAt"`
	Stale       bool       `json:"stale"`
}

type Ingester struct {
	base     string
	token    string
	extra    []string
	store    *store.Store
	interval time.Duration
	staleIn  time.Duration
	client   *http.Client
	log      *slog.Logger
	loc      *time.Location

	mu     sync.RWMutex
	status Status

	rate rateCache
}

func New(base, token string, extraChannels []string, st *store.Store, interval, staleIn time.Duration, log *slog.Logger) *Ingester {
	return &Ingester{
		base:     strings.TrimRight(base, "/"),
		token:    token,
		extra:    extraChannels,
		store:    st,
		interval: interval,
		staleIn:  staleIn,
		client:   &http.Client{Timeout: 15 * time.Second},
		log:      log,
		loc:      time.Local,
	}
}

func (in *Ingester) Status() Status {
	in.mu.RLock()
	defer in.mu.RUnlock()
	return in.status
}

// IsCardChannel says whether a room is a card company. Any room with 카드 in
// its name, plus whatever was configured - and never a group chat, where a
// friend saying "카드 승인 났어 12,000원" is not a charge.
func (in *Ingester) IsCardChannel(m message) bool {
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

func (in *Ingester) Run(ctx context.Context) {
	in.refreshStatus(ctx)
	tick := time.NewTicker(in.interval)
	defer tick.Stop()
	statusTick := time.NewTicker(time.Minute)
	defer statusTick.Stop()
	for {
		if err := in.pollOnce(ctx); err != nil && ctx.Err() == nil {
			in.log.Warn("카드 알림을 읽지 못했습니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-statusTick.C:
			in.refreshStatus(ctx)
		case <-tick.C:
		}
	}
}

func (in *Ingester) pollOnce(ctx context.Context) error {
	cursor, err := in.store.Cursor(ctx)
	if err != nil {
		return in.fail(fmt.Errorf("커서: %w", err))
	}
	for {
		var payload struct {
			Messages []message `json:"messages"`
		}
		q := url.Values{"rowid": {fmt.Sprint(cursor)}, "limit": {"500"}}
		if err := in.get(ctx, "/messages/after?"+q.Encode(), &payload); err != nil {
			return in.fail(err)
		}
		for _, m := range payload.Messages {
			if in.IsCardChannel(m) {
				if err := in.handle(ctx, m); err != nil {
					// Stop here and keep the cursor before this message: the
					// next tick tries it again rather than skipping a charge.
					return in.fail(fmt.Errorf("메시지 %s: %w", m.MessageID, err))
				}
			}
			cursor = m.RowID
		}
		if len(payload.Messages) > 0 {
			if err := in.store.SetCursor(ctx, cursor); err != nil {
				return in.fail(err)
			}
		}
		if len(payload.Messages) < 500 {
			break
		}
	}
	in.mu.Lock()
	in.status.LastPollAt = time.Now()
	in.status.LastError = ""
	in.mu.Unlock()
	return nil
}

func (in *Ingester) handle(ctx context.Context, m message) error {
	sentAt := time.Unix(m.SentAt, 0).In(in.loc)
	alert, outcome := parse.Parse(m.ChatName, m.Text, sentAt)
	if outcome == parse.NotPayment {
		return nil
	}
	eventID, err := in.store.RawEvent(ctx, m.MessageID, m.ChatName, m.Text, sentAt)
	if err != nil {
		return err
	}
	in.mu.Lock()
	if in.status.LastAlertAt == nil || sentAt.After(*in.status.LastAlertAt) {
		in.status.LastAlertAt = &sentAt
	}
	in.mu.Unlock()

	if outcome == parse.Unreadable {
		in.log.Info("읽지 못한 카드 알림을 확인 대기열에 넣습니다", "chat", m.ChatName)
		return in.store.AddUnparsed(ctx, m.MessageID, m.ChatName, m.Text, sentAt)
	}

	amount, estimated := alert.AmountKrw, false
	if amount == 0 && alert.Currency == "USD" {
		rate, err := in.rate.usdKrw(ctx, in.client)
		if err != nil {
			// Without a rate the won figure would be a guess with nothing to
			// anchor it; let a person write it in.
			return in.store.AddUnparsed(ctx, m.MessageID, m.ChatName, m.Text, sentAt)
		}
		amount, estimated = int64(math.Round(alert.ForeignAmount*rate)), true
	}
	added, err := in.store.AddAlert(ctx, m.MessageID, eventID, alert, amount, estimated)
	if added {
		in.log.Info("결제를 기록했습니다", "issuer", alert.Issuer, "kind", alert.Kind, "merchant", alert.Merchant, "amount", amount)
	}
	return err
}

func (in *Ingester) refreshStatus(ctx context.Context) {
	var payload struct {
		Messages []message `json:"messages"`
	}
	err := in.get(ctx, "/messages/recent?limit=1", &payload)
	last, _ := in.store.LastAlertAt(ctx)

	in.mu.Lock()
	defer in.mu.Unlock()
	if last != nil {
		t := last.In(in.loc)
		in.status.LastAlertAt = &t
	}
	if err != nil {
		in.status.LastError = err.Error()
		return
	}
	if len(payload.Messages) > 0 {
		t := time.Unix(payload.Messages[0].SentAt, 0).In(in.loc)
		in.status.LatestMessageAt = &t
		in.status.Stale = time.Since(t) > in.staleIn
	}
}

func (in *Ingester) fail(err error) error {
	in.mu.Lock()
	in.status.LastPollAt = time.Now()
	in.status.LastError = err.Error()
	in.mu.Unlock()
	return err
}

func (in *Ingester) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.base+path, nil)
	if err != nil {
		return err
	}
	if in.token != "" {
		req.Header.Set("Authorization", "Bearer "+in.token)
	}
	res, err := in.client.Do(req)
	if err != nil {
		return fmt.Errorf("카톡 수집기에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("카톡 수집기가 거부했습니다: %s", res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// rateCache holds a dollar rate for an hour. Upbit's USDT market is the same
// public source assets-svc uses: no key, and close enough to the card
// network's rate for an estimate that is labelled as one.
type rateCache struct {
	mu   sync.Mutex
	rate float64
	at   time.Time
}

func (r *rateCache) usdKrw(ctx context.Context, client *http.Client) (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rate > 0 && time.Since(r.at) < time.Hour {
		return r.rate, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.upbit.com/v1/ticker?markets=KRW-USDT", nil)
	if err != nil {
		return 0, err
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	var rows []struct {
		TradePrice float64 `json:"trade_price"`
	}
	if err := json.NewDecoder(res.Body).Decode(&rows); err != nil || len(rows) == 0 || rows[0].TradePrice <= 0 {
		return 0, fmt.Errorf("환율을 읽지 못했습니다")
	}
	r.rate, r.at = rows[0].TradePrice, time.Now()
	return r.rate, nil
}
