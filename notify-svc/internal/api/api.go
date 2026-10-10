// Package api is notify-svc's HTTP surface. Same bearer scheme as the rest of
// the stack: everything but /health needs the token.
//
//	POST   /events              a service reports something (notify.Event)
//	GET    /notifications       the inbox, newest first, with the unread count
//	POST   /notifications/read  {ids:[...]} or {} for all
//	GET    /settings, PUT /settings
//	GET    /vapid               the public key a browser subscribes with
//	POST   /subscriptions       a browser's PushSubscription
//	DELETE /subscriptions       {endpoint}
//	POST   /test                a push to every device, to see it arrive
//	POST   /presence            a console on screen, or leaving it
//	GET    /stream              pushes as they go out, for an open console
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/notify-svc/internal/live"
	"github.com/choigonyok/jarvis/notify-svc/internal/notify"
	"github.com/choigonyok/jarvis/notify-svc/internal/push"
	"github.com/choigonyok/jarvis/notify-svc/internal/store"
)

type Server struct {
	svc   *notify.Service
	store *store.Store
	push  *push.Pusher
	live  *live.Hub
	token string
	log   *slog.Logger
}

func New(svc *notify.Service, st *store.Store, p *push.Pusher, hub *live.Hub, token string, log *slog.Logger) *Server {
	return &Server{svc: svc, store: st, push: p, live: hub, token: token, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "push": s.push.Ready()})
	})
	mux.HandleFunc("POST /events", s.auth(s.event))
	mux.HandleFunc("POST /consume", s.auth(s.consume))
	mux.HandleFunc("GET /notifications", s.auth(s.list))
	mux.HandleFunc("POST /notifications/read", s.auth(s.read))
	mux.HandleFunc("GET /settings", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.svc.Settings(r.Context()))
	}))
	mux.HandleFunc("PUT /settings", s.auth(s.saveSettings))
	mux.HandleFunc("GET /vapid", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"publicKey": s.push.PublicKey(), "ready": s.push.Ready()})
	}))
	mux.HandleFunc("POST /subscriptions", s.auth(s.subscribe))
	mux.HandleFunc("DELETE /subscriptions", s.auth(s.unsubscribe))
	mux.HandleFunc("POST /test", s.auth(s.test))
	mux.HandleFunc("POST /presence", s.auth(s.presence))
	mux.HandleFunc("GET /stream", s.auth(s.stream))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "인증이 필요합니다."})
			return
		}
		next(w, r)
	}
}

func (s *Server) event(w http.ResponseWriter, r *http.Request) {
	var e notify.Event
	if !readJSON(w, r, &e) {
		return
	}
	n, dup, err := s.svc.Accept(r.Context(), e)
	switch {
	case errors.Is(err, notify.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		s.log.Error("알림을 처리하지 못했습니다", "err", err, "kind", e.Kind)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "알림을 처리하지 못했습니다."})
	case dup:
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": true})
	default:
		writeJSON(w, http.StatusCreated, n)
	}
}

// consumed is an event as events-svc delivers it.
type consumed struct {
	ID     int64           `json:"id"`
	Source string          `json:"source"`
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
	Key    string          `json:"key"`
	Notify *struct {
		Tier, Level, Title, Body, URL string
	} `json:"notify"`
}

// consume takes events from events-svc. An event with wording becomes a
// notification by its tier; one without is not for a person to read and is
// taken without a word. A suggestion raised about something already waiting
// in tonight's digest takes that line's place.
func (s *Server) consume(w http.ResponseWriter, r *http.Request) {
	var e consumed
	if !readJSON(w, r, &e) {
		return
	}
	if e.Type == "suggestion.raised" {
		var d struct {
			Supersedes []string `json:"supersedes"`
		}
		_ = json.Unmarshal(e.Data, &d)
		for _, key := range d.Supersedes {
			if _, err := s.store.Supersede(r.Context(), key); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "알림을 정리하지 못했습니다."})
				return
			}
		}
	}
	if e.Notify == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ignored": true})
		return
	}
	key := e.Key
	if key == "" {
		key = fmt.Sprintf("event:%d", e.ID)
	}
	_, _, err := s.svc.Accept(r.Context(), notify.Event{
		Source: e.Source, Kind: e.Type, Tier: e.Notify.Tier, Level: e.Notify.Level,
		Title: e.Notify.Title, Body: e.Notify.Body, URL: e.Notify.URL, Key: key,
	})
	switch {
	case errors.Is(err, notify.ErrInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	case err != nil:
		s.log.Error("알림을 처리하지 못했습니다", "err", err, "type", e.Type)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "알림을 처리하지 못했습니다."})
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items, unread, err := s.store.List(r.Context(), limit)
	if err != nil {
		s.log.Error("알림함을 읽지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "알림함을 읽지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "unread": unread})
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.store.MarkRead(r.Context(), body.IDs); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "읽음으로 바꾸지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var in notify.Settings
	if !readJSON(w, r, &in) {
		return
	}
	saved, err := s.svc.SaveSettings(r.Context(), in)
	switch {
	case errors.Is(err, notify.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "설정을 저장하지 못했습니다."})
	default:
		writeJSON(w, http.StatusOK, saved)
	}
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Endpoint == "" || body.Keys.P256dh == "" || body.Keys.Auth == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint 와 keys 가 필요합니다."})
		return
	}
	ua := r.Header.Get("X-Client-User-Agent")
	if ua == "" {
		ua = r.UserAgent()
	}
	if len(ua) > 300 {
		ua = ua[:300]
	}
	if err := s.store.Subscribe(r.Context(), store.Subscription{
		Endpoint: body.Endpoint, P256dh: body.Keys.P256dh, Auth: body.Keys.Auth, UserAgent: ua,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "구독을 저장하지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	_ = s.store.Unsubscribe(r.Context(), body.Endpoint)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// test pushes to every device at once, quiet hours or not. Optional
// {title, body, url}: where a tap on it goes, to try that too.
func (s *Server) test(w http.ResponseWriter, r *http.Request) {
	in := push.Payload{Title: "알림이 잘 와요", Body: "jarvis 에서 보낸 시험 알림이에요.", URL: "/"}
	if r.ContentLength > 0 && !readJSON(w, r, &in) {
		return
	}
	if !strings.HasPrefix(in.URL, "/") || strings.HasPrefix(in.URL, "//") {
		in.URL = "/"
	}
	in.Tag = "test"
	n, err := s.push.Send(r.Context(), in)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "보내지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": n})
}

// presence is a console saying it is on screen ({endpoint, visible: true},
// every few seconds) or leaving ({visible: false}). See internal/live.
func (s *Server) presence(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint string `json:"endpoint"`
		Visible  bool   `json:"visible"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	s.live.Seen(in.Endpoint, in.Visible)
	w.WriteHeader(http.StatusNoContent)
}

// stream sends an open console each push as it goes out (server-sent
// events), so the page can show it in place of the phone's banner.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // open for as long as the page is
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": open\n\n")
	_ = rc.Flush()
	msgs, stop := s.live.Subscribe()
	defer stop()
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			// Keeps proxies from closing a quiet stream.
			if _, err := io.WriteString(w, ": beat\n\n"); err != nil {
				return
			}
		case m := <-msgs:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", m); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
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
