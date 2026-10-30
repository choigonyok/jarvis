package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
)

// Commands. mind has no write API: what changes its state comes through the
// stream, as a record, and the deterministic lane does it (Command). The one
// who asked reads what came of it here (GET /api/commands/{key}) - a letter
// out, an answer at the window. Keeping the routes' own handlers means a
// reminder is checked, kept and answered as it always was; only the way in
// changed.

// commandWait is how long the door waits for the lane, for those who came to
// it.
const (
	commandWait = 60 * time.Second
	commandPoll = 25 * time.Millisecond
	maxCommand  = 64 << 10
)

// commandIn is what a command's record carries.
type commandIn struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

// recorder is a handler's answer, kept.
type recorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(code int)        { r.code = code }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }

// Command does what a record asks, and keeps what came of it. A record with no
// path is not a command: an answer to a card the agent holds, which HeldCard
// takes. A record seen before is not done twice.
func (s *Server) Command(ctx context.Context, m queue.Message) error {
	var in commandIn
	if err := json.Unmarshal(m.Payload, &in); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrRefused, err)
	}
	if in.Path == "" {
		return nil
	}
	store := s.m.Opts.Store
	if _, err := store.Command(ctx, m.SourceKey); err == nil {
		return nil
	} else if !errors.Is(err, model.ErrNotFound) {
		return err
	}
	who := moments.Owner
	if m.Actor == moments.Guest {
		who = moments.Guest
		ctx = context.WithValue(ctx, caller{}, moments.Guest)
	}
	var body io.Reader = http.NoBody
	if len(in.Body) > 0 && string(in.Body) != "null" {
		body = bytes.NewReader(in.Body)
	}
	req, err := http.NewRequestWithContext(ctx, in.Method, in.Path, body)
	if err != nil {
		return fmt.Errorf("%w: %v", queue.ErrRefused, err)
	}
	rec := &recorder{header: http.Header{}, code: http.StatusOK}
	s.writes.ServeHTTP(rec, req)
	out := rec.body.Bytes()
	if !json.Valid(out) {
		out, _ = json.Marshal(map[string]string{"text": rec.body.String()})
	}
	return store.PutCommand(ctx, model.Command{Key: m.SourceKey, Kind: m.Kind, Who: who, Status: rec.code, Body: out})
}

// commandResult is what came of a command, once it has: 404 until then. A
// person reads only their own.
func (s *Server) commandResult(w http.ResponseWriter, r *http.Request) {
	c, err := s.m.Opts.Store.Command(r.Context(), r.PathValue("key"))
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "아직 처리하지 않았습니다."})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	case c.Who != whoOf(r):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "아직 처리하지 않았습니다."})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": c.Status, "body": c.Body})
	}
}

// written is the door for those who come to it: the console, and the
// screen's word on a note. It changes nothing itself: it leaves the record
// and answers with what the lane made of it. On a laptop, with no stream, the
// handler runs here.
func (s *Server) written(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.stream == nil {
			next(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxCommand))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽지 못했습니다."})
			return
		}
		if !json.Valid(body) {
			body = nil
		}
		raw := make([]byte, 12)
		_, _ = rand.Read(raw)
		key := "cmd-" + hex.EncodeToString(raw)
		actor := "operator"
		if whoOf(r) == moments.Guest {
			actor = moments.Guest
		}
		path := r.URL.RequestURI()
		if err := s.stream.Command(r.Context(), key, record.CommandKind(r.Method, r.URL.Path), actor, r.Method, path, body); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "기록을 남기지 못했습니다: " + err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), commandWait)
		defer cancel()
		for {
			c, err := s.m.Opts.Store.Command(ctx, key)
			if err == nil {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(c.Status)
				_, _ = w.Write(c.Body)
				return
			}
			select {
			case <-ctx.Done():
				writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "아직 처리하지 못했습니다. 기록은 남았고 곧 처리됩니다."})
				return
			case <-time.After(commandPoll):
			}
		}
	}
}
