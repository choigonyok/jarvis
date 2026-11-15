// Package push delivers one payload to every subscribed device over Web Push
// (VAPID) - or to the one device that asked for it (internal/timers). It is
// the only way out of output-svc: no app to install, and a tap opens the
// console on the screen the notification is about.
package push

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/choigonyok/jarvis/output-svc/internal/live"
	"github.com/choigonyok/jarvis/output-svc/internal/store"
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
	// TTL is how long the push service keeps trying a device it cannot reach
	// (a phone with no signal). Zero is a day; a push that is only worth its
	// minute says less, and is dropped rather than arriving late.
	TTL time.Duration `json:"-"`
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
	live  *live.Hub
	log   *slog.Logger
}

// New returns a pusher. live may be nil: then every device gets the push.
func New(keys Keys, s *store.Store, hub *live.Hub, log *slog.Logger) *Pusher {
	return &Pusher{keys: keys, store: s, live: hub, log: log}
}

func (p *Pusher) Ready() bool { return p.keys.Public != "" && p.keys.Private != "" }

func (p *Pusher) PublicKey() string { return p.keys.Public }

// Send pushes to every device of one person (store.Owner, store.Guest) and
// reports how many took it. A device the push service says no longer exists
// is forgotten; one that fails otherwise keeps its place until it has failed
// many times in a row.
func (p *Pusher) Send(ctx context.Context, audience string, pl Payload) (int, error) {
	body, err := json.Marshal(pl)
	if err != nil {
		return 0, err
	}
	// Every console of theirs that is open hears it at once, and shows it in
	// the page.
	if p.live != nil {
		p.live.Broadcast(audience, body)
	}
	if !p.Ready() {
		return 0, nil
	}
	subs, err := p.store.Subscriptions(ctx, audience)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, sub := range subs {
		// On screen: the page shows it (above); a phone banner would only
		// cover that.
		if p.live != nil && p.live.Present(sub.Endpoint) {
			sent++
			continue
		}
		if p.deliver(ctx, sub, body, pl) {
			sent++
		}
	}
	return sent, nil
}

// Owns says whether endpoint is one of audience's devices.
func (p *Pusher) Owns(ctx context.Context, audience, endpoint string) (bool, error) {
	_, ok, err := p.store.Subscription(ctx, audience, endpoint)
	return ok, err
}

// SendTo pushes to one device of one person - the one that asked for it -
// and says whether it took it. False, without a word, when the device is not
// theirs (any more: a browser signed in as the other person since is that
// person's) or when there are no keys to push with.
//
// Unlike Send it is not shown in their open consoles, and a device that has
// the console on screen still gets it: what a device asks for itself is for
// that device, and on screen is not the same as looked at - a phone on the
// bench between sets keeps the workout screen awake, and only a push buzzes
// it on mute.
func (p *Pusher) SendTo(ctx context.Context, audience, endpoint string, pl Payload) (bool, error) {
	if !p.Ready() {
		return false, nil
	}
	sub, ok, err := p.store.Subscription(ctx, audience, endpoint)
	if err != nil || !ok {
		return false, err
	}
	body, err := json.Marshal(pl)
	if err != nil {
		return false, err
	}
	return p.deliver(ctx, sub, body, pl), nil
}

// deliver hands one push to one device's push service and keeps what came of
// it: a device the push service says no longer exists is forgotten, a refusal
// counts toward dropping it. True when the push service took it.
func (p *Pusher) deliver(ctx context.Context, sub store.Subscription, body []byte, pl Payload) bool {
	urgency := webpush.UrgencyNormal
	if pl.Urgent {
		urgency = webpush.UrgencyHigh
	}
	ttl := 24 * 60 * 60
	if pl.TTL > 0 {
		ttl = int(pl.TTL / time.Second)
	}
	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		Subscriber:      p.keys.Subject,
		VAPIDPublicKey:  p.keys.Public,
		VAPIDPrivateKey: p.keys.Private,
		TTL:             ttl,
		Urgency:         urgency,
		Topic:           topic(pl.Tag),
	})
	if err != nil {
		p.log.Warn("푸시를 보내지 못했습니다", "err", err)
		_ = p.store.PushResult(ctx, sub.Endpoint, false)
		return false
	}
	// The push service says why it refused in the body (Apple: {"reason": ...}).
	reason, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound:
		p.log.Info("만료된 푸시 구독을 지웁니다", "status", resp.StatusCode)
		_ = p.store.Unsubscribe(ctx, sub.Endpoint)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		_ = p.store.PushResult(ctx, sub.Endpoint, true)
		return true
	default:
		p.log.Warn("푸시 서비스가 거절했습니다", "status", resp.StatusCode, "host", host(sub.Endpoint), "reason", strings.TrimSpace(string(reason)))
		_ = p.store.PushResult(ctx, sub.Endpoint, false)
	}
	return false
}

func host(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Host
	}
	return ""
}

// A push topic collapses undelivered messages with the same topic on the push
// service. It must be at most 32 characters of URL-safe base64 - and Apple
// decodes it as such: "suggestionnew" (13 characters) is not base64 of
// anything and was refused as BadWebPushTopic, while "test" passed. A hash of
// the tag, encoded, is always exactly 32 valid characters.
func topic(tag string) string {
	if tag == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tag))
	return base64.RawURLEncoding.EncodeToString(sum[:24])
}
