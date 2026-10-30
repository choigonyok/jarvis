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
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/choigonyok/jarvis/gocommon/collect"
	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/imessage-client/internal/api"
	"github.com/choigonyok/jarvis/imessage-client/internal/imsg"
	"github.com/choigonyok/jarvis/imessage-client/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// An empty token would leave every route open to anything on the
	// network. Only a local trial may run that way, and must say so.
	if os.Getenv("API_TOKEN") == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	storePath := getenv("STORE_PATH", "./data/imessage.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		log.Error("저장소 폴더를 만들지 못했습니다", "err", err)
		os.Exit(1)
	}
	st, err := store.Open(storePath)
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

	// 들어온 메시지는 봉인해서 입구로 넘긴다. mind 는 이제 수집기를 읽으러 오지 않는다.
	loops := health.NewLoops()
	if url, key := os.Getenv("INPUT_URL"), os.Getenv("MESSAGE_KEY"); url != "" && key != "" {
		sealer, err := seal.FromBase64(key)
		if err != nil {
			log.Error("MESSAGE_KEY 를 읽지 못했습니다", "err", err)
			os.Exit(1)
		}
		pub := &collect.Publisher{
			Source: "imessage", CursorPath: storePath + ".published", Seal: sealer, Every: interval, Loops: loops, Log: log,
			Send:   record.Post(url, os.Getenv("API_TOKEN"), log),
			Latest: st.Cursor,
			After: func(rowid int64, limit int) ([]collect.Item, error) {
				msgs, err := st.After(rowid, limit)
				if err != nil {
					return nil, err
				}
				out := make([]collect.Item, len(msgs))
				for i, m := range msgs {
					out[i] = collect.Item{RowID: m.RowID, ChatID: m.ChatID, SentAt: m.SentAt, Body: m}
				}
				return out, nil
			},
		}
		go pub.Run(ctx)
	}

	// 보존 기한이 지난 메시지는 하루에 한 번 지운다(0 = 모두 둔다).
	days, _ := strconv.Atoi(getenv("RETAIN_DAYS", "730"))
	go collect.Retain(ctx, days, st.Prune, func(n int64, err error) {
		log.Info("보존 기한이 지난 메시지를 지웠습니다", "count", n, "days", days, "err", err)
	})

	srv := &http.Server{
		Addr:              getenv("LISTEN_ADDR", ":8099"),
		Handler:           api.New(st, status, os.Getenv("API_TOKEN"), loops).Handler(),
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
