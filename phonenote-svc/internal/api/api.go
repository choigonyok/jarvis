// Package api is phonenote-svc's HTTP surface.
//
//	POST /notes        a collector's batch: {device, problem, lastKey, notes:[...]}
//	                   (its own token - the collector runs on a Mac, not in the stack)
//	GET  /notes        the newest notifications and what was made of them
//	GET  /occurrences  what was worth keeping, as mind reads occurrences
//	GET  /collectors   each collector as last heard
//	GET  /health
package api

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
	"github.com/choigonyok/jarvis/phonenote-svc/internal/read"
	"github.com/choigonyok/jarvis/phonenote-svc/internal/store"
)

type Server struct {
	Store *store.Store
	// Token is the stack's; CollectorToken only lets a collector hand notes in.
	Token          string
	CollectorToken string
	// Wake tells the reader there is something new.
	Wake func()
	Log  *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /notes", s.intake)
	mux.HandleFunc("GET /notes", s.auth(s.recent))
	mux.HandleFunc("GET /occurrences", s.auth(s.occurrences))
	mux.HandleFunc("GET /collectors", s.auth(s.collectors))
	return mux
}

func bearer(r *http.Request, token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" && !bearer(r, s.Token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "인증이 필요합니다."})
			return
		}
		next(w, r)
	}
}

// Work apps, mail and the apps read elsewhere never come in, whatever a
// collector sends: company data is not connected, and KakaoTalk and
// Messages have their own readers.
var refused = []string{
	"com.tinyspeck.", "com.slack", "com.atlassian.", "com.microsoft.", "com.google.gmail", "com.apple.mail",
	"com.apple.mobilemail", "com.readdle.", "com.kakao.", "com.iwilab.kakaotalk", "com.apple.mobilesms",
	"com.apple.ichat", "com.agilebits.", "com.1password.", "com.apple.passwords",
}

// Refused says whether an app's notifications are never taken in.
func Refused(app string) bool {
	a := strings.ToLower(app)
	for _, p := range refused {
		if strings.HasPrefix(a, p) {
			return true
		}
	}
	return false
}

func (s *Server) intake(w http.ResponseWriter, r *http.Request) {
	if !bearer(r, s.CollectorToken) && !bearer(r, s.Token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "인증이 필요합니다."})
		return
	}
	var body struct {
		Device  string    `json:"device"`
		Problem string    `json:"problem"`
		LastKey string    `json:"lastKey"`
		Notes   []note.In `json:"notes"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil || json.Unmarshal(raw, &body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON 을 읽지 못했습니다."})
		return
	}
	device := clip(strings.TrimSpace(body.Device), 60)
	if device == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device 가 필요합니다."})
		return
	}
	if len(body.Notes) > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "한 번에 500개까지입니다."})
		return
	}
	in := make([]note.In, 0, len(body.Notes))
	for _, n := range body.Notes {
		if n.Key == "" || n.App == "" || Refused(n.App) {
			continue
		}
		if n.At.IsZero() || n.At.After(time.Now().Add(time.Hour)) {
			n.At = time.Now()
		}
		n.Key, n.App = clip(n.Key, 120), clip(n.App, 120)
		n.Title, n.Subtitle, n.Body = clip(n.Title, 300), clip(n.Subtitle, 300), clip(n.Body, 2000)
		in = append(in, n)
	}
	added, err := s.Store.Add(r.Context(), device, in)
	if err != nil {
		s.Log.Error("알림을 저장하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "알림을 저장하지 못했습니다."})
		return
	}
	if err := s.Store.Seen(r.Context(), device, clip(body.Problem, 300), clip(body.LastKey, 120)); err != nil {
		s.Log.Warn("수집기 상태를 남기지 못했습니다", "err", err)
	}
	if added > 0 && s.Wake != nil {
		s.Wake()
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": added})
}

func (s *Server) recent(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	notes, err := s.Store.Recent(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "알림을 읽지 못했습니다."})
		return
	}
	for i := range notes {
		notes[i].AppName = read.Name(notes[i].App)
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}

func (s *Server) collectors(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Store.Collectors(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "수집기 상태를 읽지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collectors": cs})
}

// Occurrence is mind's shape (mind-svc model.Occurrence).
type Occurrence struct {
	ID     string    `json:"id"`
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
	Title  string    `json:"title"`
	Place  string    `json:"place,omitempty"`
	Things []string  `json:"things,omitempty"`
	Class  string    `json:"class,omitempty"`
	Amount int64     `json:"amount,omitempty"`
	Ref    string    `json:"ref"`
	Quote  string    `json:"quote,omitempty"`
}

// OccurrenceOf is a kept notification as mind takes it, or false for one
// that is mind's already some other way: a payment comes from the 가계부
// (with the items this put there), not twice.
func OccurrenceOf(n note.Note) (Occurrence, bool) {
	r := n.Reading
	if r == nil || !r.Meaningful {
		return Occurrence{}, false
	}
	o := Occurrence{ID: "phone:" + strconv.FormatInt(n.ID, 10), At: n.At, Title: r.Summary, Amount: r.AmountKrw, Ref: n.App, Quote: clip(n.Text(), 80)}
	for _, it := range r.Items {
		o.Things = append(o.Things, it.Name)
	}
	switch r.Kind {
	case note.Purchase:
		return o, false
	case note.Deposit:
		o.Kind, o.Class = "deposit", "입금"
		if r.Salary {
			o.Class = "급여"
		}
		if r.Counterparty != "" {
			o.Title = r.Counterparty
		}
	case note.Withdrawal:
		o.Kind, o.Class = "transfer", "출금"
	case note.DeliveryOrder:
		o.Kind, o.Class, o.Place = "delivery", r.Stage, r.Merchant
	case note.Shipping:
		o.Kind, o.Class, o.Place = "parcel", r.Stage, r.Merchant
	case note.Reservation:
		o.Kind, o.Place = "reservation", r.Merchant
	case note.Bill:
		o.Kind = "bill"
	default:
		o.Kind, o.Class = "notice", read.Name(n.App)
	}
	if o.Title == "" {
		o.Title = clip(n.Title, 60)
	}
	return o, true
}

func (s *Server) occurrences(w http.ResponseWriter, r *http.Request) {
	since, err := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
	if err != nil {
		since = time.Now().Add(-14 * 24 * time.Hour)
	}
	notes, err := s.Store.Meaningful(r.Context(), since, 500)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "알림을 읽지 못했습니다."})
		return
	}
	out := []Occurrence{}
	for _, n := range notes {
		if o, ok := OccurrenceOf(n); ok {
			out = append(out, o)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"occurrences": out})
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
