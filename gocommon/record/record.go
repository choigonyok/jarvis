// Package record writes what happened as Records: the one shape everything
// the assistant sees will come through (input.records, migration 075), and
// what a replay reads.
//
// Today it is what stage 0 of the to-be structure needs: the payment guard's
// questions and answers, payment cards and how they were answered. A record
// is written beside the work it is about and must never hold it up - the
// guard's answer goes out whether or not the record did - so Write only
// queues, and one goroutine writes in order. A database that is away is
// waited for, in order, and the loop says so on /health.
//
// It writes to input.outbox, the same as everything else that happens:
// input-svc alone stores a record.
package record

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The two tiers of a record.
const (
	// Raw may hold what a person typed. It can be deleted after its
	// retention period.
	Raw = "raw"
	// Durable is what was decided. It is never changed.
	Durable = "durable"
)

// Record is the envelope. The payload is opaque here: only its kind's schema
// knows what is in it.
type Record struct {
	Kind      string
	Source    string
	SourceKey string
	// Actor is who did it, Channel the way it came: an operator is not the
	// same claim as the console (a person speaking is not the same as a
	// channel being authenticated).
	Actor   string
	Channel string
	// Subject is what it is about; order is kept per subject.
	Subject    string
	OccurredAt time.Time
	Tier       string
	// SchemaVersion is the version of this kind's payload; zero means 1.
	SchemaVersion int
	// TraceID ties a flow together; CausationID is the source key of the
	// record that led to this one.
	TraceID     string
	CausationID string
	Payload     any
}

// Check says what is wrong with the envelope, if anything.
func (r Record) Check() error {
	var missing []string
	for _, f := range []struct{ name, value string }{
		{"kind", r.Kind}, {"source", r.Source}, {"source_key", r.SourceKey}, {"actor", r.Actor},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	if r.OccurredAt.IsZero() {
		missing = append(missing, "occurred_at")
	}
	if len(missing) > 0 {
		return fmt.Errorf("기록에 %s 가 없습니다", strings.Join(missing, ", "))
	}
	if r.Tier != Raw && r.Tier != Durable {
		return fmt.Errorf("기록의 층이 %q 입니다", r.Tier)
	}
	return nil
}

// sink is where records end up.
type sink interface {
	append(ctx context.Context, r Record, payload []byte) error
}

// LoopName is the Writer's loop on /health.
const LoopName = "records"

const (
	queueSize = 4096
	// tryFor bounds one write. The queue waits behind it, nothing else does.
	tryFor = 5 * time.Second
	// The wait between tries while the database is away.
	firstWait = time.Second
	mostWait  = time.Minute
	// stale is how long the loop may go without writing or confirming the
	// database before the status tab calls it stopped.
	stale = 15 * time.Minute
)

// Writer writes records in order. A nil Writer does nothing, so a process
// without a database runs as before.
type Writer struct {
	to    sink
	queue chan queued
	loops *health.Loops
	log   *slog.Logger
	// dropped counts records that found the queue full: a database away for
	// long enough to fill it, and still not worth holding up a payment for.
	dropped  atomic.Int64
	wait     func(ctx context.Context, d time.Duration)
	ping     func(ctx context.Context) error
	interval time.Duration
}

type queued struct {
	r       Record
	payload []byte
}

// New is a Writer on the database at dsn, or nil with none. Run must be
// running for anything to reach it.
func New(dsn string, loops *health.Loops, log *slog.Logger) *Writer {
	if dsn == "" {
		return nil
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Warn("기록을 남기지 않습니다: DATABASE_URL 을 해석하지 못했습니다", "err", err)
		return nil
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		log.Warn("기록을 남기지 않습니다", "err", err)
		return nil
	}
	w := newWriter(pgSink{pool}, loops, log)
	w.ping = pool.Ping
	return w
}

// Post is the way to hand one record to input-svc at url and know that it was
// taken: for those who keep their own cursor and must not move it otherwise.
func Post(url, token string, log *slog.Logger) func(ctx context.Context, r Record) error {
	s := httpSink{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: tryFor}, log: log}
	return func(ctx context.Context, r Record) error {
		if r.SchemaVersion == 0 {
			r.SchemaVersion = 1
		}
		payload, err := json.Marshal(r.Payload)
		if err != nil {
			return err
		}
		return s.append(ctx, r, payload)
	}
}

// NewHTTP is a Writer that hands records to input-svc at url: for a process
// with no way to the outbox (the guest's agent). Run must be running.
func NewHTTP(url, token string, loops *health.Loops, log *slog.Logger) *Writer {
	if url == "" {
		return nil
	}
	return newWriter(httpSink{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: tryFor}, log: log}, loops, log)
}

func newWriter(to sink, loops *health.Loops, log *slog.Logger) *Writer {
	loops.Add(LoopName, "기록 남기기", stale)
	return &Writer{
		to: to, queue: make(chan queued, queueSize), loops: loops, log: log, interval: 5 * time.Minute,
		wait: func(ctx context.Context, d time.Duration) {
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
		},
	}
}

// Write queues a record and returns at once. A record that cannot be a
// record is a bug in the caller: it is logged and left out.
func (w *Writer) Write(r Record) {
	if w == nil {
		return
	}
	if r.OccurredAt.IsZero() {
		r.OccurredAt = time.Now()
	}
	if r.SchemaVersion == 0 {
		r.SchemaVersion = 1
	}
	if err := r.Check(); err != nil {
		w.log.Warn("기록을 남기지 않습니다", "kind", r.Kind, "err", err)
		return
	}
	payload := []byte("{}")
	if r.Payload != nil {
		b, err := json.Marshal(r.Payload)
		if err != nil {
			w.log.Warn("기록을 남기지 않습니다", "kind", r.Kind, "err", err)
			return
		}
		payload = b
	}
	select {
	case w.queue <- queued{r, payload}:
	default:
		if n := w.dropped.Add(1); n == 1 || n%100 == 0 {
			w.log.Warn("기록 대기열이 찼습니다. 데이터베이스가 돌아오지 않으면 기록이 빠집니다", "빠진 수", n)
		}
	}
}

// Dropped is how many records never got into the queue.
func (w *Writer) Dropped() int64 {
	if w == nil {
		return 0
	}
	return w.dropped.Load()
}

// Run writes queued records until ctx ends, and what is left after it for a
// few seconds. An idle round still checks the database, so that a Writer with
// nothing to write is not called stopped - and one that cannot reach it is.
func (w *Writer) Run(ctx context.Context) {
	if w == nil {
		return
	}
	idle := time.NewTicker(w.interval)
	defer idle.Stop()
	for {
		select {
		case q := <-w.queue:
			w.write(ctx, q)
		case <-idle.C:
			w.check(ctx)
		case <-ctx.Done():
			w.flush()
			return
		}
	}
}

// check is the round of a loop with nothing to do.
func (w *Writer) check(ctx context.Context) {
	if w.ping == nil {
		w.loops.OK(LoopName)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, tryFor)
	defer cancel()
	if err := w.ping(cctx); err != nil {
		w.loops.Fail(LoopName, err)
		return
	}
	w.loops.OK(LoopName)
}

// write puts one record in, however long the database is away: order is what
// a replay rests on, and nothing is waiting on the record.
func (w *Writer) write(ctx context.Context, q queued) {
	for n := 1; ; n++ {
		tctx, cancel := context.WithTimeout(ctx, tryFor)
		err := w.to.append(tctx, q.r, q.payload)
		cancel()
		if err == nil {
			w.loops.OK(LoopName)
			w.waiting(nil)
			return
		}
		if ctx.Err() != nil {
			w.requeue(q)
			return
		}
		w.loops.Fail(LoopName, err)
		at := q.r.OccurredAt
		w.waiting(&at)
		if n == 1 {
			w.log.Warn("기록을 남기지 못해 다시 시도합니다", "kind", q.r.Kind, "err", err)
		}
		w.wait(ctx, health.Backoff(n, firstWait, mostWait))
	}
}

func (w *Writer) waiting(since *time.Time) { w.loops.Waiting(LoopName, since) }

// requeue puts a record being written when ctx ended back for flush.
func (w *Writer) requeue(q queued) {
	select {
	case w.queue <- q:
	default:
		w.dropped.Add(1)
	}
}

// flush writes what is queued at shutdown, in the time a stop allows.
func (w *Writer) flush() {
	ctx, cancel := context.WithTimeout(context.Background(), tryFor)
	defer cancel()
	for {
		select {
		case q := <-w.queue:
			if err := w.to.append(ctx, q.r, q.payload); err != nil {
				if !errors.Is(err, context.DeadlineExceeded) {
					w.log.Warn("끝나면서 기록을 남기지 못했습니다", "kind", q.r.Kind, "err", err)
				}
				return
			}
		default:
			return
		}
	}
}

type pgSink struct{ pool *pgxpool.Pool }

// envelope is a Record as it lies in the outbox.
type envelope struct {
	Kind          string          `json:"kind"`
	Source        string          `json:"source"`
	SourceKey     string          `json:"source_key"`
	Actor         string          `json:"actor"`
	Channel       string          `json:"channel"`
	Subject       string          `json:"subject"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Tier          string          `json:"tier"`
	SchemaVersion int             `json:"schema_version"`
	TraceID       string          `json:"trace_id"`
	CausationID   string          `json:"causation_id"`
	Payload       json.RawMessage `json:"payload"`
}

// outboxEnvelope is a record as it travels.
func outboxEnvelope(r Record, payload []byte) envelope {
	return envelope{
		Kind: r.Kind, Source: r.Source, SourceKey: r.SourceKey, Actor: r.Actor, Channel: r.Channel, Subject: r.Subject,
		OccurredAt: r.OccurredAt, Tier: r.Tier, SchemaVersion: r.SchemaVersion, TraceID: r.TraceID, CausationID: r.CausationID,
		Payload: payload,
	}
}

func (s pgSink) append(ctx context.Context, r Record, payload []byte) error {
	body, err := json.Marshal(outboxEnvelope(r, payload))
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `insert into input.outbox (payload) values ($1)`, body)
	return err
}

type httpSink struct {
	url, token string
	http       *http.Client
	log        *slog.Logger
}

// append posts a record. One input-svc refuses is not tried again: it would
// hold up everything behind it for good.
func (s httpSink) append(ctx context.Context, r Record, payload []byte) error {
	body, err := json.Marshal(outboxEnvelope(r, payload))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url+"/records", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode < 500 && resp.StatusCode != http.StatusUnauthorized:
		s.log.Warn("입구가 기록을 거절했습니다", "kind", r.Kind, "status", resp.StatusCode)
		return nil
	}
	return fmt.Errorf("입구가 %d 를 돌려줬습니다", resp.StatusCode)
}

// Memory keeps records in memory: a test's Writer, and a replay's, which
// compares what a run wrote with what was recorded.
type Memory struct {
	mu   sync.Mutex
	list []Record
}

func (m *Memory) append(_ context.Context, r Record, _ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.list = append(m.list, r)
	return nil
}

// All is what has been written so far, in order.
func (m *Memory) All() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.list...)
}

// Find is the first written record that matches, waiting for it a moment:
// Write only queues.
func (m *Memory) Find(match func(Record) bool) (Record, bool) {
	for i := 0; i < 400; i++ {
		for _, r := range m.All() {
			if match(r) {
				return r, true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return Record{}, false
}

// NewMemory is a Writer that keeps what it is given. Run must be running.
func NewMemory(log *slog.Logger) (*Writer, *Memory) {
	m := &Memory{}
	return newWriter(m, nil, log), m
}

// KindSpendingWrite is a change to the household book, which mind keeps.
const KindSpendingWrite = "spending.write"

// KindJobsWrite is a change to a job, which mind keeps.
const KindJobsWrite = "jobs.write"

// KindRegistryWrite is a change to a row of mind's registry.
const KindRegistryWrite = "registry.write"

// CommandKind names a request for mind as the record it travels as: what the
// agent asks of mind and what mind's door takes both come as these, and only
// these change its state.
func CommandKind(method, path string) string {
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" {
		return "command"
	}
	switch parts[1] {
	case "intents":
		return "intent.answer"
	case "notes":
		return "note.decided"
	case "links":
		return "link.decided"
	case "spending":
		return KindSpendingWrite
	case "jobs":
		return KindJobsWrite
	case "registry":
		return KindRegistryWrite
	case "reminders":
		switch method {
		case "POST":
			return "reminder.set"
		case "PATCH":
			return "reminder.change"
		case "DELETE":
			return "reminder.cancel"
		}
	case "checklists":
		switch {
		case method == "POST" && len(parts) == 2:
			return "checklist.make"
		case method == "PATCH":
			return "checklist.change"
		case method == "DELETE":
			return "checklist.close"
		case len(parts) >= 4 && parts[3] == "items":
			return "checklist.check"
		case len(parts) >= 4 && parts[3] == "show":
			return "checklist.show"
		}
	}
	return "command"
}
