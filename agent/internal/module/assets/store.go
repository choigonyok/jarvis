// Package assets lets the model read the portfolio: holdings, return against
// principal, drift from the target allocation, and the daily snapshots.
//
// It is read-only, and not by convention: the module implements no Actuator,
// so there is no action kind for the gate to approve and no tool that writes.
// The keys that can place orders stay in assets-svc; this package only ever
// holds the bearer token for that service's GET routes.
package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Holding is one position, in the shape assets-svc sends.
type Holding struct {
	Venue    string  `json:"venue"` // upbit | kis | gold
	Kind     string  `json:"kind"`  // coin | stock | gold
	Symbol   string  `json:"symbol"`
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	AvgPrice float64 `json:"avgPrice"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"` // KRW | USD
	ValueKrw float64 `json:"valueKrw"`
	CostKrw  float64 `json:"costKrw"`
}

// Change is how the current basket moved over a window. Rate is a fraction;
// nil means nothing could be priced that far back.
type Change struct {
	Rate      *float64 `json:"rate"`
	AmountKrw *float64 `json:"amountKrw"`
	Missing   []string `json:"missing"`
}

type PrincipalPart struct {
	Venue        string   `json:"venue"`
	ValueKrw     *float64 `json:"valueKrw"`
	PrincipalKrw float64  `json:"principalKrw"`
	ProfitKrw    *float64 `json:"profitKrw"`
	Rate         *float64 `json:"rate"`
}

type Principal struct {
	Since        string          `json:"since"`
	PrincipalKrw float64         `json:"principalKrw"`
	ProfitKrw    float64         `json:"profitKrw"`
	Rate         float64         `json:"rate"`
	Parts        []PrincipalPart `json:"parts"`
}

type FixedAsset struct {
	Label    string  `json:"label"`
	ValueKrw float64 `json:"valueKrw"`
}

// Drift is one bucket against its target. GapKrw positive means buy.
type Drift struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	ValueKrw float64  `json:"valueKrw"`
	Current  float64  `json:"current"`
	Target   float64  `json:"target"`
	GapKrw   float64  `json:"gapKrw"`
	InBand   bool     `json:"inBand"`
	Symbols  []string `json:"symbols"`
}

type Move struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	AmountKrw float64 `json:"amountKrw"`
	Optional  bool    `json:"optional"`
}

// Allocation is computed in assets-svc, not here, so the console and the
// model are reading one calculation. Nil when the service predates it.
type Allocation struct {
	Rows     []Drift `json:"rows"`
	Moves    []Move  `json:"moves"`
	TotalKrw float64 `json:"totalKrw"`
}

// Portfolio is the subset of /portfolio this module reads. The curve series
// are left out: they are for drawing, and a model reading three hundred
// points learns nothing the window changes do not already say.
type Portfolio struct {
	Holdings   []Holding         `json:"holdings"`
	CashKrw    float64           `json:"cashKrw"`
	TotalKrw   float64           `json:"totalKrw"`
	CostKrw    float64           `json:"costKrw"`
	ProfitKrw  float64           `json:"profitKrw"`
	ReturnRate float64           `json:"returnRate"`
	UsdKrw     float64           `json:"usdKrw"`
	Changes    map[string]Change `json:"changes"`
	At         string            `json:"at"`
	Principal  *Principal        `json:"principal"`
	Fixed      []FixedAsset      `json:"fixed"`
	Allocation *Allocation       `json:"allocation"`
	Problems   []string          `json:"problems"`
}

// Snapshot is one recorded day: what was actually held then, not today's
// basket re-priced.
type Snapshot struct {
	Date     string  `json:"date"`
	TotalKrw float64 `json:"totalKrw"`
	CashKrw  float64 `json:"cashKrw"`
	CostKrw  float64 `json:"costKrw"`
}

// Store talks to assets-svc. No cache: the service already holds a 30-second
// one and coalesces concurrent requests, and a second layer here would only
// make the two disagree about how fresh "now" is.
type Store struct {
	base   string
	token  string
	client *http.Client
}

func NewStore(baseURL, token string) *Store {
	return &Store{
		base:  strings.TrimRight(baseURL, "/"),
		token: token,
		// 캐시가 비어 있으면 서비스가 두 증권사와 과거 종가를 걸어 올라가느라
		// 수 초가 걸린다. 캘린더의 10초로는 첫 조회가 잘린다.
		client: &http.Client{Timeout: 45 * time.Second},
	}
}

func (s *Store) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("자산 서비스에 연결하지 못했습니다: %w", err)
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
		return fmt.Errorf("자산 서비스가 거부했습니다: %s", res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (s *Store) Portfolio(ctx context.Context) (Portfolio, error) {
	var p Portfolio
	err := s.get(ctx, "/portfolio", &p)
	return p, err
}

func (s *Store) History(ctx context.Context, days int) ([]Snapshot, error) {
	var payload struct {
		Snapshots []Snapshot `json:"snapshots"`
	}
	err := s.get(ctx, "/history?days="+strconv.Itoa(days), &payload)
	return payload.Snapshots, err
}
