// Command photos-svc is the photo server's adapter. It reads what Immich holds
// and leaves a record when that changed: the last two days' photos, and the
// days that are over. It does not judge a photo and it keeps nothing of its
// own: mind no longer goes to Immich to read.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/watch"
)

const (
	recentEvery = 2 * time.Minute
	recentBack  = 48 * time.Hour
	daysEvery   = 30 * time.Minute
	// daysBack is how many finished days each read carries: a phone that
	// uploaded late, or a day this was not running.
	daysBack = 3
	// pageSize and maxPages bound one read.
	pageSize = 500
	maxPages = 50
)

// Asset is a photo, with what is read from it and no more.
type Asset struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	FileCreatedAt string `json:"fileCreatedAt"`
	LocalDateTime string `json:"localDateTime"`
	IsFavorite    bool   `json:"isFavorite,omitempty"`
	IsTrashed     bool   `json:"isTrashed,omitempty"`
	ExifInfo      *struct {
		City             *string `json:"city"`
		State            *string `json:"state"`
		Country          *string `json:"country"`
		DateTimeOriginal *string `json:"dateTimeOriginal"`
	} `json:"exifInfo,omitempty"`
	People []struct {
		Name string `json:"name"`
	} `json:"people,omitempty"`
}

// Immich reads the photo server.
type Immich struct {
	URL, Key string
	HTTP     *http.Client
}

// Taken is what was taken between from and to, oldest pages first.
func (im Immich) Taken(ctx context.Context, from time.Time, to *time.Time) ([]Asset, error) {
	out := []Asset{}
	for page := 1; page > 0 && page < maxPages; {
		q := map[string]any{"takenAfter": from.UTC().Format(time.RFC3339), "withExif": true, "withPeople": true, "size": pageSize, "page": page}
		if to != nil {
			q["takenBefore"] = to.UTC().Format(time.RFC3339)
		}
		body, err := json.Marshal(q)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(im.URL, "/")+"/api/search/metadata", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", im.Key)
		resp, err := im.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		var got struct {
			Assets struct {
				Items    []Asset `json:"items"`
				NextPage *string `json:"nextPage"`
			} `json:"assets"`
		}
		if resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("immich: %d", resp.StatusCode)
		}
		err = json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, got.Assets.Items...)
		page = 0
		if got.Assets.NextPage != nil {
			page, _ = strconv.Atoi(*got.Assets.NextPage)
		}
	}
	return out, nil
}

// Recent is the last two days' photos.
func (im Immich) Recent(now func() time.Time) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		items, err := im.Taken(ctx, now().Add(-recentBack), nil)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	}
}

// Day is one finished day's photos.
type Day struct {
	Date  string  `json:"date"`
	Items []Asset `json:"items"`
}

// Days is the last days that are over: what memory keeps of a day is said once
// the day is done.
func (im Immich) Days(now func() time.Time, loc *time.Location) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		t := now().In(loc)
		today := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		out := []Day{}
		for i := daysBack; i >= 1; i-- {
			from := today.AddDate(0, 0, -i)
			to := from.AddDate(0, 0, 1)
			items, err := im.Taken(ctx, from, &to)
			if err != nil {
				return nil, err
			}
			out = append(out, Day{Date: from.Format("2006-01-02"), Items: items})
		}
		return map[string]any{"days": out}, nil
	}
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	token, url := os.Getenv("API_TOKEN"), os.Getenv("INPUT_URL")
	if token == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	loc, err := time.LoadLocation(getenv("TZ", "Asia/Seoul"))
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}
	loops := health.NewLoops()
	records := record.NewHTTP(url, token, loops, log)
	go records.Run(ctx)
	if os.Getenv("IMMICH_URL") != "" && os.Getenv("IMMICH_API_KEY") != "" {
		im := Immich{URL: os.Getenv("IMMICH_URL"), Key: os.Getenv("IMMICH_API_KEY"), HTTP: &http.Client{Timeout: 60 * time.Second}}
		(&watch.Watcher{Service: "photos", Records: records, Loops: loops, Log: log}).Run(ctx,
			watch.Source{Kind: "photos.recent", Label: "최근 사진", Every: recentEvery, Read: im.Recent(time.Now)},
			watch.Source{Kind: "photos.days", Label: "지난 날들의 사진", Every: daysEvery, Read: im.Days(time.Now, loc)},
		)
	} else {
		log.Warn("IMMICH_URL/IMMICH_API_KEY 가 없어 사진을 읽지 않습니다")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		health.Write(w, http.StatusOK, health.Report{Loops: loops.List()})
	})
	addr := getenv("LISTEN_ADDR", ":8107")
	srv := &http.Server{Addr: addr, Handler: mux, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		log.Info("photos-svc 가 듣기 시작했습니다", "addr", addr)
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

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
