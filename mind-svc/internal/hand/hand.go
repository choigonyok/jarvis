// Package hand is how mind's approved writes reach the outside: not by mind.
// mind decides and keeps the ledger; output-svc is the hand that does it. A
// write leaves as an effect, output-svc calls the adapter's fixed API once,
// and what came of it returns as a record (execute.result).
//
// The actuators (package act) keep saying what to do and reading what came
// back: a write still looks to them like an HTTP request, and the Transport
// carries it. Reading is theirs to do as before: a look at the calendar just
// before moving something on it changes nothing.
package hand

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
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

const (
	// KindCall is a write on its way to an adapter.
	KindCall = "adapter.call"
	// KindResult is what came of one.
	KindResult = "execute.result"
	// Wait is how long a write is given. Past it the hand refuses it, so a
	// write that was reported as failed cannot happen after all.
	Wait = 90 * time.Second
	poll = 40 * time.Millisecond
)

// Call is a write as output-svc gets it.
type Call struct {
	Service  string          `json:"service"`
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Body     json.RawMessage `json:"body,omitempty"`
	Deadline time.Time       `json:"deadline"`
	// Side is whose side of a shared thing the write is from, as the adapter
	// is told it: partner, or empty for the operator's.
	Side string `json:"side,omitempty"`
}

// Transport carries an actuator's writes as effects. Reads go by Reads.
type Transport struct {
	// Publish sends the effect.
	Publish func(ctx context.Context, m queue.Message) error
	Store   store.Store
	Reads   http.RoundTripper
	Wait    time.Duration
	Now     func() time.Time
}

// RoundTrip sends a write and waits for what came of it.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet {
		reads := t.Reads
		if reads == nil {
			reads = http.DefaultTransport
		}
		return reads.RoundTrip(req)
	}
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
	}
	wait := t.Wait
	if wait <= 0 {
		wait = Wait
	}
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	key := "call-" + hex.EncodeToString(raw)
	call := Call{Service: req.URL.Hostname(), Method: req.Method, Path: req.URL.RequestURI(), Deadline: now().Add(wait),
		Side: req.Header.Get("X-Jarvis-Side")}
	if len(bytes.TrimSpace(body)) > 0 {
		call.Body = bytes.TrimSpace(body)
	}
	payload, err := json.Marshal(call)
	if err != nil {
		return nil, err
	}
	ctx := req.Context()
	if err := t.Publish(ctx, queue.Message{Kind: KindCall, Source: "mind", SourceKey: key, Actor: "mind", Subject: call.Service, Payload: payload}); err != nil {
		return nil, fmt.Errorf("쓰기를 보내지 못했습니다: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		c, err := t.Store.Command(ctx, key)
		if err == nil {
			return &http.Response{
				StatusCode: c.Status, Status: http.StatusText(c.Status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
				Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(c.Body)),
				ContentLength: int64(len(c.Body)), Request: req,
			}, nil
		}
		if !errors.Is(err, model.ErrNotFound) && ctx.Err() == nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("쓰기의 결과가 오지 않았습니다(%s %s): %w", call.Service, call.Path, ctx.Err())
		case <-time.After(poll):
		}
	}
}

// Result is an execute.result: what came of a write, kept for whoever waits.
func Result(ctx context.Context, st store.Store, m queue.Message) error {
	var r struct {
		Key    string          `json:"key"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(m.Payload, &r); err != nil || r.Key == "" {
		return fmt.Errorf("%w: 실행 결과를 읽지 못했습니다", queue.ErrRefused)
	}
	if len(r.Body) == 0 || !json.Valid(r.Body) {
		r.Body = json.RawMessage("{}")
	}
	return st.PutCommand(ctx, model.Command{Key: r.Key, Kind: KindResult, Who: "owner", Status: r.Status, Body: r.Body})
}
