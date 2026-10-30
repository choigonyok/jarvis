// Package api is input-svc's HTTP surface. Same bearer scheme as the rest of
// the stack: everything but /health needs the token.
//
//	GET  /health                       the contract (gocommon/health): each consumer's delivery, the held box
//	POST /records                      a record, from a process with no database
//	POST /mind/{path}                  what a screen changes in mind: a record, and what came of it (also PUT, PATCH, DELETE)
//	GET  /last                         when each source last had news, and the latest of each check (the status tab)
//	POST /chat/messages                {text, images?, ref?} what a person said: a record, which the agent takes from the queue
//	GET  /chat/messages/{key}          the words of one, for the agent that took it
//	GET  /turns?thread=&since=&limit=&before=   the conversation: a thread, or a page of it
//	POST /turns                        a turn said: its record, and the turn
//	PUT  /turns/{id}                   a turn with an origin given its newest word
//	GET  /workout                      the training log, whole
//	POST /workout/changes              what changed in it: its record, and the sessions
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
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/seal"
	chat "github.com/choigonyok/jarvis/input-svc/internal/chat/store"
	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
	"github.com/choigonyok/jarvis/input-svc/internal/store"
	workout "github.com/choigonyok/jarvis/input-svc/internal/workout/store"
)

// Views are the read models kept here beside the records.
type Views struct {
	Chat    *chat.Store
	Workout *workout.Store
	// Seal closes a turn's words in its record.
	Seal *seal.Sealer
	// TZ is the wall clock a turn is stamped in.
	TZ string
}

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
	views Views
}

// New is the surface on s.
func New(s *store.Store, token, guestToken string, views Views, loops *health.Loops, log *slog.Logger) *Server {
	return &Server{store: s, token: token, guestToken: guestToken, views: views, loops: loops, log: log, now: time.Now}
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
	mux.HandleFunc("GET /turns", s.auth(true, s.listTurns))
	mux.HandleFunc("POST /turns", s.auth(true, s.appendTurn))
	// A job's card given its newest word (see turn.Origin).
	mux.HandleFunc("PUT /turns/{id}", s.auth(true, s.updateTurn))
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		mux.HandleFunc(method+" /mind/{path...}", s.auth(true, s.command))
	}
	mux.HandleFunc("GET /last", s.auth(false, s.last))
	mux.HandleFunc("POST /chat/messages", s.auth(true, s.postMessage))
	mux.HandleFunc("GET /chat/messages/{key}", s.auth(true, s.getMessage))
	mux.HandleFunc("GET /workout", s.auth(true, s.readWorkout))
	mux.HandleFunc("POST /workout/changes", s.auth(true, s.changeWorkout))
	mux.HandleFunc("GET /held", s.auth(false, s.held))
	mux.HandleFunc("POST /held/{consumer}/{id}/retry", s.auth(false, s.retryHeld))
	mux.HandleFunc("DELETE /held/{consumer}/{id}", s.auth(false, s.dropHeld))
	return mux
}

// stuckAfter is how long a consumer may go without getting past a message
// before the status tab calls its delivery stopped.
const stuckAfter = 15 * time.Minute

// health answers in the stack's contract: a loop per consumer - since when it
// has not got past a message, and why - and the held box. The names of what
// is held are kinds and consumers, nothing from inside them.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	rep := health.Report{Loops: s.loops.List()}
	ctx := r.Context()
	pool, now := s.store.Pool(), s.now()
	var heldList []queue.HeldItem
	for _, t := range queue.All {
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
	e, err := envelope.Parse(raw)
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

func (s *Server) last(w http.ResponseWriter, r *http.Request) {
	seen, list, err := s.store.Last(r.Context())
	if err != nil {
		s.log.Error("마지막 기록을 읽지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "기록을 읽지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": seen, "checks": list})
}

func (s *Server) held(w http.ResponseWriter, r *http.Request) {
	out := []queue.HeldItem{}
	for _, t := range queue.All {
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

// anyTopic does something to a consumer's held message, on the topic that has
// it: a consumer name belongs to one.
func anyTopic(do func(t queue.Topic) (bool, error)) (bool, error) {
	for _, t := range queue.All {
		ok, err := do(t)
		if ok || err != nil {
			return ok, err
		}
	}
	return false, nil
}

func (s *Server) retryHeld(w http.ResponseWriter, r *http.Request) {
	s.onHeld(w, r, func(ctx context.Context, consumer string, id int64) (bool, error) {
		return anyTopic(func(t queue.Topic) (bool, error) { return queue.Retry(ctx, s.store.Pool(), t, consumer, id) })
	}, http.StatusAccepted)
}

func (s *Server) dropHeld(w http.ResponseWriter, r *http.Request) {
	s.onHeld(w, r, func(ctx context.Context, consumer string, id int64) (bool, error) {
		ok, err := anyTopic(func(t queue.Topic) (bool, error) { return queue.Drop(ctx, s.store.Pool(), t, consumer, id) })
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
