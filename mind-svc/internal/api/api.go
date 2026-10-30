// Package api is mind-svc's HTTP surface.
//
//	GET  /health                      the stack's contract (gocommon/health): the facts held for a person
//	GET  /status                      reading and remembering in a few numbers (token)
//	GET  /                            the console: simulator, world model, intents
//	GET  /api/snapshot                everything the console shows
//	GET  /now                         the situation right now, by rule: what the conversation is shown each turn
//	                                  (the guest's token: the guest's own, with the guest's taste)
//	GET  /messages/search?q=&room=&since=&limit=   messages whose text holds every word of q, newest first (token)
//	GET  /messages/context?ref=&before=&after=     a message with the ones around it in its room (token)
//	POST /api/simulate                {room, roomName, sender, mine, text, at?}
//	POST /api/read                    {room?} interpret now instead of waiting for quiet
//	POST /api/settle                  hand settling changes to the skills now
//	POST /api/tick                    {name: "morning" | "night"}
//	POST /api/clock                   {advance: "25h"} move a memory-mode clock (tests the timers)
//	POST /api/intents/{id}/{approve|reject|undo}   reject takes {reason}
//	POST /api/policy/rebuild          recount approval stats from the intents
//	POST /api/patterns/rebuild        recount repetitions from the plans and purchases
//	POST /api/ledger/rebuild          make the result ledger and the facts table again
//	POST /api/facts/adopt             copy into the facts table what memory's graph holds and it does not
//	GET  /api/facts/index?offset=&limit=  what memory's graph is made from (memory-svc POST /rebuild reads it)
//	GET  /api/slots/{name}?saved=     whether a slot's value saved then still holds
//	GET  /api/commands/{key}          what came of a command sent through the stream (commands.go)
//	GET  /api/weather/places          where the plans of the next two weeks are (context-svc reads their forecast)
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
//	POST /api/facts/{id}/release      send a held fact again
//	POST /api/facts/{id}/drop         a person deciding a held fact is not to be kept
//	GET  /api/reminders?all=1         the reminders waiting (all: and those said, cancelled or expired in three days)
//	GET  /api/reminders?days=30       the same, with those of the last days instead (the 작업 tab's history, a year at most)
//	POST /api/reminders               {text, body?, at?, place?, move?, after?, until?, url?} one to say at its moment
//	PATCH /api/reminders/{id}         {text?, body?, at?, place?, move?, after?, until?, clear?} change it; a said one waits again
//	DELETE /api/reminders/{id}        cancel it
//	GET  /api/checklists?all=1&ids=   the checklists open (all: and those closed in two weeks; ids: these, open or not)
//	POST /api/checklists              {title, items, note?, topic?, source?} a new list - with an open list's topic, that one
//	GET  /api/checklists/{id}         one list
//	PATCH /api/checklists/{id}        {title?, note?, add?, remove?, check?, uncheck?, rename?, by?, reopen?}
//	POST /api/checklists/{id}/items/{item}  {done, by?} a tap on the card
//	DELETE /api/checklists/{id}       close it
//	POST /api/checklists/{id}/show    {lead?} ask the agent to put it in the conversation now (checklist.shown)
//
// /api and the console need a token unless open: in the stack the port is
// bound to the host's loopback, and a laptop run has nothing to protect. The
// services call with their token as a bearer; a person signs in to the
// console with a token of its own (MIND_CONSOLE_TOKEN), never the services'.
package api

import (
	"context"
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

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	jobs "github.com/choigonyok/jarvis/mind-svc/internal/jobs/api"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/slot"
	spending "github.com/choigonyok/jarvis/mind-svc/internal/spending/api"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

//go:embed ui/index.html
var ui embed.FS

type Server struct {
	book  *spending.Server
	jobs  *jobs.Server
	m     *mind.Mind
	token string
	// consoleToken is what a person signs in to the console with, once, for
	// a cookie. It opens the console's routes and not the services' own
	// (/status).
	consoleToken string
	open         bool
	clock        *clock.Clock // nil unless the clock can be moved (memory mode)
	log          *slog.Logger
	// guestToken is the guest's agent's: it opens the guest's own reminders,
	// checklists and situation, and nothing else of mind.
	guestToken string
	// stream takes what would change mind's state; nil (a laptop) does it at
	// once.
	stream Commands
	writes *http.ServeMux
}

// Commands is the stream as far as this door needs it.
type Commands interface {
	Command(ctx context.Context, key, kind, actor, method, path string, body json.RawMessage) error
}

// SetStream sends what changes mind through the stream.
func (s *Server) SetStream(c Commands) { s.stream = c }

// SetBook puts the household book on the API.
func (s *Server) SetBook(b *spending.Server) { s.book = b }

// SetJobs puts the jobs on the API, each person's their own.
func (s *Server) SetJobs(j *jobs.Server) {
	j.SetWho(whoOf)
	s.jobs = j
}

// SetGuestToken lets the guest's agent keep the guest's own reminders and
// checklists here, and read the guest's situation (person).
func (s *Server) SetGuestToken(t string) { s.guestToken = t }

func New(m *mind.Mind, token, consoleToken string, open bool, c *clock.Clock, log *slog.Logger) *Server {
	return &Server{m: m, token: token, consoleToken: consoleToken, open: open, clock: c, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// What changes mind's state is reached two ways, and neither changes it
	// here: the agent leaves a record, which the lane brings to writes; the
	// console and the screen come to this door, which leaves the same record.
	s.writes = http.NewServeMux()
	owner := func(next http.HandlerFunc) http.HandlerFunc { return s.auth(false, next) }
	write := func(pattern string, guard func(http.HandlerFunc) http.HandlerFunc, h http.HandlerFunc) {
		s.writes.HandleFunc(pattern, h)
		mux.HandleFunc(pattern, guard(s.written(h)))
	}
	mux.HandleFunc("GET /health", s.health)
	if s.book != nil {
		mux.HandleFunc("GET "+spending.Prefix+"/health", s.book.Health())
		s.book.Mount(
			func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, owner(h)) },
			func(pattern string, h http.HandlerFunc) { write(pattern, owner, h) })
	}
	if s.jobs != nil {
		mux.HandleFunc("GET "+jobs.Prefix+"/health", s.jobs.Health())
		s.jobs.Mount(
			func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, s.person(h)) },
			func(pattern string, h http.HandlerFunc) { write(pattern, s.person, h) })
	}
	mux.HandleFunc("POST /api/facts/adopt", s.auth(true, func(w http.ResponseWriter, r *http.Request) {
		n, err := s.m.AdoptFacts(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "adopted": n})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"adopted": n})
	}))
	mux.HandleFunc("GET /api/facts/index", s.auth(true, func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows, err := s.m.Opts.Store.IndexRows(r.Context(), max(offset, 0), max(1, min(limit, 500)))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		out := make([]json.RawMessage, 0, len(rows))
		for _, f := range rows {
			out = append(out, f.Body)
		}
		writeJSON(w, http.StatusOK, map[string]any{"rows": out})
	}))
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
	mux.HandleFunc("GET /now", s.person(s.now))
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
	write("POST /api/intents/{id}/{action}", owner, s.decide)
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
	write("POST /api/notes/{id}", owner, s.decideNote)
	mux.HandleFunc("GET /api/notes/{id}/versions", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		vs, err := s.m.Opts.Store.NoteVersions(r.Context(), r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": vs})
	}))
	write("POST /api/links", owner, s.decideLink)
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
	mux.HandleFunc("POST /api/ledger/rebuild", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		rows, facts, err := s.m.RebuildLedger(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"outcomes": rows, "facts": facts})
	}))
	mux.HandleFunc("GET /api/slots/{name}", s.auth(false, s.slot))
	mux.HandleFunc("GET /api/weather/places", s.auth(true, func(w http.ResponseWriter, r *http.Request) {
		places, err := s.m.WeatherPlaces(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"places": places})
	}))
	mux.HandleFunc("GET /api/entities/{id}/changes", s.auth(false, s.changes))
	mux.HandleFunc("POST /api/replay", s.auth(false, s.replay))
	mux.HandleFunc("GET /api/recall", s.auth(false, s.recall))
	mux.HandleFunc("POST /api/facts/flush", s.auth(false, s.flush))
	mux.HandleFunc("POST /api/facts/{id}/release", s.auth(false, s.releaseFact))
	mux.HandleFunc("POST /api/facts/{id}/drop", s.auth(false, s.dropFact))
	mux.HandleFunc("GET /api/reminders", s.person(s.listReminders))
	write("POST /api/reminders", s.person, s.setReminder)
	write("PATCH /api/reminders/{id}", s.person, s.changeReminder)
	write("DELETE /api/reminders/{id}", s.person, s.cancelReminder)
	mux.HandleFunc("GET /api/checklists", s.person(s.listChecklists))
	write("POST /api/checklists", s.person, s.makeChecklist)
	mux.HandleFunc("GET /api/checklists/{id}", s.person(s.getChecklist))
	write("PATCH /api/checklists/{id}", s.person, s.changeChecklist)
	write("POST /api/checklists/{id}/items/{item}", s.person, s.checkItem)
	write("DELETE /api/checklists/{id}", s.person, s.closeChecklist)
	write("POST /api/checklists/{id}/show", s.person, s.showChecklist)
	mux.HandleFunc("GET /api/commands/{key}", s.person(s.commandResult))
	return mux
}

// now is the situation of whoever asks, with their taste: the operator's, or
// - with the guest's token - the guest's, which holds nothing of the
// operator's.
func (s *Server) now(w http.ResponseWriter, r *http.Request) {
	guest := whoOf(r) == moments.Guest
	situation := s.m.Situation
	if guest {
		situation = s.m.GuestSituation
	}
	now, err := situation(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Absent when it could not be read, "" when there is none: the agent
	// keeps the list in its instructions and changes them only on an answer.
	var prefs *string
	if guest {
		if text, ok := s.m.GuestPreferences(); ok {
			prefs = &text
		}
	} else if text, err := s.m.PreferencesRead(r.Context()); err == nil {
		prefs = &text
	} else {
		s.log.Warn("취향을 읽지 못했습니다", "err", err)
	}
	writeJSON(w, http.StatusOK, struct {
		world.Situation
		// Prefs is what is known of the asker's taste, with grounds.
		Prefs *string `json:"prefs,omitempty"`
	}{now, prefs})
}

type caller struct{}

// person guards a route on someone's own: their reminders and checklists,
// and their situation. What auth lets through is the operator's, and the
// guest's token the guest's - another's is not found.
func (s *Server) person(next http.HandlerFunc) http.HandlerFunc {
	owner := s.auth(false, next)
	return func(w http.ResponseWriter, r *http.Request) {
		if s.guestToken != "" && same(r.Header.Get("Authorization"), "Bearer "+s.guestToken) {
			next(w, r.WithContext(context.WithValue(r.Context(), caller{}, moments.Guest)))
			return
		}
		owner(w, r)
	}
}

// whoOf is whose the request is: moments.Owner or moments.Guest (the same
// words as checklist's).
func whoOf(r *http.Request) string {
	if w, ok := r.Context().Value(caller{}).(string); ok {
		return w
	}
	return moments.Owner
}

// auth guards a route. The services' own routes (service:
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

// slot says whether a value kept elsewhere - sealed, so never here - still
// holds, for the one who is about to use it.
func (s *Server) slot(w http.ResponseWriter, r *http.Request) {
	d, ok := slot.Get(r.PathValue("name"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 슬롯이 없습니다"})
		return
	}
	saved, err := time.Parse(time.RFC3339, r.URL.Query().Get("saved"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "saved 는 RFC3339 시각이어야 합니다"})
		return
	}
	got, err := s.m.SlotReading(r.Context(), d, saved)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, got)
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

// health answers in the stack's contract (gocommon/health): the facts held
// after failing on their own. How reading and remembering are going is the
// status tab's own rows (mind.reading, mind.memory, from /status).
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	held, err := s.m.Opts.Store.HeldFacts(r.Context())
	if err != nil {
		health.Write(w, http.StatusServiceUnavailable, health.Report{})
		return
	}
	rep := health.Report{}
	if len(held) > 0 {
		box := &health.Held{Count: len(held), Where: "mind 콘솔(scripts/mini console)의 기억할 사실에서 다시 보내거나 버리세요. 기억이 다시 받기 시작하면 저절로 한 번 더 보냅니다."}
		for i := len(held) - 1; i >= 0; i-- {
			f := held[i]
			if box.Oldest == nil || f.CreatedAt.Before(*box.Oldest) {
				at := f.CreatedAt
				box.Oldest = &at
			}
			if len(box.Items) < health.ShownItems {
				// The relation and who it is about, not what was said.
				box.Items = append(box.Items, health.Item{What: f.Relation + " · " + factEnd(f.Subject), Why: f.Error, At: f.CreatedAt})
			}
		}
		rep.Held = box
	}
	health.Write(w, http.StatusOK, rep)
}

func factEnd(p model.Party) string {
	if p.Self {
		return "나"
	}
	return p.Name
}

func (s *Server) releaseFact(w http.ResponseWriter, r *http.Request) {
	if s.m.Remember == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "장기 기억이 연결되어 있지 않습니다."})
		return
	}
	n, err := s.m.Remember.Release(r.Context(), r.PathValue("id"))
	switch {
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	case n == 0:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "보류한 사실이 아닙니다."})
	default:
		writeJSON(w, http.StatusOK, map[string]int{"released": n})
	}
}

func (s *Server) dropFact(w http.ResponseWriter, r *http.Request) {
	if s.m.Remember == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "장기 기억이 연결되어 있지 않습니다."})
		return
	}
	switch err := s.m.Remember.Drop(r.Context(), r.PathValue("id")); {
	case errors.Is(err, model.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 사실이 없습니다."})
	case errors.Is(err, model.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "보류한 사실만 버릴 수 있습니다."})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
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
	case "covered":
		// The agent did not show the card: its conversation asks the same
		// already, on the assistant's own card (by).
		var body struct {
			By string `json:"by"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
		in, err = s.m.Gate.Covered(r.Context(), id, strings.TrimSpace(body.By))
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "approve, reject, undo, covered 중 하나입니다."})
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
