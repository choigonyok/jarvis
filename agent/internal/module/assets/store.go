// Package assets lets the model read the portfolio: holdings, return against
// principal, drift from the target allocation, and the daily snapshots.
//
// It is read-only to the model, and not by convention: no tool here writes.
// The one write is the operator's own answer to a card this module raises
// by itself - where money taken out of an account went (ask.go) - and it
// goes to assets-svc's /outflows/answer, which changes no figure. The keys
// that can place orders stay in assets-svc; nothing here reaches /orders.
package assets

import (
	"bytes"
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

// LedgerCheck is a place the hand-kept ledger disagrees with the trades.
type LedgerCheck struct {
	Venue     string  `json:"venue"`
	Kind      string  `json:"kind"` // short | total
	AmountKrw float64 `json:"amountKrw"`
	Message   string  `json:"message"`
}

type Principal struct {
	Since        string          `json:"since"`
	PrincipalKrw float64         `json:"principalKrw"`
	ProfitKrw    float64         `json:"profitKrw"`
	Rate         float64         `json:"rate"`
	Parts        []PrincipalPart `json:"parts"`
	Checks       []LedgerCheck   `json:"checks"`
	Outflows     []Outflow       `json:"outflows"`
}

// FlowRef is a deposit into an account here: what a withdrawal went into, or may have.
type FlowRef struct {
	Key       string  `json:"key"`
	Date      string  `json:"date"`
	Venue     string  `json:"venue"`
	AmountKrw float64 `json:"amountKrw"`
}

// OutflowPart is where part of a withdrawal went: transfer (back into an
// account here), spend, card (a card bill), saved, parked (still in the bank).
type OutflowPart struct {
	Use       string   `json:"use"`
	AmountKrw float64  `json:"amountKrw"`
	To        *FlowRef `json:"to,omitempty"`
	Category  string   `json:"category,omitempty"`
	Memo      string   `json:"memo,omitempty"`
	DecidedAt string   `json:"decidedAt,omitempty"`
}

// Outflow is one withdrawal from the pot and where it went (assets-svc
// outflows.ts). Status "ask": AskKrw of it is accounted for by nothing yet.
type Outflow struct {
	Key        string        `json:"key"`
	Date       string        `json:"date"`
	Venue      string        `json:"venue"`
	AmountKrw  float64       `json:"amountKrw"`
	Memo       string        `json:"memo"`
	Status     string        `json:"status"` // transfer | answered | waiting | ask
	Parts      []OutflowPart `json:"parts"`
	AskKrw     float64       `json:"askKrw"`
	Candidates []FlowRef     `json:"candidates"`
}

// OutflowAnswer is the operator's word on part of a withdrawal.
type OutflowAnswer struct {
	Key       string  `json:"key"`
	FlowKrw   float64 `json:"flowKrw"`
	Use       string  `json:"use"`
	AmountKrw int64   `json:"amountKrw"`
	Category  string  `json:"category,omitempty"`
	Memo      string  `json:"memo,omitempty"`
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

// RealizedLine is everything sold of one holding since the principal's start.
type RealizedLine struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Symbol    string  `json:"symbol"`
	Sales     int     `json:"sales"`
	ProfitKrw float64 `json:"profitKrw"`
	CostKrw   float64 `json:"costKrw"`
	Held      bool    `json:"held"`
}

// TaxBasket is one year's capital-gains estimate: overseas shares or coins.
type TaxBasket struct {
	Kind         string  `json:"kind"` // overseas | coin
	GainKrw      float64 `json:"gainKrw"`
	DeductionKrw float64 `json:"deductionKrw"`
	TaxKrw       float64 `json:"taxKrw"`
	InForce      bool    `json:"inForce"`
}

// Realized is what has already been sold: per holding, and this year's tax.
type Realized struct {
	Since    string         `json:"since"`
	Missing  []string       `json:"missing"`
	Lines    []RealizedLine `json:"lines"`
	TotalKrw float64        `json:"totalKrw"`
	Tax      struct {
		Year    int         `json:"year"`
		Baskets []TaxBasket `json:"baskets"`
	} `json:"tax"`
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
	Realized   *Realized         `json:"realized"`
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
	return s.call(ctx, http.MethodGet, path, nil, out)
}

func (s *Store) call(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, payload)
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
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Outflows is every withdrawal since the principal's start and where it went.
func (s *Store) Outflows(ctx context.Context) ([]Outflow, error) {
	var payload struct {
		Outflows []Outflow `json:"outflows"`
	}
	err := s.get(ctx, "/outflows", &payload)
	return payload.Outflows, err
}

// AnswerOutflow writes where part of a withdrawal went.
func (s *Store) AnswerOutflow(ctx context.Context, a OutflowAnswer) error {
	return s.call(ctx, http.MethodPost, "/outflows/answer", a, nil)
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
