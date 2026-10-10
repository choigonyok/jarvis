// Package push delivers one payload to every subscribed device over Web Push
// (VAPID). It is the only way out of notify-svc: no app to install, and a tap
// opens the console on the screen the notification is about.
package push

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/choigonyok/jarvis/notify-svc/internal/store"
)

// Payload is what the console's service worker (web/public/sw.js) reads.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	// Same tag replaces an earlier notification on the device instead of stacking.
	Tag string `json:"tag"`
	// Urgent asks the push service to wake the device now.
	Urgent bool `json:"-"`
}

type Keys struct {
	Public  string
	Private string
	// A contact the push services can reach about abuse: "mailto:..." or a URL.
	Subject string
}

type Pusher struct {
	keys  Keys
	store *store.Store
	log   *slog.Logger
}

func New(keys Keys, s *store.Store, log *slog.Logger) *Pusher {
	return &Pusher{keys: keys, store: s, log: log}
}

func (p *Pusher) Ready() bool { return p.keys.Public != "" && p.keys.Private != "" }

func (p *Pusher) PublicKey() string { return p.keys.Public }

// Send pushes to every device and reports how many took it. A device the push
// service says no longer exists is forgotten; one that fails otherwise keeps
// its place until it has failed many times in a row.
func (p *Pusher) Send(ctx context.Context, pl Payload) (int, error) {
	if !p.Ready() {
		return 0, nil
	}
	subs, err := p.store.Subscriptions(ctx)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(pl)
	if err != nil {
		return 0, err
	}
	urgency := webpush.UrgencyNormal
	if pl.Urgent {
		urgency = webpush.UrgencyHigh
	}
	sent := 0
	for _, sub := range subs {
		resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, &webpush.Options{
			Subscriber:      p.keys.Subject,
			VAPIDPublicKey:  p.keys.Public,
			VAPIDPrivateKey: p.keys.Private,
			TTL:             24 * 60 * 60,
			Urgency:         urgency,
			Topic:           topic(pl.Tag),
		})
		if err != nil {
			p.log.Warn("푸시를 보내지 못했습니다", "err", err)
			_ = p.store.PushResult(ctx, sub.Endpoint, false)
			continue
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound:
			p.log.Info("만료된 푸시 구독을 지웁니다", "status", resp.StatusCode)
			_ = p.store.Unsubscribe(ctx, sub.Endpoint)
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			sent++
			_ = p.store.PushResult(ctx, sub.Endpoint, true)
		default:
			p.log.Warn("푸시 서비스가 거절했습니다", "status", resp.StatusCode)
			_ = p.store.PushResult(ctx, sub.Endpoint, false)
		}
	}
	return sent, nil
}

// A push topic collapses undelivered messages with the same topic on the push
// service; it must be URL-safe base64 and at most 32 characters.
func topic(tag string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(tag) && len(out) < 32; i++ {
		c := tag[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}
