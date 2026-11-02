// Command photos-svc is the photo server's adapter. It reads what Immich holds
// and leaves a record when that changed: the last two days' photos, and the
// days that are over. It does not judge a photo and it keeps nothing of its
// own: mind no longer goes to Immich to read.
//
// It is also the window the conversation looks through: find photos by what is
// in them, where and when (GET /search), and fetch one to show
// (GET /assets/{id}/preview). Reading only; albums are mind's to ask about.
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
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
	// walkDays is how many earlier days each read of the days adds, and
	// walkBack how far that goes: the library's past reaches memory a little
	// at a time, and only what is new there changes anything (mind keeps a hash).
	walkDays = 7
	walkBack = 2 * 366
	// pageSize and maxPages bound one read.
	pageSize = 500
	maxPages = 50
	// maxFound is the most one search answers; maxPreview bounds a photo.
	maxFound   = 40
	maxPreview = 20 << 20
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

// Query is what a search asks. Words are matched against what is in the photos
// (Immich's image embedding, any language); the rest narrows it.
type Query struct {
	Words    string
	City     string
	From, To *time.Time
	Favorite bool
	Size     int
}

// Found is a photo as a search answers it.
type Found struct {
	ID       string   `json:"id"`
	At       string   `json:"at"` // where it was taken, on that place's clock
	City     string   `json:"city,omitempty"`
	State    string   `json:"state,omitempty"`
	Country  string   `json:"country,omitempty"`
	People   []string `json:"people,omitempty"`
	Favorite bool     `json:"favorite,omitempty"`
}

// Find is the photos a query asks for: by likeness with words, the newest
// first without them.
func (im Immich) Find(ctx context.Context, q Query) ([]Found, error) {
	size := min(max(q.Size, 1), maxFound)
	ask := map[string]any{"size": size, "withExif": true, "type": "IMAGE"}
	path := "/api/search/metadata"
	if q.Words != "" {
		path, ask["query"] = "/api/search/smart", q.Words
	} else {
		ask["order"], ask["withPeople"] = "desc", true
	}
	if q.City != "" {
		ask["city"] = q.City
	}
	if q.From != nil {
		ask["takenAfter"] = q.From.UTC().Format(time.RFC3339)
	}
	if q.To != nil {
		ask["takenBefore"] = q.To.UTC().Format(time.RFC3339)
	}
	if q.Favorite {
		ask["isFavorite"] = true
	}
	body, err := json.Marshal(ask)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(im.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", im.Key)
	resp, err := im.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("immich: %d", resp.StatusCode)
	}
	var got struct {
		Assets struct {
			Items []Asset `json:"items"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return nil, err
	}
	out := make([]Found, 0, len(got.Assets.Items))
	for _, a := range got.Assets.Items {
		if a.IsTrashed {
			continue
		}
		f := Found{ID: a.ID, At: a.LocalDateTime, Favorite: a.IsFavorite}
		if a.ExifInfo != nil {
			f.City, f.State, f.Country = deref(a.ExifInfo.City), deref(a.ExifInfo.State), deref(a.ExifInfo.Country)
		}
		for _, p := range a.People {
			if p.Name != "" {
				f.People = append(f.People, p.Name)
			}
		}
		out = append(out, f)
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Preview is a photo at the size a screen shows it.
func (im Immich) Preview(ctx context.Context, id string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(im.URL, "/")+"/api/assets/"+id+"/thumbnail?size=preview", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("x-api-key", im.Key)
	resp, err := im.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("immich: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPreview+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxPreview {
		return nil, "", errors.New("사진이 너무 큽니다")
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// Day is one finished day's photos.
type Day struct {
	Date  string  `json:"date"`
	Items []Asset `json:"items"`
}

// Days is the last days that are over: what memory keeps of a day is said once
// the day is done.
func (im Immich) Days(now func() time.Time, loc *time.Location) func(context.Context) (any, error) {
	walked := 0
	return func(ctx context.Context) (any, error) {
		t := now().In(loc)
		today := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		out := []Day{}
		read := func(from time.Time, always bool) error {
			to := from.AddDate(0, 0, 1)
			items, err := im.Taken(ctx, from, &to)
			if err == nil && (always || len(items) > 0) {
				out = append(out, Day{Date: from.Format("2006-01-02"), Items: items})
			}
			return err
		}
		for i := daysBack; i >= 1; i-- {
			if err := read(today.AddDate(0, 0, -i), true); err != nil {
				return nil, err
			}
		}
		for n := 0; n < walkDays && walked < walkBack; n++ {
			if err := read(today.AddDate(0, 0, -daysBack-walked-1), false); err != nil {
				return nil, err
			}
			walked++
		}
		return map[string]any{"days": out}, nil
	}
}

var assetID = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

// door is the window's routes, behind the services' token.
func door(im Immich, token string, loc *time.Location) http.Handler {
	mux := http.NewServeMux()
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeError(w, http.StatusUnauthorized, "인증이 필요합니다")
				return
			}
			if im.URL == "" {
				writeError(w, http.StatusServiceUnavailable, "사진 서버가 연결되지 않았습니다")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /search", guard(func(w http.ResponseWriter, r *http.Request) {
		in := r.URL.Query()
		q := Query{Words: strings.TrimSpace(in.Get("q")), City: strings.TrimSpace(in.Get("city")), Favorite: in.Get("favorite") == "true"}
		q.Size, _ = strconv.Atoi(in.Get("limit"))
		if q.Size == 0 {
			q.Size = 12
		}
		for key, dst := range map[string]**time.Time{"from": &q.From, "to": &q.To} {
			v := in.Get(key)
			if v == "" {
				continue
			}
			day, err := time.ParseInLocation("2006-01-02", v, loc)
			if err != nil {
				writeError(w, http.StatusBadRequest, key+" 는 YYYY-MM-DD 입니다")
				return
			}
			if key == "to" {
				day = day.AddDate(0, 0, 1)
			}
			*dst = &day
		}
		items, err := im.Find(r.Context(), q)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	mux.HandleFunc("GET /assets/{id}/preview", guard(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !assetID.MatchString(id) {
			writeError(w, http.StatusBadRequest, "사진 id 가 아닙니다")
			return
		}
		data, kind, err := im.Preview(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if kind != "" {
			w.Header().Set("Content-Type", kind)
		}
		_, _ = w.Write(data)
	}))
	return mux
}

func writeError(w http.ResponseWriter, code int, text string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": text})
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
	var im Immich
	if os.Getenv("IMMICH_URL") != "" && os.Getenv("IMMICH_API_KEY") != "" {
		im = Immich{URL: os.Getenv("IMMICH_URL"), Key: os.Getenv("IMMICH_API_KEY"), HTTP: &http.Client{Timeout: 60 * time.Second}}
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
	mux.Handle("/", door(im, token, loc))
	addr := getenv("LISTEN_ADDR", ":8107")
	srv := &http.Server{Addr: addr, Handler: mux, ReadTimeout: 10 * time.Second, WriteTimeout: 70 * time.Second}
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
