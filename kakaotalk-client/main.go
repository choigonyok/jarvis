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
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go p.Run(ctx)

	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(st, p, token).Handler(),
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
