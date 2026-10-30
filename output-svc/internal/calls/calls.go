// Package calls is the hand: an approved write mind decided on, carried out
// against an adapter's fixed API, once. Nothing else in the stack writes to the
// calendar, the book, the accounts or the albums on mind's say-so, and only
// what is listed here can be written at all.
package calls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/record"
)

// KindCall is a write mind sent; KindResult what came of it.
const (
	KindCall   = "adapter.call"
	KindResult = "execute.result"
)

// Service is an adapter a write can go to.
type Service struct {
	URL string
	// Auth is how this service is told who is calling.
	Auth func(r *http.Request)
}

type route struct {
	method string
	path   *regexp.Regexp
}

// routes is the fixed API: what may be written, by service. A write that is
// not here is refused, whoever sent it (입구는 관대, 출구는 보수).
var routes = map[string][]route{
	"calendar": {{http.MethodPut, regexp.MustCompile(`^/events$`)}, {http.MethodPatch, regexp.MustCompile(`^/events/[^/?]+$`)}, {http.MethodDelete, regexp.MustCompile(`^/events/[^/?]+$`)}},
	"assets":   {{http.MethodPost, regexp.MustCompile(`^/orders$`)}, {http.MethodPost, regexp.MustCompile(`^/orders/rebalance$`)}},
	"immich":   {{http.MethodPost, regexp.MustCompile(`^/api/albums$`)}, {http.MethodDelete, regexp.MustCompile(`^/api/albums/[^/?]+$`)}},
}

// Allowed is whether a write is on the list.
func Allowed(service, method, path string) bool {
	for _, r := range routes[service] {
		if r.method == method && r.path.MatchString(path) {
			return true
		}
	}
	return false
}

// Hand carries out calls.
type Hand struct {
	Services map[string]Service
	Pool     *pgxpool.Pool
	Records  *record.Writer
	HTTP     *http.Client
	Log      *slog.Logger
	Now      func() time.Time
}

type call struct {
	Service  string          `json:"service"`
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Body     json.RawMessage `json:"body"`
	Deadline time.Time       `json:"deadline"`
	// Side is passed on: whose side of the shared calendar a person's change is from.
	Side string `json:"side"`
}

// Take carries out one call. The ledger row goes in before the call does: a
// call seen again is answered from it and never made twice, and one that was
// under way when the process died is answered as not known, because a broker
// order is not something to try again to find out.
func (h *Hand) Take(ctx context.Context, m queue.Message) error {
	var c call
	if err := json.Unmarshal(m.Payload, &c); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrRefused, err)
	}
	key := m.SourceKey
	now := h.now()
	switch {
	case !c.Deadline.IsZero() && now.After(c.Deadline):
		h.answer(key, http.StatusGone, map[string]string{"error": "기한이 지나 하지 않았습니다"})
		return nil
	case !Allowed(c.Service, c.Method, pathOf(c.Path)):
		h.Log.Error("정해지지 않은 쓰기를 거절합니다", "service", c.Service, "method", c.Method, "path", pathOf(c.Path))
		h.answer(key, http.StatusForbidden, map[string]string{"error": "정해진 API 가 아닙니다"})
		return nil
	}
	first, err := h.claim(ctx, key, c)
	if err != nil {
		return err
	}
	if !first {
		return h.again(ctx, key)
	}
	status, body := h.call(ctx, c)
	if _, err := h.Pool.Exec(ctx, `update output.calls set status = $2, body = $3, finished_at = now() where key = $1`, key, status, string(body)); err != nil {
		h.Log.Warn("실행 결과를 장부에 남기지 못했습니다", "key", key, "err", err)
	}
	h.send(key, status, body)
	return nil
}

func (h *Hand) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func pathOf(p string) string {
	path, _, _ := strings.Cut(p, "?")
	return path
}

func (h *Hand) claim(ctx context.Context, key string, c call) (bool, error) {
	tag, err := h.Pool.Exec(ctx, `insert into output.calls (key, service, method, path) values ($1, $2, $3, $4) on conflict do nothing`,
		key, c.Service, c.Method, pathOf(c.Path))
	return tag.RowsAffected() == 1, err
}

// again answers a call seen before from the ledger.
func (h *Hand) again(ctx context.Context, key string) error {
	var status *int
	var body []byte
	err := h.Pool.QueryRow(ctx, `select status, body from output.calls where key = $1`, key).Scan(&status, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status == nil {
		h.answer(key, http.StatusConflict, map[string]string{"error": "하던 중 끊겨서 결과를 모릅니다. 확인이 필요합니다"})
		return nil
	}
	h.send(key, *status, body)
	return nil
}

// call makes the request. What cannot be reached is an answer too: the one
// waiting decides what it means.
func (h *Hand) call(ctx context.Context, c call) (int, []byte) {
	s, ok := h.Services[c.Service]
	if !ok || s.URL == "" {
		return http.StatusBadGateway, mustJSON(map[string]string{"error": c.Service + " 의 주소가 없습니다"})
	}
	if !c.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.Deadline)
		defer cancel()
	}
	var in io.Reader
	if len(c.Body) > 0 {
		in = bytes.NewReader(c.Body)
	}
	req, err := http.NewRequestWithContext(ctx, c.Method, strings.TrimRight(s.URL, "/")+c.Path, in)
	if err != nil {
		return http.StatusBadGateway, mustJSON(map[string]string{"error": err.Error()})
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Side != "" {
		req.Header.Set("X-Jarvis-Side", c.Side)
	}
	if s.Auth != nil {
		s.Auth(req)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return http.StatusBadGateway, mustJSON(map[string]string{"error": c.Service + " 에 닿지 못했습니다: " + err.Error()})
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return http.StatusBadGateway, mustJSON(map[string]string{"error": err.Error()})
	}
	if !json.Valid(body) {
		body = mustJSON(map[string]string{"text": string(body)})
	}
	return resp.StatusCode, body
}

func (h *Hand) answer(key string, status int, v any) { h.send(key, status, mustJSON(v)) }

func (h *Hand) send(key string, status int, body []byte) {
	h.Records.Write(record.Record{
		Kind: KindResult, Source: "output", SourceKey: "result:" + key, Actor: "output", Channel: "adapter", Subject: key, Tier: record.Raw,
		Payload: map[string]any{"key": key, "status": status, "body": json.RawMessage(body)},
	})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
