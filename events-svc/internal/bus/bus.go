// Package bus delivers the event log to each subscriber, in order, at least
// once. It decides nothing about an event beyond whether its type matches a
// pattern the subscriber asked for; what to do with it is the subscriber's.
//
// A guest's subscription (store.Subscription.Guest) is the one exception: it
// is sent only what is for the guest (notify.audience "guest"), with the
// guest's token - never the operator's events, never the services' token.
//
// What it does when a delivery fails is the stack's one rule
// (gocommon/health): a consumer that is down costs its events nothing and is
// waited for, in order; an event the consumer is up for but keeps failing on
// is held after health.MaxTries and the cursor moves past it, so one bad event
// no longer stops every notification and card behind it. A held event goes
// again when the consumer takes something after failing, or registers again
// (a restart - usually the fix), out of order but not lost.
package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/events-svc/internal/store"
	"github.com/choigonyok/jarvis/gocommon/health"
)

type Store interface {
	After(ctx context.Context, cursor int64, limit int) ([]store.Event, error)
	Subscriptions(ctx context.Context) ([]store.Subscription, error)
	Advance(ctx context.Context, consumer string, cursor int64, delivered bool) error
	Failed(ctx context.Context, consumer, msg string) error
	ItemFailed(ctx context.Context, consumer string, event int64, msg string) (int, error)
	Hold(ctx context.Context, consumer string, event int64, tries int, msg string) error
	ReleaseHeld(ctx context.Context, consumer string) error
	DueHeld(ctx context.Context, consumer string, limit int) ([]store.HeldEvent, error)
	HeldDelivered(ctx context.Context, consumer string, event int64) error
	HeldFailed(ctx context.Context, consumer string, event int64, msg string) error
}

type Bus struct {
	store Store
	token string
	// guestToken is what a guest's subscription is sent with: the guest's
	// agent knows that one, and only that one.
	guestToken string
	http       *http.Client
	log        *slog.Logger

	mu      sync.Mutex
	wake    map[string]chan struct{}
	running map[string]context.CancelFunc
}

func New(s Store, token, guestToken string, log *slog.Logger) *Bus {
	return &Bus{
		store: s, token: token, guestToken: guestToken, log: log,
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
	// 지워진 구독의 일꾼은 멈춘다.
	live := map[string]bool{}
	for _, sub := range subs {
		live[sub.Consumer] = true
	}
	for consumer, cancel := range b.running {
		if !live[consumer] {
			cancel()
			delete(b.running, consumer)
			delete(b.wake, consumer)
		}
	}
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
		delivered, retryIn, err := b.pass(ctx, consumer)
		wait := 10 * time.Second
		if err != nil {
			b.log.Warn("이벤트를 전달하지 못했습니다", "consumer", consumer, "err", err)
			if retryIn > 0 {
				// The event failed on its own: its tries set the wait.
				wait = retryIn
			} else {
				wait, backoff = backoff, min(backoff*2, 5*time.Minute)
			}
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

// outcome is how a delivery went, as the rule in gocommon/health reads it.
type outcome int

const (
	delivered outcome = iota
	// refused: the consumer says it will never take this one (400, 409, 422
	// ...). It is skipped rather than holding everything behind it.
	refused
	// outage: the consumer is not there to take anything - unreachable,
	// overloaded, refusing the token, without the route. Not the event's
	// fault: no try is counted and the cursor waits.
	outage
	// failed: the consumer is there and could not take this event (a 500).
	failed
)

func judge(status int, err error) outcome {
	switch {
	case err != nil:
		return outage
	case status >= 200 && status < 300:
		return delivered
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusRequestTimeout,
		http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return outage
	}
	if status >= 400 && status < 500 {
		return refused
	}
	return failed
}

func failure(consumer string, status int, err error, id int64) string {
	if err != nil {
		return fmt.Sprintf("%s 에 닿지 못했습니다 (이벤트 %d): %v", consumer, id, err)
	}
	return fmt.Sprintf("%s 가 %d 를 돌려줬습니다 (이벤트 %d)", consumer, status, id)
}

// pass delivers what is past the consumer's cursor, in order. It stops at an
// outage, and at an event the consumer fails on until that event has failed
// health.MaxTries times while the consumer is up - then the event is held and
// the pass goes on past it. It returns how many it got through and, when it
// stopped at an event's own failure, how long that event waits.
func (b *Bus) pass(ctx context.Context, consumer string) (int, time.Duration, error) {
	subs, err := b.store.Subscriptions(ctx)
	if err != nil {
		return 0, 0, err
	}
	var sub *store.Subscription
	for i := range subs {
		if subs[i].Consumer == consumer {
			sub = &subs[i]
		}
	}
	if sub == nil {
		return 0, 0, nil
	}
	events, err := b.store.After(ctx, sub.Cursor, 50)
	if err != nil {
		return 0, 0, err
	}
	// It was failing: the first event it takes says it works again, and
	// what was held for it goes once more.
	recovering := sub.Failures > 0
	n := 0
	for _, e := range events {
		if !Matches(sub.Types, e.Type) || !Reaches(*sub, e) {
			if err := b.store.Advance(ctx, consumer, e.ID, false); err != nil {
				return n, 0, err
			}
			continue
		}
		status, err := b.post(ctx, *sub, e)
		switch judge(status, err) {
		case refused:
			b.log.Warn("소비자가 이벤트를 거절해 건너뜁니다", "consumer", consumer, "id", e.ID, "type", e.Type, "status", status)
			fallthrough
		case delivered:
			if err := b.store.Advance(ctx, consumer, e.ID, true); err != nil {
				return n, 0, err
			}
			n++
			if recovering {
				recovering = false
				if err := b.store.ReleaseHeld(ctx, consumer); err != nil {
					return n, 0, err
				}
			}
		case outage:
			msg := failure(consumer, status, err, e.ID)
			if ferr := b.store.Failed(ctx, consumer, msg); ferr != nil {
				return n, 0, ferr
			}
			return n, 0, errors.New(msg)
		case failed:
			msg := failure(consumer, status, err, e.ID)
			tries, ferr := b.store.ItemFailed(ctx, consumer, e.ID, msg)
			if ferr != nil {
				return n, 0, ferr
			}
			if tries < health.MaxTries || !b.up(ctx, sub.URL) {
				return n, health.Backoff(tries, 5*time.Second, 5*time.Minute), errors.New(msg)
			}
			if err := b.store.Hold(ctx, consumer, e.ID, tries, msg); err != nil {
				return n, 0, err
			}
			b.log.Warn("받지 못하는 이벤트를 격리함으로 빼고 넘어갑니다", "consumer", consumer, "id", e.ID, "type", e.Type, "tries", tries)
			// What it failed on was this event, not the consumer.
			recovering = false
		}
	}
	return n, 0, b.retryHeld(ctx, sub)
}

// retryHeld sends what was held for the consumer and is due again, once:
// what goes through leaves the box, what does not waits for the next
// occasion. An outage stops it, leaving the rest due.
func (b *Bus) retryHeld(ctx context.Context, sub *store.Subscription) error {
	due, err := b.store.DueHeld(ctx, sub.Consumer, 20)
	if err != nil {
		return err
	}
	for _, h := range due {
		if !Reaches(*sub, h.Event) {
			// Never sent to it, so never held for it; should one be there,
			// it still does not go.
			if err := b.store.HeldDelivered(ctx, sub.Consumer, h.Event.ID); err != nil {
				return err
			}
			continue
		}
		status, err := b.post(ctx, *sub, h.Event)
		switch judge(status, err) {
		case delivered, refused:
			if err := b.store.HeldDelivered(ctx, sub.Consumer, h.Event.ID); err != nil {
				return err
			}
			b.log.Info("격리함의 이벤트를 다시 전했습니다", "consumer", sub.Consumer, "id", h.Event.ID, "type", h.Event.Type)
		case outage:
			msg := failure(sub.Consumer, status, err, h.Event.ID)
			_ = b.store.Failed(ctx, sub.Consumer, msg)
			return errors.New(msg)
		default:
			if err := b.store.HeldFailed(ctx, sub.Consumer, h.Event.ID, failure(sub.Consumer, status, err, h.Event.ID)); err != nil {
				return err
			}
		}
	}
	return nil
}

// up is whether the consumer answers its health check: a 500 from a
// consumer that is up is about the event; from one that is not, it is an
// outage. The services answer /health, the agent /healthz.
func (b *Bus) up(ctx context.Context, consumerURL string) bool {
	u, err := url.Parse(consumerURL)
	if err != nil || u.Host == "" {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, path := range []string{"/health", "/healthz"} {
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, u.Scheme+"://"+u.Host+path, nil)
		if err != nil {
			return false
		}
		resp, err := b.http.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			return resp.StatusCode == http.StatusOK
		}
	}
	return false
}

func (b *Bus) post(ctx context.Context, sub store.Subscription, e store.Event) (int, error) {
	body, _ := json.Marshal(e)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	token := b.token
	if sub.Guest {
		token = b.guestToken
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

// Reaches is whether an event may go to a subscription at all: everything
// may go to the services', only what is for the guest to the guest's.
func Reaches(sub store.Subscription, e store.Event) bool {
	return !sub.Guest || (e.Notify != nil && e.Notify.Audience == "guest")
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
