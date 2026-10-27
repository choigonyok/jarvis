// Package api is events-svc's HTTP surface. Same bearer scheme as the rest
// of the stack: everything but /health needs the token.
//
//	GET  /health                       the contract (gocommon/health): each consumer's delivery, the held box
//	POST /events                       a service reports something
//	PUT  /subscriptions/{consumer}     a consumer registers {url, types, fromStart}
//	GET  /subscriptions                who is subscribed and how far along
//	GET  /events?after=&limit=         the log, for looking back
//	GET  /held                         events set aside after failing on their own
//	POST /held/{consumer}/{id}/retry   send a held event again now
//	DELETE /held/{consumer}/{id}       give up on a held event
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/events-svc/internal/store"
	"github.com/choigonyok/jarvis/gocommon/health"
)

type Server struct {
	store *store.Store
	wake  func()
	sync  func()
	token string
	// guestToken is the guest's agent's: it posts notifications for the
	// guest (a card waiting on them) and nothing else.
	guestToken string
	// loops are this process's own (the outbox drain); each consumer's
	// delivery is read from its subscription.
	loops *health.Loops
	log   *slog.Logger
}

func New(s *store.Store, wake, sync func(), token, guestToken string, loops *health.Loops, log *slog.Logger) *Server {
	return &Server{store: s, wake: wake, sync: sync, token: token, guestToken: guestToken, loops: loops, log: log}
}

type guestKey struct{}

// asGuest is whether the request came with the guest's token.
func asGuest(r *http.Request) bool {
	v, _ := r.Context().Value(guestKey{}).(bool)
	return v
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /events", s.auth(true, s.append))
	mux.HandleFunc("PUT /subscriptions/{consumer}", s.auth(false, s.subscribe))
	mux.HandleFunc("DELETE /subscriptions/{consumer}", s.auth(false, s.unsubscribe))
	mux.HandleFunc("GET /subscriptions", s.auth(false, s.subscriptions))
	mux.HandleFunc("GET /events", s.auth(false, s.list))
	mux.HandleFunc("GET /held", s.auth(false, s.held))
	mux.HandleFunc("POST /held/{consumer}/{id}/retry", s.auth(false, s.retryHeld))
	mux.HandleFunc("DELETE /held/{consumer}/{id}", s.auth(false, s.dropHeld))
	return mux
}

// stuckAfter is how long a consumer may go without getting past an event
// before the status tab calls its delivery stopped.
const stuckAfter = 15 * time.Minute

// health answers in the stack's contract: a loop per consumer - since when
// it has not got past an event, and why - and the held box. The names of
// what is held are event types and consumers, nothing from inside them.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	rep := health.Report{Loops: s.loops.List()}
	ctx := r.Context()
	subs, err := s.store.Subscriptions(ctx)
	if err != nil {
		health.Write(w, http.StatusServiceUnavailable, rep)
		return
	}
	waiting, err := s.store.Waiting(ctx)
	if err != nil {
		health.Write(w, http.StatusServiceUnavailable, rep)
		return
	}
	now := time.Now()
	for _, sub := range subs {
		l := health.Loop{Name: "deliver:" + sub.Consumer, Label: sub.Consumer + " 에게 전달",
			StaleSec: int(stuckAfter.Seconds()), StartedAt: now, LastError: sub.LastError, Failing: sub.Failures}
		// It got through until the failures began, or until the oldest
		// event it has not got past came in - one that sat there while
		// nothing failed is a worker that is not running.
		through := now
		if sub.FailingSince != nil {
			through = *sub.FailingSince
		}
		if at, ok := waiting[sub.Consumer]; ok {
			l.Behind = &at
			if at.Before(through) {
				through = at
			}
		}
		l.LastOK = &through
		rep.Loops = append(rep.Loops, l)
	}
	held, err := s.store.Held(ctx, 200)
	if err != nil {
		health.Write(w, http.StatusServiceUnavailable, rep)
		return
	}
	if len(held) > 0 {
		box := &health.Held{Count: len(held), Where: "소비자를 고쳐 다시 띄우면 저절로 다시 보냅니다. 버리려면 events-svc DELETE /held/{소비자}/{id}"}
		for i, h := range held {
			if box.Oldest == nil || h.HeldAt.Before(*box.Oldest) {
				at := h.HeldAt
				box.Oldest = &at
			}
			if i < health.ShownItems {
				box.Items = append(box.Items, health.Item{What: fmt.Sprintf("%s · %s #%d", h.Consumer, h.Event.Type, h.Event.ID),
					Why: h.LastError, At: h.HeldAt})
			}
		}
		rep.Held = box
	}
	health.Write(w, http.StatusOK, rep)
}

func (s *Server) held(w http.ResponseWriter, r *http.Request) {
	held, err := s.store.Held(r.Context(), 200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "격리함을 읽지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"held": held})
}

func (s *Server) retryHeld(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 는 숫자입니다."})
		return
	}
	ok, err := s.store.RetryHeld(r.Context(), r.PathValue("consumer"), id)
	switch {
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "다시 보내지 못했습니다."})
	case !ok:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "격리함에 그런 이벤트가 없습니다."})
	default:
		s.wake()
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *Server) dropHeld(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 는 숫자입니다."})
		return
	}
	ok, err := s.store.DropHeld(r.Context(), r.PathValue("consumer"), id)
	switch {
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "버리지 못했습니다."})
	case !ok:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "격리함에 그런 이벤트가 없습니다."})
	default:
		s.log.Info("격리함의 이벤트를 버렸습니다", "consumer", r.PathValue("consumer"), "id", id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// auth takes the services' token everywhere and the guest's only where
// guests is set (posting an event - see append for what it may post).
func (s *Server) auth(guests bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		switch {
		case s.token == "" || same(got, "Bearer "+s.token):
		case guests && s.guestToken != "" && same(got, "Bearer "+s.guestToken):
			r = r.WithContext(context.WithValue(r.Context(), guestKey{}, true))
		default:
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "인증이 필요합니다."})
			return
		}
		next(w, r)
	}
}

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// incoming is the event as sent. Besides the event's own fields it accepts
// the notification shape services sent before there was an event log
// (kind, tier, title...): that becomes an event of type kind, carrying its
// wording as notify. Senders move to the event shape one at a time.
type incoming struct {
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject"`
	Data       json.RawMessage `json:"data"`
	Notify     *store.Notify   `json:"notify"`
	Key        string          `json:"key"`
	OccurredAt *time.Time      `json:"occurredAt"`

	Kind     string `json:"kind"`
	Tier     string `json:"tier"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
	Audience string `json:"audience"`
}

func (in incoming) event() (store.Event, string) {
	e := store.Event{Source: in.Source, Type: in.Type, Subject: in.Subject, Data: in.Data, Notify: in.Notify, Key: in.Key}
	if in.OccurredAt != nil {
		e.OccurredAt = *in.OccurredAt
	}
	if e.Type == "" && in.Kind != "" {
		e.Type = in.Kind
		if e.Notify == nil && in.Title != "" {
			e.Notify = &store.Notify{Tier: in.Tier, Level: in.Level, Title: in.Title, Body: in.Body, URL: in.URL, Audience: in.Audience}
		}
	}
	switch {
	case e.Source == "" || e.Type == "":
		return e, "source 와 type 이 필요합니다"
	case len(e.Type) > 80 || len(e.Source) > 40 || len(e.Subject) > 200 || len(e.Key) > 200:
		return e, "source, type, subject, key 가 너무 깁니다"
	case len(e.Data) > 0 && !json.Valid(e.Data):
		return e, "data 는 JSON 이어야 합니다"
	case e.Notify != nil && (e.Notify.Title == "" || !validTier(e.Notify.Tier)):
		return e, "notify 에는 title 과 tier(now, digest, log)가 필요합니다"
	}
	return e, ""
}

// Parse reads a POST /events body - or the same payload left in the
// outbox - into an event, or says what is wrong with it.
func Parse(raw []byte) (store.Event, error) {
	var in incoming
	if err := json.Unmarshal(raw, &in); err != nil {
		return store.Event{}, errors.New("JSON 을 읽지 못했습니다")
	}
	e, problem := in.event()
	if problem != "" {
		return e, errors.New(problem)
	}
	return e, nil
}

func validTier(t string) bool { return t == "now" || t == "digest" || t == "log" }

func (s *Server) append(w http.ResponseWriter, r *http.Request) {
	var in incoming
	if !readJSON(w, r, &in) {
		return
	}
	e, problem := in.event()
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if asGuest(r) {
		// The guest's agent tells the guest something, and that is all an
		// event from it can do: a notification, to the guest's devices.
		if e.Notify == nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "손님은 알림만 보낼 수 있습니다."})
			return
		}
		e.Notify.Audience = "guest"
	}
	saved, dup, err := s.store.Append(r.Context(), e)
	switch {
	case err != nil:
		s.log.Error("이벤트를 저장하지 못했습니다", "err", err, "type", e.Type)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "이벤트를 저장하지 못했습니다."})
	case dup:
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": true})
	default:
		s.wake()
		writeJSON(w, http.StatusCreated, saved)
	}
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL       string   `json:"url"`
		Types     []string `json:"types"`
		FromStart bool     `json:"fromStart"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	consumer := r.PathValue("consumer")
	if consumer == "" || !strings.HasPrefix(body.URL, "http") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "consumer 와 http 주소가 필요합니다."})
		return
	}
	if len(body.Types) == 0 {
		body.Types = []string{"*"}
	}
	sub, err := s.store.Subscribe(r.Context(), consumer, body.URL, body.Types, body.FromStart)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "구독을 저장하지 못했습니다."})
		return
	}
	s.sync()
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	ok, err := s.store.Unsubscribe(r.Context(), r.PathValue("consumer"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "구독을 지우지 못했습니다."})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 구독이 없습니다."})
		return
	}
	s.sync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) subscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.store.Subscriptions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "구독을 읽지 못했습니다."})
		return
	}
	latest, _ := s.store.Latest(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"latest": latest, "subscriptions": subs})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	events, err := s.store.After(r.Context(), after, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "이벤트를 읽지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, v)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON 을 읽지 못했습니다."})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
