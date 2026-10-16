// Command phonenote-svc reads the phone's notifications and makes something
// of them.
//
// The phone's notifications reach a Mac through iPhone Mirroring; a
// collector there (cmd/collector) reads the Mac's notification store and
// hands them in. Every one is kept. Each is read - by rule when that is
// enough (an ad, a login code, an app that never says anything), by the
// model otherwise - into what happened: a delivery order and its menu, a
// payment, money in or out, a parcel, a booking. What that changes, it
// changes: a 가계부 payment gets the items and the shop the card alert left
// out. What is worth keeping is offered to mind as occurrences.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/api"
	"github.com/choigonyok/jarvis/phonenote-svc/internal/apply"
	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
	"github.com/choigonyok/jarvis/phonenote-svc/internal/read"
	"github.com/choigonyok/jarvis/phonenote-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	token := os.Getenv("API_TOKEN")
	if token == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
		os.Exit(1)
	}
	collectorToken := os.Getenv("COLLECTOR_TOKEN")
	if collectorToken == "" {
		log.Warn("COLLECTOR_TOKEN 이 없어 수집기가 알림을 보낼 수 없습니다")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Error("DATABASE_URL 을 해석하지 못했습니다", "err", err)
		os.Exit(1)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Error("데이터베이스 풀을 만들지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	loc, err := time.LoadLocation(getenv("TZ", "Asia/Seoul"))
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}
	st := store.New(pool)
	model := &read.Anthropic{Key: os.Getenv("ANTHROPIC_API_KEY"), ModelID: os.Getenv("PHONENOTE_MODEL"), Budget: number("PHONENOTE_DAILY_USD", 0.3)}
	if !model.Ready() {
		log.Warn("모델 키(ANTHROPIC_API_KEY)가 없어 규칙으로 읽히는 알림만 해석합니다")
	}
	w := &worker{st: st, reader: read.Reader{Model: model}, log: log, tried: map[int64]time.Time{}}
	if url := os.Getenv("SPENDING_URL"); url != "" {
		w.applier = apply.Applier{Spending: apply.HTTP{URL: url, Token: token}, Loc: loc}
	}
	wake := make(chan struct{}, 1)
	go w.run(ctx, wake)

	addr := getenv("LISTEN_ADDR", ":8104")
	srv := &http.Server{
		Addr: addr,
		Handler: (&api.Server{Store: st, Token: token, CollectorToken: collectorToken, Log: log, Wake: func() {
			select {
			case wake <- struct{}{}:
			default:
			}
		}}).Handler(),
		ReadTimeout:  20 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		log.Info("phonenote-svc 가 듣기 시작했습니다", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 서버", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

// worker reads what is new and applies what was read, a batch at a time.
type worker struct {
	st      *store.Store
	reader  read.Reader
	applier apply.Applier
	log     *slog.Logger
	// tried: a reading waiting for its payment is looked at every ten
	// minutes, not on every pass.
	tried map[int64]time.Time
}

func (w *worker) run(ctx context.Context, wake <-chan struct{}) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		if err := w.pass(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("알림을 처리하지 못했습니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wake:
		}
	}
}

func (w *worker) pass(ctx context.Context) error {
	open, err := w.st.Open(ctx, 30)
	if err != nil {
		return err
	}
	for _, n := range open {
		if n.Status == "new" {
			n = w.read(ctx, n)
			if n.Status != "read" {
				if err := w.st.Save(ctx, n); err != nil {
					return err
				}
				continue
			}
		} else if at, ok := w.tried[n.ID]; ok && time.Since(at) < 10*time.Minute {
			continue
		}
		w.tried[n.ID] = time.Now()
		applied, done, err := w.applier.Apply(ctx, n)
		switch {
		case err != nil:
			n.Tries++
			n.Error = err.Error()
			if n.Tries >= 8 {
				n.Status = "failed"
			}
		case done:
			n.Status, n.Applied, n.Error = "done", applied, ""
			delete(w.tried, n.ID)
		}
		if applied != "" {
			w.log.Info("알림을 반영했습니다", "app", n.App, "applied", applied)
		}
		if err := w.st.Save(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// read turns a new notification into a reading: rules, then the model.
func (w *worker) read(ctx context.Context, n note.Note) note.Note {
	r, ok := read.Rule(n)
	if !ok {
		if usual, err := w.st.Usual(ctx, n.App, 8); err == nil && read.Quiet(usual) {
			r, ok = note.Reading{Kind: note.Other, Summary: "이 앱 알림은 남길 것이 없었어요", By: "rule"}, true
		}
	}
	if !ok {
		var err error
		r, err = w.reader.Read(ctx, n)
		if err != nil {
			n.Tries++
			n.Error = err.Error()
			switch {
			case errors.Is(err, read.ErrNoModel):
				n.Status = "skipped"
			case errors.Is(err, read.ErrBudget):
				// Tomorrow's budget reads it.
			case n.Tries >= 5:
				n.Status = "failed"
			}
			return n
		}
	}
	n.Reading, n.Status, n.Error = &r, "read", ""
	return n
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func number(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return fallback
}
