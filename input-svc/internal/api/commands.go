package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
)

// What a screen changes comes in here, like everything else: the request is
// left as a record, mind's deterministic lane does it, and what came of it is
// read back for the one who asked. With mind down the record stays and is done
// when it comes up.

const (
	commandWait = 30 * time.Second
	commandPoll = 25 * time.Millisecond
	maxCommand  = 256 << 10
)

// command leaves one request for mind and answers with what came of it. The
// path is mind's, the gateway having said which may come this way.
func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCommand))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "본문이 너무 큽니다."})
		return
	}
	if !json.Valid(body) {
		body = nil
	}
	path := "/" + r.PathValue("path")
	kind := record.CommandKind(r.Method, path)
	if kind == "command" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 경로가 없습니다."})
		return
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "기록을 남기지 못했습니다."})
		return
	}
	key := "cmd-" + hex.EncodeToString(raw)
	target := path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	payload, err := json.Marshal(map[string]any{"method": r.Method, "path": target, "body": json.RawMessage(body)})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "기록을 남기지 못했습니다."})
		return
	}
	e := envelope.Envelope{Kind: kind, Source: "web", SourceKey: key, Actor: "operator", Channel: "console", Subject: path,
		OccurredAt: s.now(), Tier: "raw", SchemaVersion: 1, Payload: payload}
	if asGuest(r) {
		if e, err = envelope.ForGuest(e); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
	}
	if _, _, err := s.store.Put(r.Context(), e); err != nil {
		s.log.Error("기록을 남기지 못했습니다", "err", err, "kind", kind)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "기록을 남기지 못했습니다."})
		return
	}
	status, answer, err := s.answer(r.Context(), key)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusAccepted, map[string]string{"error": "아직 처리하지 못했습니다. 기록은 남았고 곧 처리됩니다."})
	case err != nil:
		s.log.Error("명령의 답을 읽지 못했습니다", "err", err, "kind", kind)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "답을 읽지 못했습니다."})
	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		if status != http.StatusNoContent {
			_, _ = w.Write(answer)
		}
	}
}

// answer waits for what came of a command.
func (s *Server) answer(ctx context.Context, key string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandWait)
	defer cancel()
	for {
		var status int
		var body []byte
		err := s.store.Pool().QueryRow(ctx, `select status, body from mind.commands where key = $1`, key).Scan(&status, &body)
		if err == nil {
			return status, body, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, err
		}
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-time.After(commandPoll):
		}
	}
}
