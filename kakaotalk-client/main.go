// Command kakaotalk-client is a microservice that polls the local KakaoTalk
// SQLCipher database on a short interval, stores new messages incrementally in a
// plain SQLite file, and serves them over a small read-only HTTP API.
//
// It must run on the macOS host (or a container with the KakaoTalk container
// directory bind-mounted and KAKAO_UUID / KAKAO_USER_ID supplied), because the
// encryption key is derived from the machine's IOPlatformUUID and the account
// id, and only the host can read the live -wal file as messages arrive.
package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/choigonyok/jarvis/gocommon/collect"
	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/api"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/kakao"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/poller"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/store"
)

func main() {
	cfg := kakao.Config{
		ContainerDir: os.Getenv("KAKAO_CONTAINER_DIR"),
		HomeDir:      os.Getenv("KAKAO_HOME"),
		UUID:         os.Getenv("KAKAO_UUID"),
		UserID:       envInt64("KAKAO_USER_ID", 0),
		Key:          os.Getenv("KAKAO_SQLCIPHER_KEY"),
		PlistDir:     os.Getenv("KAKAO_PLIST_DIR"),
		ScratchDir:   os.Getenv("KAKAO_SCRATCH_DIR"),
	}

	storePath := getenv("STORE_PATH", "./data/kakaotalk.db")
	if err := os.MkdirAll(dirOf(storePath), 0o700); err != nil {
		log.Fatalf("store dir: %v", err)
	}
	interval := envDuration("POLL_INTERVAL", time.Second)
	batch := int(envInt64("POLL_BATCH", 500))
	addr := getenv("LISTEN_ADDR", ":8090")
	// An empty token would leave every route open to anything on the
	// network. Only a local trial may run that way, and must say so.
	if os.Getenv("API_TOKEN") == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Fatal("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
	}
	token := os.Getenv("API_TOKEN")

	st, err := store.Open(storePath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	reader, err := kakao.Open(cfg)
	if err != nil {
		log.Fatalf("open kakao source: %v", err)
	}
	log.Printf("kakaotalk-client: source unlocked (user_id=%d), polling every %s", reader.UserID(), interval)

	p := poller.New(reader, st, interval, batch)
	p.ImportHistory = os.Getenv("KAKAO_IMPORT_HISTORY") != "false"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go p.Run(ctx)
	// 들어온 메시지는 봉인해서 입구로 넘긴다. mind 는 이제 수집기를 읽으러 오지 않는다.
	loops := health.NewLoops()
	if url, key := os.Getenv("INPUT_URL"), os.Getenv("MESSAGE_KEY"); url != "" && key != "" {
		sealer, err := seal.FromBase64(key)
		if err != nil {
			log.Fatalf("MESSAGE_KEY: %v", err)
		}
		logger := slog.Default()
		pub := &collect.Publisher{
			Source: "kakao", CursorPath: storePath + ".published", Seal: sealer, Every: interval, Loops: loops, Log: logger,
			Send:   record.Post(url, token, logger),
			Latest: func() (int64, error) { _, top, err := st.RowIDRange(); return top, err },
			After: func(rowid int64, limit int) ([]collect.Item, error) {
				msgs, err := st.MessagesAfter(rowid, limit)
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
	// 평문 사본의 보존 기한(일). 0 이면 지우지 않는다.
	go collect.Retain(ctx, int(envInt64("RETAIN_DAYS", 730)), st.Prune, func(n int64, err error) {
		log.Printf("retain: deleted=%d err=%v", n, err)
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(st, p, token, loops).Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		log.Printf("kakaotalk-client: HTTP listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("kakaotalk-client: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
