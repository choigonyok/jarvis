// Package watch is what an adapter does with the outside: reads it every so
// often and leaves a record when what it read has changed. mind no longer
// polls anyone: what it knows of the calendar, the phone and the accounts came
// to it through the stream, and it reads the outside only as far as records
// carry it.
//
// A read that says the same as the last says nothing. After a restart the
// first read goes out once more, which a reader takes as it takes any other:
// each is what the source says now, whole, so a missed one is made good by the
// next and none of them can be applied twice to bad effect.
package watch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
)

// Source is one thing an adapter reads.
type Source struct {
	// Kind is the record it goes out as.
	Kind  string
	Label string
	Every time.Duration
	Read  func(ctx context.Context) (any, error)
}

// Watcher reads Sources for one service.
type Watcher struct {
	Service string
	Records *record.Writer
	Loops   *health.Loops
	Log     *slog.Logger
	// Now is the clock; a test sets it.
	Now func() time.Time
}

// Run reads each source until ctx ends.
func (w *Watcher) Run(ctx context.Context, sources ...Source) {
	for _, s := range sources {
		w.Loops.Add("watch:"+s.Kind, s.Label, 5*s.Every)
		go w.loop(ctx, s)
	}
}

func (w *Watcher) loop(ctx context.Context, s Source) {
	last := ""
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		sum, err := w.round(ctx, s, last)
		switch {
		case err != nil && ctx.Err() == nil:
			w.Log.Warn("바깥을 읽지 못했습니다", "kind", s.Kind, "err", err)
			w.Loops.Fail("watch:"+s.Kind, err)
		case err == nil:
			last = sum
			w.Loops.OK("watch:" + s.Kind)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// round reads once and leaves a record if it differs from last, and says what
// it read as a sum.
func (w *Watcher) round(ctx context.Context, s Source, last string) (string, error) {
	v, err := s.Read(ctx)
	if err != nil {
		return last, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return last, err
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(raw))
	if sum == last {
		return last, nil
	}
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	at := now()
	w.Records.Write(record.Record{
		Kind: s.Kind, Source: w.Service, SourceKey: fmt.Sprintf("%s:%d", s.Kind, at.UnixNano()), Actor: w.Service,
		Subject: s.Kind, OccurredAt: at, Tier: record.Raw, Payload: json.RawMessage(raw),
	})
	return sum, nil
}

// Local reads the service's own read API, as the one who used to poll it did:
// what goes out is what that route answers. With keys, only those of the
// answer: the rest (the time it was asked, a live price) would make every read
// a change.
func Local(h http.Handler, token, path string, keys ...string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := &recorder{header: http.Header{}, code: http.StatusOK}
		h.ServeHTTP(rec, req)
		if rec.code >= 300 {
			return nil, fmt.Errorf("%s: %d", path, rec.code)
		}
		if len(keys) == 0 {
			return json.RawMessage(rec.body.Bytes()), nil
		}
		var all map[string]json.RawMessage
		if err := json.Unmarshal(rec.body.Bytes(), &all); err != nil {
			return nil, err
		}
		picked := make(map[string]json.RawMessage, len(keys))
		for _, k := range keys {
			if v, ok := all[k]; ok {
				picked[k] = v
			}
		}
		return picked, nil
	}
}

type recorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(code int)        { r.code = code }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
