// Package api is input-svc's HTTP surface. Same bearer scheme as the rest of
// the stack: everything but /health needs the token.
//
//	GET  /health                       the contract (gocommon/health): each consumer's delivery, the held box
//	POST /records                      a record, or the event shape, from a process with no database
//	GET  /held                         messages set aside after failing on their own
//	POST /held/{consumer}/{id}/retry   send a held message again now
//	DELETE /held/{consumer}/{id}       give up on a held message
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

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
	"github.com/choigonyok/jarvis/input-svc/internal/store"
)

// Server is the HTTP surface.
type Server struct {
	store *store.Store
	token string
	// guestToken is the guest's agent's: it can tell the guest something,
	// and that is all it can do here.
	guestToken string
	// loops are this process's own; each consumer's delivery is read from
	// the tables.
	loops *health.Loops
	log   *slog.Logger
	now   func() time.Time
}

// New is the surface on s.
func New(s *store.Store, token, guestToken string, loops *health.Loops, log *slog.Logger) *Server {
	return &Server{store: s, token: token, guestToken: guestToken, loops: loops, log: log, now: time.Now}
}

type guestKey struct{}

func asGuest(r *http.Request) bool {
	v, _ := r.Context().Value(guestKey{}).(bool)
	return v
}

// Handler is the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /records", s.auth(true, s.post))
	mux.HandleFunc("GET /held", s.auth(false, s.held))
	mux.HandleFunc("POST /held/{consumer}/{id}/retry", s.auth(false, s.retryHeld))
	mux.HandleFunc("DELETE /held/{consumer}/{id}", s.auth(false, s.dropHeld))
	return mux
}

// stuckAfter is how long a consumer may go without getting past a message
// before the status tab calls its delivery stopped.
const stuckAfter = 15 * time.Minute

// topics is every stream, the guest's second.
var topics = []queue.Topic{queue.Records, queue.Guest}

// topicOf is the stream a consumer reads: the guest's end in -guest.
func topicOf(consumer string) queue.Topic {
	if strings.HasSuffix(consumer, GuestSuffix) {
		return queue.Guest
	}
	return queue.Records
}

// GuestSuffix ends the name of every consumer of the guest's stream.
const GuestSuffix = "-guest"

// health answers in the stack's contract: a loop per consumer - since when it
// has not got past a message, and why - and the held box. The names of what
// is held are kinds and consumers, nothing from inside them.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	rep := health.Report{Loops: s.loops.List()}
	ctx := r.Context()
	pool, now := s.store.Pool(), s.now()
	var heldList []queue.HeldItem
	for _, t := range topics {
		states, err := queue.States(ctx, pool, t)
		if err != nil {
			health.Write(w, http.StatusServiceUnavailable, rep)
			return
		}
		for _, c := range states {
			l := health.Loop{Name: "deliver:" + c.Name, Label: c.Name + " 에게 전달",
				StaleSec: int(stuckAfter.Seconds()), StartedAt: now, LastError: c.LastError, Failing: c.Failures}
			// It got through until the failures began, or until the oldest
			// message it has not got past came in - one that sat there while
			// nothing failed is a worker that is not running.
			through := now
			if c.FailingSince != nil {
				through = *c.FailingSince
			}
			if c.Oldest != nil {
				l.Behind = c.Oldest
				if c.Oldest.Before(through) {
					through = *c.Oldest
				}
			}
			l.LastOK = &through
			rep.Loops = append(rep.Loops, l)
		}
		held, err := queue.Held(ctx, pool, t, 200)
		if err != nil {
			health.Write(w, http.StatusServiceUnavailable, rep)
			return
		}
		heldList = append(heldList, held...)
	}
	rejected, turnedAway, err := s.store.Rejections(ctx, health.ShownItems)
	if err != nil {
		health.Write(w, http.StatusServiceUnavailable, rep)
		return
	}
	if len(heldList) > 0 || turnedAway > 0 {
		box := &health.Held{Count: len(heldList) + turnedAway,
			Where: "받는 쪽을 고쳐 다시 띄우면 저절로 다시 보냅니다. 버리려면 input-svc DELETE /held/{소비자}/{id}. 봉투가 잘못된 것은 input.rejected 에 있습니다"}
		for i, h := range heldList {
			if box.Oldest == nil || h.HeldAt.Before(*box.Oldest) {
				at := h.HeldAt
				box.Oldest = &at
			}
			if i < health.ShownItems {
				box.Items = append(box.Items, health.Item{What: fmt.Sprintf("%s · %s #%d", h.Consumer, h.Kind, h.ID), Why: h.LastError, At: h.HeldAt})
			}
		}
		for _, x := range rejected {
			if box.Oldest == nil || x.At.Before(*box.Oldest) {
				at := x.At
				box.Oldest = &at
			}
			if len(box.Items) < health.ShownItems {
				box.Items = append(box.Items, health.Item{What: fmt.Sprintf("봉투 거절 #%d", x.ID), Why: x.Why, At: x.At})
			}
		}
		rep.Held = box
	}
	health.Write(w, http.StatusOK, rep)
}

// post takes one record from a process with no way to the outbox. The guest's
// token can only tell the guest something: a notification, for the guest.
func (s *Server) post(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽지 못했습니다."})
		return
	}
	now := s.now()
	e, err := envelope.Parse(raw, now.UnixNano(), now)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if asGuest(r) {
		e, err = envelope.ForGuest(e)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
	}
	id, dup, err := s.store.Put(r.Context(), e)
	switch {
	case err != nil:
		s.log.Error("기록을 저장하지 못했습니다", "err", err, "kind", e.Kind)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "기록을 저장하지 못했습니다."})
	case dup:
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": true})
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
	}
}

func (s *Server) held(w http.ResponseWriter, r *http.Request) {
	out := []queue.HeldItem{}
	for _, t := range topics {
		held, err := queue.Held(r.Context(), s.store.Pool(), t, 200)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "격리함을 읽지 못했습니다."})
			return
		}
		out = append(out, held...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"held": out})
}

func (s *Server) onHeld(w http.ResponseWriter, r *http.Request, do func(ctx context.Context, consumer string, id int64) (bool, error), done int) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id 는 숫자입니다."})
		return
	}
	ok, err := do(r.Context(), r.PathValue("consumer"), id)
	switch {
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "처리하지 못했습니다."})
	case !ok:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "격리함에 그런 메시지가 없습니다."})
	default:
		w.WriteHeader(done)
	}
}

func (s *Server) retryHeld(w http.ResponseWriter, r *http.Request) {
	s.onHeld(w, r, func(ctx context.Context, consumer string, id int64) (bool, error) {
		return queue.Retry(ctx, s.store.Pool(), topicOf(consumer), consumer, id)
	}, http.StatusAccepted)
}

func (s *Server) dropHeld(w http.ResponseWriter, r *http.Request) {
	s.onHeld(w, r, func(ctx context.Context, consumer string, id int64) (bool, error) {
		ok, err := queue.Drop(ctx, s.store.Pool(), topicOf(consumer), consumer, id)
		if ok {
			s.log.Info("격리함의 메시지를 버렸습니다", "consumer", consumer, "id", id)
		}
		return ok, err
	}, http.StatusNoContent)
}

// auth takes the services' token everywhere and the guest's only where guests
// is set.
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		return
	}
}
