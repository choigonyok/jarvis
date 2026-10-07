package market

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Store is the client to market-svc.
type Store struct {
	base   string
	token  string
	client *http.Client
}

func NewStore(baseURL, token string) *Store {
	return &Store{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

type Task struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	PriceKrw *int64 `json:"priceKrw,omitempty"`
	ToStatus string `json:"toStatus,omitempty"`
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
	Note     string `json:"note,omitempty"`
}

type Listing struct {
	ID          int64      `json:"id"`
	Status      string     `json:"status"`
	Title       string     `json:"title"`
	PriceKrw    int64      `json:"priceKrw"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	Condition   string     `json:"condition"`
	Shipping    string     `json:"shipping"`
	Photos      []string   `json:"photos"`
	JoongnaID   string     `json:"joongnaId,omitempty"`
	URL         string     `json:"url,omitempty"`
	Views       int        `json:"views"`
	Likes       int        `json:"likes"`
	Chats       int        `json:"chats"`
	Note        string     `json:"note,omitempty"`
	PostedAt    *time.Time `json:"postedAt,omitempty"`
	SoldAt      *time.Time `json:"soldAt,omitempty"`
	SyncedAt    *time.Time `json:"syncedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	Tasks       []Task     `json:"tasks"`
}

type State struct {
	LoginRequired bool       `json:"loginRequired"`
	LoginNote     string     `json:"loginNote,omitempty"`
	LastSyncAt    *time.Time `json:"lastSyncAt,omitempty"`
}

// NewListing is what an approved card sends to market-svc.
type NewListing struct {
	Title       string   `json:"title"`
	PriceKrw    int64    `json:"priceKrw"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Condition   string   `json:"condition"`
	Shipping    string   `json:"shipping"`
	Photos      []string `json:"photos"`
}

type Work struct {
	Task    Task    `json:"task"`
	Listing Listing `json:"listing"`
}

type Report struct {
	Outcome   string `json:"outcome"`
	JoongnaID string `json:"joongnaId,omitempty"`
	URL       string `json:"url,omitempty"`
	Note      string `json:"note,omitempty"`
}

type SyncItem struct {
	JoongnaID string `json:"joongnaId"`
	Status    string `json:"status"`
	PriceKrw  int64  `json:"priceKrw"`
	Views     int    `json:"views"`
	Likes     int    `json:"likes"`
	Chats     int    `json:"chats"`
	Gone      bool   `json:"gone"`
}

type Purge struct {
	ID     int64    `json:"id"`
	Photos []string `json:"photos"`
}

func (s *Store) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("중고나라 서비스에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
			return errors.New(payload.Error)
		}
		return fmt.Errorf("중고나라 서비스가 거부했습니다: %s", res.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (s *Store) Create(ctx context.Context, n NewListing) (Listing, error) {
	var l Listing
	err := s.call(ctx, http.MethodPost, "/listings", n, &l)
	return l, err
}

func (s *Store) List(ctx context.Context) ([]Listing, State, error) {
	var payload struct {
		Listings []Listing `json:"listings"`
		State    State     `json:"state"`
	}
	err := s.call(ctx, http.MethodGet, "/listings", nil, &payload)
	return payload.Listings, payload.State, err
}

func (s *Store) NextTask(ctx context.Context) (*Work, error) {
	var payload struct {
		Work *Work `json:"work"`
	}
	err := s.call(ctx, http.MethodGet, "/tasks/next", nil, &payload)
	return payload.Work, err
}

func (s *Store) ReportTask(ctx context.Context, id int64, r Report) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/tasks/%d/report", id), r, nil)
}

func (s *Store) SyncDue(ctx context.Context) ([]Listing, error) {
	var payload struct {
		Listings []Listing `json:"listings"`
	}
	err := s.call(ctx, http.MethodGet, "/sync/due", nil, &payload)
	return payload.Listings, err
}

func (s *Store) ReportSync(ctx context.Context, items []SyncItem, note string, loginRequired bool) (int, error) {
	var payload struct {
		Updated int `json:"updated"`
	}
	err := s.call(ctx, http.MethodPost, "/sync",
		map[string]any{"items": items, "note": note, "loginRequired": loginRequired}, &payload)
	return payload.Updated, err
}

func (s *Store) PurgeDue(ctx context.Context) ([]Purge, error) {
	var payload struct {
		Listings []Purge `json:"listings"`
	}
	err := s.call(ctx, http.MethodGet, "/purge/due", nil, &payload)
	return payload.Listings, err
}

func (s *Store) LoginResolved(ctx context.Context) error {
	return s.call(ctx, http.MethodPost, "/login/resolved", nil, nil)
}

func (s *Store) MarkPurged(ctx context.Context, id int64) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/listings/%d/purged", id), nil, nil)
}
