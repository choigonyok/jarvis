// Command search-svc finds and reads the web for the assistant.
//
// Three things the conversation's own web tools could not do, in one place:
// search Korea where Korea writes (Naver's blogs, cafes, news and web, by
// NAVER API HUB), read a page together with what its pictures say (the OCR on
// the Mac mini - an event's card, a product's detail image), and list places
// near a point the way Naver Map does (open now, until when, stars). The
// agent calls it with one token; it calls out to the web and to the OCR, and
// to nothing else.
//
// It sits on a network of its own (compose: search) with the agents: what it
// fetches is anyone's page, and a page that took it over would find no other
// service to reach. Its fetcher refuses private addresses on top of that.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/choigonyok/jarvis/search-svc/internal/api"
	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
	"github.com/choigonyok/jarvis/search-svc/internal/imgtext"
	"github.com/choigonyok/jarvis/search-svc/internal/naver"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	token := os.Getenv("API_TOKEN")
	if token == "" {
		// Up but off, not a restart loop: the stack comes up the same on a
		// machine that has not set search up yet.
		log.Warn("API_TOKEN(JARVIS_SEARCH_TOKEN)이 없어 검색은 꺼져 있습니다 - /health 만 답합니다")
	}
	client := &naver.Client{ID: os.Getenv("NAVER_API_HUB_ID"), Secret: os.Getenv("NAVER_API_HUB_SECRET"), Base: os.Getenv("NAVER_API_HUB_URL")}
	if !client.Configured() {
		log.Warn("NAVER_API_HUB_ID·SECRET 이 없어 네이버 검색은 꺼져 있습니다(읽기·장소는 됩니다)")
	}
	web := fetch.New()
	ocrURL := strings.TrimRight(os.Getenv("OCR_URL"), "/")
	ocrHTTP := &http.Client{Timeout: 40 * time.Second}
	images := &imgtext.Reader{OCRURL: ocrURL, Token: os.Getenv("OCR_TOKEN"), HTTP: ocrHTTP, Fetch: web}
	if images.Token == "" {
		images.Token = token
	}

	srv := &http.Server{
		Addr: getenv("LISTEN_ADDR", ":8104"),
		Handler: (&api.Server{
			Token: token, Naver: client, Fetch: web, Images: images, Log: log,
			OCRHealth: ocrHealth(ocrURL, ocrHTTP),
		}).Handler(),
		ReadTimeout: 10 * time.Second,
		// A page with a long detail image is read in tens of seconds.
		WriteTimeout: 150 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("search-svc 가 듣기 시작했습니다", "addr", srv.Addr, "naver", client.Configured(), "ocr", ocrURL != "")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("서버가 멈췄습니다", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
}

// ocrHealth asks the OCR's /health, at most once a minute.
func ocrHealth(base string, client *http.Client) func(context.Context) bool {
	var mu sync.Mutex
	var at time.Time
	var up bool
	return func(ctx context.Context) bool {
		if base == "" {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) < time.Minute {
			return up
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		up = err == nil && resp.StatusCode == http.StatusOK
		if resp != nil {
			resp.Body.Close()
		}
		at = time.Now()
		return up
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
