// Package httpapi exposes the agent to the web client: hydrate once over
// JSON, then follow along over SSE. Every stream frame comes off the one bus,
// so a proposal raised with no conversation behind it reaches the browser the
// same way a chat turn does.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

type Server struct {
	thread        *thread.Store
	proposals     *proposal.Store
	calendar      *calendar.Store
	runner        Sender
	bus           *bus.Bus
	mcp           map[string]http.Handler
	log           *slog.Logger
	allowedOrigin string
	// interceptToken is the shared secret the browser container presents when
	// it stops a payment. Empty means the endpoint refuses everything.
	interceptToken string
	role           string
	// backgroundLogin is the sign-in hosts a background task may reach
	// without asking (see postIntercept).
	backgroundLogin map[string]bool
	// apiToken guards the routes the console drives. Empty leaves them open,
	// which is right when nothing but the compose network can reach this port
	// and wrong the moment a tunnel gives the agent a public hostname.
	apiToken     string
	approvalWait time.Duration
}

// Sender is whatever drives a turn. Keeping it an interface means the
// transport does not care that a CLI subprocess sits behind it.
type Sender interface {
	Send(text string) error
}

type Deps struct {
	// Role is "owner" or "guest": which agent this is. Every response says so
	// (X-Jarvis-Agent), and the gateway refuses one from the wrong agent.
	Role      string
	Thread    *thread.Store
	Proposals *proposal.Store
	Calendar  *calendar.Store
	Runner    Sender
	Bus       *bus.Bus
	// MCP maps a mount path to its server: the permission gate at /mcp, one
	// module server per path beneath it.
	MCP            map[string]http.Handler
	AllowedOrigin  string
	InterceptToken string
	// BackgroundLogin names hosts a background task may sign in through.
	BackgroundLogin []string
	APIToken        string
	ApprovalWait    time.Duration
	Log             *slog.Logger
}

func New(d Deps) *Server {
	return &Server{
		thread:          d.Thread,
		proposals:       d.Proposals,
		calendar:        d.Calendar,
		runner:          d.Runner,
		bus:             d.Bus,
		mcp:             d.MCP,
		log:             d.Log,
		allowedOrigin:   d.AllowedOrigin,
		interceptToken:  d.InterceptToken,
		role:            d.Role,
		backgroundLogin: hostSet(d.BackgroundLogin),
		apiToken:        d.APIToken,
		approvalWait:    d.ApprovalWait,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Everything the console drives. The bearer check is on each of them
	// rather than on the mux, so /healthz stays open for the healthcheck and
	// /intercept and /mcp keep the separate secrets they already have.
	mux.HandleFunc("GET /thread", s.guard(s.getThread))
	mux.HandleFunc("POST /messages", s.guard(s.postMessage))
	mux.HandleFunc("GET /events", s.guard(s.events))

	mux.HandleFunc("GET /proposals", s.guard(s.getProposals))
	mux.HandleFunc("POST /proposals/{id}/decision", s.guard(s.postDecision))

	// The browser container dials this when a page tries to pay.
	mux.HandleFunc("POST /intercept", s.postIntercept)

	mux.HandleFunc("GET /calendar", s.guard(s.getCalendar))
	mux.HandleFunc("POST /calendar", s.guard(s.postCalendar))
	mux.HandleFunc("DELETE /calendar/{id}", s.guard(s.deleteCalendar))

	// Claude Code dials these. The permission gate sits at /mcp; module
	// servers mount beneath it, and the more specific pattern wins.
	for path, handler := range s.mcp {
		mux.Handle(path, handler)
		mux.Handle(strings.TrimSuffix(path, "/")+"/", handler)
	}
	return s.withCORS(s.identify(mux))
}

// guard requires the console's bearer token. It is a plain equality check on
// a value both sides read from the environment: there is exactly one caller,
// and anything more would be a login in front of a login.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiToken == "" {
			next(w, r)
			return
		}
		// Only the console's server-side proxy calls these, and it can set a
		// header - the browser never holds this token.
		// CutPrefix, not TrimPrefix: the scheme is required, so a header that
		// is only the raw token does not quietly count as presenting it.
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(s.apiToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

// identify stamps every response with which agent answered. Two agents share
// a tunnel, and the gateway checks this before passing a response on - a
// connection that lands on the wrong one must not hand one person's
// conversation to the other.
func (s *Server) identify(next http.Handler) http.Handler {
	role := s.role
	if role == "" {
		role = "owner"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jarvis-Agent", role)
		next.ServeHTTP(w, r)
	})
}

// withCORS stays closed unless an origin is configured; the default topology
// puts the Next.js server in front, so the browser never talks here directly.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.allowedOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.allowedOrigin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	turns, thinking := s.thread.Snapshot()
	if turns == nil {
		turns = []*thread.Turn{}
	}
	// Proposals ride along: a client hydrating the thread needs the cards
	// those turns point at, and one round trip is one less way to be stale.
	writeJSON(w, http.StatusOK, map[string]any{
		"turns":     turns,
		"thinking":  thinking,
		"proposals": s.proposals.List(),
	})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "메시지가 비어 있습니다.")
		return
	}
	if err := s.runner.Send(text); err != nil {
		if errors.Is(err, claudecode.ErrBusy) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "요청을 시작하지 못했습니다.")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) getProposals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"proposals": s.proposals.List()})
}

func (s *Server) postDecision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	err := s.proposals.Decide(r.PathValue("id"), proposal.Decision(body.Decision))
	switch {
	case errors.Is(err, proposal.ErrNotFound):
		writeErr(w, http.StatusNotFound, "그런 요청이 없습니다.")
	case errors.Is(err, proposal.ErrAlreadyDecided):
		writeErr(w, http.StatusConflict, "이미 결재된 요청입니다.")
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) getCalendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	writeJSON(w, http.StatusOK, map[string]any{
		"events": s.calendar.Range(q.Get("from"), q.Get("to")),
	})
}

// postCalendar is the operator editing an existing entry by hand, which needs
// no card. A new entry does: the calendar is shared with a partner since it
// moved to uniple, and the operator asked that every addition be approved.
// New entries come in through the calendar tools and their approval card.
func (s *Server) postCalendar(w http.ResponseWriter, r *http.Request) {
	var in calendar.Event
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	if in.ID == "" {
		writeErr(w, http.StatusForbidden, "새 일정은 대화에서 요청해 승인 카드를 거쳐 추가합니다.")
		return
	}
	// An edit keeps whoever created it; only a new entry is stamped as mine.
	if in.ID != "" {
		if existing, ok := s.calendar.Get(in.ID); ok {
			in.Source = existing.Source
		}
	} else {
		in.Source = calendar.SourceHuman
	}

	saved, err := s.calendar.Put(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) deleteCalendar(w http.ResponseWriter, r *http.Request) {
	if _, err := s.calendar.Delete(r.PathValue("id")); err != nil {
		if errors.Is(err, calendar.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "삭제하지 못했습니다.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "스트리밍을 지원하지 않습니다.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// Defeats proxy buffering, which otherwise holds frames until the stream ends.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	events, unsubscribe := s.bus.Subscribe()
	defer unsubscribe()

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func hostSet(hosts []string) map[string]bool {
	out := map[string]bool{}
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out[h] = true
		}
	}
	return out
}
