// Package api is mind-svc's HTTP surface.
//
//	GET  /health
//	GET  /status                      reading and remembering in a few numbers (token)
//	POST /consume                     events-svc delivers an observation (token)
//	GET  /                            the console: simulator, world model, intents
//	GET  /api/snapshot                everything the console shows
//	GET  /now                         the situation right now, by rule: what the conversation is shown each turn
//	GET  /messages/search?q=&room=&since=&limit=   messages whose text holds every word of q, newest first (token)
//	GET  /messages/context?ref=&before=&after=     a message with the ones around it in its room (token)
//	POST /api/simulate                {room, roomName, sender, mine, text, at?}
//	POST /api/read                    {room?} interpret now instead of waiting for quiet
//	POST /api/settle                  hand settling changes to the skills now
//	POST /api/tick                    {name: "morning" | "night"}
//	POST /api/clock                   {advance: "25h"} move a memory-mode clock (tests the timers)
//	POST /api/intents                 {skill, kind, topic, title, body, reason, confidence, payload} an intent from outside
//	POST /api/intents/{id}/{approve|reject|undo}   reject takes {reason}
//	POST /api/policy/rebuild          recount approval stats from the intents
//	POST /api/patterns/rebuild        recount repetitions from the plans and purchases
//	GET  /api/notes                   the notes about the operator, every status
//	POST /api/notes/{id}              {status, text, reason} the operator on a note
//	GET  /api/notes/{id}/versions     a note's versions, with why each
//	POST /api/links                   {from, to, rel, state} the operator on a link
//	POST /api/replies/rebuild         re-index reply situations from the messages
//	GET  /api/things                  what was bought in half a year, by class, with restock cycles and sizes
//	GET  /api/fixed                   monthly charges (subscriptions, bills) and which changed
//	GET  /api/places                  the operator's own map: places reviewed, best first
//	GET  /api/meals?days=30           meals as the card saw them: delivered, at home, which meal
//	GET  /api/entities/{id}/changes   an entity's history, with evidence
//	POST /api/replay                  {after, limit} the log through a fresh loop, in memory
//	GET  /api/recall?q=&person=       what is known: world model + long-term memory
//	POST /api/facts/flush             send the ledger's pending facts to memory now
//	GET  /api/reminders?all=1         the reminders waiting (all: and those said, cancelled or expired in three days)
//	POST /api/reminders               {text, body?, at?, place?, move?, after?, until?, url?} one to say at its moment
//	PATCH /api/reminders/{id}         {text?, body?, at?, place?, move?, after?, until?, clear?} change it; a said one waits again
//	DELETE /api/reminders/{id}        cancel it
//
// /api and the console need a token unless open: in the stack the port is
// bound to the host's loopback, and a laptop run has nothing to protect. The
// services call with their token as a bearer; a person signs in to the
// console with a token of its own (MIND_CONSOLE_TOKEN), never the services'.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

//go:embed ui/index.html
var ui embed.FS

type Server struct {
	m     *mind.Mind
	token string
	// consoleToken is what a person signs in to the console with, once, for
	// a cookie. It opens the console's routes and not the services' own
	// (/consume, /status).
	consoleToken string
	open         bool
	clock        *clock.Clock // nil unless the clock can be moved (memory mode)
	log          *slog.Logger
}

func New(m *mind.Mind, token, consoleToken string, open bool, c *clock.Clock, log *slog.Logger) *Server {
	return &Server{m: m, token: token, consoleToken: consoleToken, open: open, clock: c, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "mode": s.m.Opts.Mode, "llm": s.m.Opts.LLM.Ready()})
	})
	mux.HandleFunc("POST /consume", s.auth(true, s.consume))
	mux.HandleFunc("GET /status", s.auth(true, func(w http.ResponseWriter, r *http.Request) {
		st, err := s.m.Status(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, st)
	}))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if s.login(w, r) {
			return
		}
		if !s.open && !s.bearer(r) && !s.signedIn(r) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("콘솔은 토큰이 필요합니다. /?token=<MIND_CONSOLE_TOKEN> 으로 한 번 여세요(scripts/mini console).\n"))
			return
		}
		page, _ := ui.ReadFile("ui/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /api/snapshot", s.auth(false, s.snapshot))
	mux.HandleFunc("GET /now", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		now, err := s.m.Situation(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// Absent when it could not be read, "" when there is none: the agent
		// keeps the list in its instructions and changes them only on an answer.
		var prefs *string
		if text, err := s.m.PreferencesRead(r.Context()); err == nil {
			prefs = &text
		} else {
			s.log.Warn("취향을 읽지 못했습니다", "err", err)
		}
		writeJSON(w, http.StatusOK, struct {
			world.Situation
			// Prefs is what is known of the operator's taste, with grounds.
			Prefs *string `json:"prefs,omitempty"`
		}{now, prefs})
	}))
	// The messages themselves, for the conversation: what mind read but did
	// not keep as a fact. The services' token only - these hand out text.
	mux.HandleFunc("GET /messages/search", s.auth(true, s.searchMessages))
	mux.HandleFunc("GET /messages/context", s.auth(true, s.messageContext))
	mux.HandleFunc("POST /api/simulate", s.auth(false, s.simulate))
	mux.HandleFunc("POST /api/read", s.auth(false, s.read))
	mux.HandleFunc("POST /api/settle", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		s.m.Attn.Settle(r.Context())
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /api/tick", s.auth(false, s.tick))
	mux.HandleFunc("POST /api/clock", s.auth(false, s.moveClock))
	mux.HandleFunc("POST /api/intents", s.auth(false, s.submit))
	mux.HandleFunc("POST /api/intents/{id}/{action}", s.auth(false, s.decide))
	mux.HandleFunc("POST /api/policy/rebuild", s.auth(false, s.rebuildPolicy))
	// The notes alone, for the console's 생활 screen: the snapshot carries
	// message text, which that screen has no business with.
	// Rules still being tried in silence (candidates) only with ?all=1: the
	// screen asks the operator about what has come true, not about guesses.
	mux.HandleFunc("GET /api/notes", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		notes, err := s.m.Notes(r.Context(), r.URL.Query().Get("all") == "1")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
	}))
	mux.HandleFunc("POST /api/notes/{id}", s.auth(false, s.decideNote))
	mux.HandleFunc("GET /api/notes/{id}/versions", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		vs, err := s.m.Opts.Store.NoteVersions(r.Context(), r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": vs})
	}))
	mux.HandleFunc("POST /api/links", s.auth(false, s.decideLink))
	mux.HandleFunc("GET /api/fixed", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		fixed, err := s.m.FixedCosts(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"fixed": fixed})
	}))
	mux.HandleFunc("GET /api/places", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		places, err := s.m.Places(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"places": places})
	}))
	mux.HandleFunc("GET /api/meals", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days == 0 {
			days = 30
		}
		meals, err := s.m.Meals(r.Context(), max(1, min(days, 365)))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"meals": meals})
	}))
	mux.HandleFunc("GET /api/things", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		things, err := s.m.Things(r.Context(), 0)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"things": things})
	}))
	mux.HandleFunc("POST /api/replies/rebuild", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		n, err := s.m.RebuildReplies(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"situations": n})
	}))
	mux.HandleFunc("POST /api/patterns/rebuild", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		n, err := s.m.RebuildPatterns(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"plans": n})
	}))
	mux.HandleFunc("GET /api/entities/{id}/changes", s.auth(false, s.changes))
	mux.HandleFunc("POST /api/replay", s.auth(false, s.replay))
	mux.HandleFunc("GET /api/recall", s.auth(false, s.recall))
	mux.HandleFunc("POST /api/facts/flush", s.auth(false, s.flush))
	mux.HandleFunc("GET /api/reminders", s.auth(false, s.listReminders))
	mux.HandleFunc("POST /api/reminders", s.auth(false, s.setReminder))
	mux.HandleFunc("PATCH /api/reminders/{id}", s.auth(false, s.changeReminder))
	mux.HandleFunc("DELETE /api/reminders/{id}", s.auth(false, s.cancelReminder))
	return mux
}

// auth guards a route. The services' own routes (service: /consume,
// /status) take the services' token and nothing else. The console's take it
// too, or the console's cookie - and are let through without either only when
// the server is open: a memory-mode run on a laptop, with nothing to protect.
func (s *Server) auth(service bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok := s.bearer(r)
		if !service {
			ok = ok || s.open || s.signedIn(r)
		}
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "인증이 필요합니다."})
			return
		}
		next(w, r)
	}
}

const tokenCookie = "mind_token"

// bearer is whether the request carries the services' token. With no token
// configured nothing passes.
func (s *Server) bearer(r *http.Request) bool {
	return s.token != "" && same(r.Header.Get("Authorization"), "Bearer "+s.token)
}

// signedIn is whether the request carries a live console cookie. A browser
// cannot send a bearer header, so the console signs in once with /?token=….
func (s *Server) signedIn(r *http.Request) bool {
	if s.consoleToken == "" {
		return false
	}
	c, err := r.Cookie(tokenCookie)
	return err == nil && s.validSession(c.Value, time.Now())
}

// sessionTTL is how long the console's cookie lasts.
const sessionTTL = 24 * time.Hour

// session is the cookie value: an expiry and an HMAC of it keyed by the
// console's token. The token itself never sits in the browser, and a stolen
// cookie dies in a day.
func (s *Server) session(exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(s.consoleToken))
	mac.Write([]byte("mind-console:" + e))
	return e + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validSession(v string, now time.Time) bool {
	e, _, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(e, 10, 64)
	if err != nil || now.Unix() > n {
		return false
	}
	return same(v, s.session(time.Unix(n, 0)))
}

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// login trades /?token=… for the cookie and sends the browser back to /, so
// the token does not stay in the address bar or the history.
func (s *Server) login(w http.ResponseWriter, r *http.Request) bool {
	t := r.URL.Query().Get("token")
	if t == "" || s.consoleToken == "" || !same(t, s.consoleToken) {
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: tokenCookie, Value: s.session(time.Now().Add(sessionTTL)), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(w, r, "/", http.StatusSeeOther)
	return true
}

// consume takes one observation from events-svc. A failure is a 500, so
// events-svc tries it again; what can never be handled is skipped.
func (s *Server) consume(w http.ResponseWriter, r *http.Request) {
	var o obs.Observation
	if !readJSON(w, r, &o) {
		return
	}
	if err := s.m.Handle(r.Context(), o); err != nil {
		s.log.Warn("관측을 처리하지 못했습니다", "type", o.Type, "id", o.ID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) recall(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	people := r.URL.Query()["person"]
	if q == "" && len(people) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q 나 person 이 필요합니다."})
		return
	}
	rec, err := s.m.Recall(r.Context(), q, people, 10)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) flush(w http.ResponseWriter, r *http.Request) {
	if s.m.Remember == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "장기 기억이 연결되어 있지 않습니다."})
		return
	}
	n, err := s.m.Remember.Flush(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"sent": n, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": n})
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.m.Snapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) simulate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Room     string `json:"room"`
		RoomName string `json:"roomName"`
		Sender   string `json:"sender"`
		Mine     bool   `json:"mine"`
		Text     string `json:"text"`
		At       string `json:"at"` // optional, RFC 3339 or "2006-01-02T15:04" in local time
	}
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Text) == "" || strings.TrimSpace(in.Room) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "room 과 text 가 필요합니다."})
		return
	}
	var at time.Time
	if in.At != "" {
		var err error
		if at, err = time.Parse(time.RFC3339, in.At); err != nil {
			if at, err = time.ParseInLocation("2006-01-02T15:04", in.At, s.m.Opts.Loc); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at 은 RFC 3339 나 2006-01-02T15:04 입니다."})
				return
			}
		}
	}
	if in.RoomName == "" {
		in.RoomName = in.Room
	}
	msg, err := s.m.Simulate(r.Context(), "sim:"+in.Room, in.RoomName, in.Sender, in.Mine, in.Text, at)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Room string `json:"room"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	var err error
	if in.Room == "" {
		err = s.m.ReadAll(r.Context())
	} else {
		err = s.m.Conv.Read(r.Context(), in.Room)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) tick(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.m.Tick(r.Context(), in.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) moveClock(w http.ResponseWriter, r *http.Request) {
	if s.clock == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "실제 시계로 도는 중이라 시간을 옮길 수 없습니다."})
		return
	}
	var in struct {
		Advance string `json:"advance"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	d, err := time.ParseDuration(in.Advance)
	if err != nil || d <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "advance 는 25h 같은 양의 기간입니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]time.Time{"now": s.clock.Advance(d)})
}

// submit takes an intent from outside the loop - the agent's judging turn
// over an event mind does not read (a portfolio out of band) - and hands it
// to the same gate every skill's intent goes through: one ledger, one
// budget, one way a card reaches the operator.
func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	var in model.Intent
	if !readJSON(w, r, &in) {
		return
	}
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	if in.Skill == "" || in.Kind == "" || in.Title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skill, kind, title 이 필요합니다."})
		return
	}
	in.Skill = "ext:" + strings.TrimPrefix(in.Skill, "ext:")
	in.Decision, in.Status, in.Card, in.Tier, in.Result, in.Undo, in.Error = "", "", false, "", nil, nil, ""
	out, err := s.m.Gate.Submit(r.Context(), in)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// rebuildPolicy recounts the approval stats from the intents they are
// derived from.
func (s *Server) rebuildPolicy(w http.ResponseWriter, r *http.Request) {
	n, err := s.m.Gate.Rebuild(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"intents": n})
}

// decideNote is the operator confirming, rejecting or rewording a note.
func (s *Server) decideNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
		Text   string `json:"text"`
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	n, err := s.m.Self.Decide(r.Context(), r.PathValue("id"), in.Status, in.Text, in.Reason)
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 노트가 없습니다."})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, n)
	}
}

// decideLink is the operator saying a link is so, or is not.
func (s *Server) decideLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Rel   string `json:"rel"`
		State string `json:"state"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	l, err := s.m.DecideLink(r.Context(), in.From, in.To, in.Rel, in.State)
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 링크가 없습니다."})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, l)
	}
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in model.Intent
	var err error
	switch r.PathValue("action") {
	case "approve":
		var body struct {
			Answers map[string]string `json:"answers"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body)
		in, err = s.m.Gate.ApproveWith(r.Context(), id, body.Answers)
	case "reject":
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
		in, err = s.m.Gate.RejectWhy(r.Context(), id, strings.TrimSpace(body.Reason))
	case "undo":
		in, err = s.m.Gate.Undo(r.Context(), id)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "approve, reject, undo 중 하나입니다."})
		return
	}
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 제안이 없습니다."})
	case err != nil:
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, in)
	}
}

func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	cs, err := s.m.Opts.Store.Changes(r.Context(), r.PathValue("id"), 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": cs})
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		After int64 `json:"after"`
		Limit int   `json:"limit"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	if in.Limit <= 0 || in.Limit > 5000 {
		in.Limit = 1000
	}
	rep, err := s.m.Replay(r.Context(), s.m.Opts.LLM, in.After, in.Limit)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
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

// messageScan bounds how many messages one search reads (newest first):
// text is sealed, so every candidate is opened and matched here.
const messageScan = 100000

// messageOut is a message as the conversation's tools read it.
type messageOut struct {
	Ref      string    `json:"ref"`
	Source   string    `json:"source"`
	Room     string    `json:"room"`
	RoomName string    `json:"roomName"`
	Sender   string    `json:"sender"`
	IsMine   bool      `json:"isMine"`
	SentAt   time.Time `json:"sentAt"`
	Text     string    `json:"text"`
}

func outMessage(m model.Message, text string) messageOut {
	return messageOut{Ref: m.Ref, Source: m.Source, Room: m.Room, RoomName: m.RoomName, Sender: m.Sender, IsMine: m.IsMine, SentAt: m.SentAt, Text: text}
}

// searchMessages answers with a short stretch of each match around the
// first word found: enough to pick one, little enough to keep the
// conversation's context small. message_context shows the rest.
func (s *Server) searchMessages(w http.ResponseWriter, r *http.Request) {
	in := r.URL.Query()
	q := store.MessageQuery{Terms: strings.Fields(in.Get("q")), Room: in.Get("room"), Limit: bounded(in.Get("limit"), 10, 1, 20), Scan: messageScan}
	if len(q.Terms) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q 가 필요합니다."})
		return
	}
	if v := strings.TrimSpace(in.Get("since")); v != "" {
		since, err := time.ParseInLocation("2006-01-02", v, s.m.Opts.Loc)
		if err != nil {
			if since, err = time.Parse(time.RFC3339, v); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "since 는 YYYY-MM-DD 입니다."})
				return
			}
		}
		q.Since = since
	}
	ms, err := s.m.Opts.Store.SearchMessages(r.Context(), q)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]messageOut, 0, len(ms))
	for _, m := range ms {
		out = append(out, outMessage(m, snippet(m.Text, q.Terms, 60)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

// messageContext answers with the message and its neighbours, each cut to a
// few hundred characters.
func (s *Server) messageContext(w http.ResponseWriter, r *http.Request) {
	in := r.URL.Query()
	ref := strings.TrimSpace(in.Get("ref"))
	if ref == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ref 가 필요합니다."})
		return
	}
	ms, err := s.m.Opts.Store.MessageContext(r.Context(), ref, bounded(in.Get("before"), 5, 0, 20), bounded(in.Get("after"), 5, 0, 20))
	if errors.Is(err, model.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 메시지가 없습니다(보존 기간이 지났을 수 있습니다)."})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]messageOut, 0, len(ms))
	for _, m := range ms {
		out = append(out, outMessage(m, cut(m.Text, 300)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out, "ref": ref})
}

// bounded reads a count, def when missing or unreadable, kept in [lo, hi].
func bounded(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		n = def
	}
	return min(max(n, lo), hi)
}

// snippet is text around the first of terms it holds, around runes each
// side, on one line.
func snippet(text string, terms []string, around int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	// Lowered rune by rune, so positions in it are positions in runes.
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	at := -1
	for _, t := range terms {
		if i := strings.Index(string(lower), strings.Map(unicode.ToLower, t)); i >= 0 {
			at = utf8.RuneCountInString(string(lower)[:i])
			break
		}
	}
	if at < 0 {
		return cut(string(runes), 2*around)
	}
	from, to := max(0, at-around), min(len(runes), at+around)
	out := string(runes[from:to])
	if from > 0 {
		out = "…" + out
	}
	if to < len(runes) {
		out += "…"
	}
	return out
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
