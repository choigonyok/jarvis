// Package bus delivers the event log to each subscriber, in order, at least
// once. It decides nothing about an event beyond whether its type matches a
// pattern the subscriber asked for; what to do with it is the subscriber's.
package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/events-svc/internal/store"
)

type Store interface {
	After(ctx context.Context, cursor int64, limit int) ([]store.Event, error)
	Subscriptions(ctx context.Context) ([]store.Subscription, error)
	Advance(ctx context.Context, consumer string, cursor int64, delivered bool) error
	Failed(ctx context.Context, consumer, msg string) error
}

type Bus struct {
	store Store
	token string
	http  *http.Client
	log   *slog.Logger

	mu      sync.Mutex
	wake    map[string]chan struct{}
	running map[string]context.CancelFunc
}

func New(s Store, token string, log *slog.Logger) *Bus {
	return &Bus{
		store: s, token: token, log: log,
		http:    &http.Client{Timeout: 30 * time.Second},
		wake:    map[string]chan struct{}{},
		running: map[string]context.CancelFunc{},
	}
}

// Notify wakes every worker: a new event is in.
func (b *Bus) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.wake {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Run keeps one worker per subscription, picking up new subscriptions as
// they register.
func (b *Bus) Run(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		b.sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Sync starts workers for subscriptions that have none. Called by Run and
// right after a consumer registers.
func (b *Bus) Sync(ctx context.Context) { b.sync(ctx) }

func (b *Bus) sync(ctx context.Context) {
	subs, err := b.store.Subscriptions(ctx)
	if err != nil {
		b.log.Warn("구독을 읽지 못했습니다", "err", err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range subs {
		if _, ok := b.running[sub.Consumer]; ok {
			continue
		}
		wctx, cancel := context.WithCancel(ctx)
		ch := make(chan struct{}, 1)
		b.wake[sub.Consumer] = ch
		b.running[sub.Consumer] = cancel
		go b.worker(wctx, sub.Consumer, ch)
	}
}

func (b *Bus) worker(ctx context.Context, consumer string, wake chan struct{}) {
	backoff := time.Second
	for {
		delivered, err := b.pass(ctx, consumer)
		wait := 10 * time.Second
		if err != nil {
			b.log.Warn("이벤트를 전달하지 못했습니다", "consumer", consumer, "err", err)
			_ = b.store.Failed(ctx, consumer, err.Error())
			wait, backoff = backoff, min(backoff*2, 5*time.Minute)
		} else {
			backoff = time.Second
			if delivered > 0 {
				continue // there may be more
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
			if err != nil {
				// A woken worker still honours its backoff after a failure.
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		case <-time.After(wait):
		}
	}
}

// pass delivers what is past the consumer's cursor, stopping at the first
// event the consumer could not take. It returns how many it got through.
func (b *Bus) pass(ctx context.Context, consumer string) (int, error) {
	subs, err := b.store.Subscriptions(ctx)
	if err != nil {
		return 0, err
	}
	var sub *store.Subscription
	for i := range subs {
		if subs[i].Consumer == consumer {
			sub = &subs[i]
		}
	}
	if sub == nil {
		return 0, nil
	}
	events, err := b.store.After(ctx, sub.Cursor, 50)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range events {
		if !Matches(sub.Types, e.Type) {
			if err := b.store.Advance(ctx, consumer, e.ID, false); err != nil {
				return n, err
			}
			continue
		}
		status, err := b.post(ctx, sub.URL, e)
		switch {
		case err != nil:
			return n, err
		case status >= 200 && status < 300:
		case status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests:
			// The consumer says this one will never be taken: skip it
			// rather than hold everything behind it.
			b.log.Warn("소비자가 이벤트를 거절해 건너뜁니다", "consumer", consumer, "id", e.ID, "type", e.Type, "status", status)
		default:
			return n, fmt.Errorf("%s 가 %d 를 돌려줬습니다 (이벤트 %d)", consumer, status, e.ID)
		}
		if err := b.store.Advance(ctx, consumer, e.ID, true); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (b *Bus) post(ctx context.Context, url string, e store.Event) (int, error) {
	body, _ := json.Marshal(e)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

// Matches reads a subscription's patterns: "*", an exact type, or "x.*" for
// every type in category x.
func Matches(patterns []string, typ string) bool {
	for _, p := range patterns {
		switch {
		case p == "*", p == typ:
			return true
		case strings.HasSuffix(p, ".*") && strings.HasPrefix(typ, strings.TrimSuffix(p, "*")):
			return true
		}
	}
	return false
}
