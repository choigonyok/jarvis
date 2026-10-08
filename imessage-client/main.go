// Command imessage-client collects iMessage and SMS from the Mac's Messages
// database, the way kakaotalk-client collects KakaoTalk: it reads forward from
// the last row it stored, keeps its own copy, and serves it read-only in the
// same shape, so the console's 메시지 tab reads both alike.
//
// The Mac must be signed in to Messages (with Messages in iCloud on, to have
// the phone's history), and Docker needs Full Disk Access to read
// ~/Library/Messages.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/choigonyok/jarvis/imessage-client/internal/api"
	"github.com/choigonyok/jarvis/imessage-client/internal/imsg"
	"github.com/choigonyok/jarvis/imessage-client/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(getenv("STORE_PATH", "./data/imessage.db"))
	if err != nil {
		log.Error("저장소를 열지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	status := &api.Status{}
	interval := duration("POLL_INTERVAL", 2*time.Second)
	cfg := imsg.Config{
		ChatDB:     getenv("CHAT_DB", "/messages/chat.db"),
		ContactsDB: os.Getenv("CONTACTS_DB"),
		Scratch:    getenv("SCRATCH_DIR", "/tmp/imessage-scratch"),
	}

	// The reader is opened in the loop: until Full Disk Access is granted the
	// file is not there, and the service should serve /status saying so rather
	// than exit and restart forever.
	go func() {
		var reader *imsg.Reader
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			if reader == nil {
				r, err := imsg.Open(cfg)
				if err != nil {
					total, _ := st.Total()
					status.Set(0, total, err)
				} else {
					reader = r
					log.Info("메시지 데이터베이스를 읽기 시작합니다", "path", cfg.ChatDB)
				}
			}
			if reader != nil {
				cursor, _ := st.Cursor()
				msgs, err := reader.After(cursor, 1000)
				if err == nil && len(msgs) > 0 {
					err = st.Upsert(msgs)
					cursor = msgs[len(msgs)-1].RowID
				}
				total, _ := st.Total()
				status.Set(cursor, total, err)
				if err != nil {
					log.Warn("메시지를 읽지 못했습니다", "err", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()

	srv := &http.Server{
		Addr:              getenv("LISTEN_ADDR", ":8099"),
		Handler:           api.New(st, status, os.Getenv("API_TOKEN")).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("imessage-client 가 듣기 시작했습니다", "addr", srv.Addr)
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

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func duration(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d > 0 {
		return d
	}
	return def
}
