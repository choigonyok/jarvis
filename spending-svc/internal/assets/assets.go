// Package assets brings into the book what was paid out of the investment
// accounts.
//
// A card alert never sees money taken out of Upbit or KIS and then spent by
// transfer or in cash. assets-svc pairs each withdrawal with a deposit into
// another of the accounts; what nothing pairs is put to the operator on a
// card, and what they say was spent is read from GET /outflows here and kept
// as a row of its own (source 'asset'). Paying a card bill is not spent here
// - those charges are in the book already, from their alerts.
//
// The list is read whole every time and the rows made to match it, the way
// a cursor would not: an answer changed or asked again takes its row out.
package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/spending-svc/internal/store"
)

// Part and Outflow are the slice of assets-svc's GET /outflows read here.
type Part struct {
	Use       string  `json:"use"`
	AmountKrw float64 `json:"amountKrw"`
	Category  string  `json:"category"`
	Memo      string  `json:"memo"`
	DecidedAt string  `json:"decidedAt"`
}

type Outflow struct {
	Key   string `json:"key"`
	Date  string `json:"date"`
	Venue string `json:"venue"`
	Parts []Part `json:"parts"`
}

var venueName = map[string]string{"upbit": "업비트", "kis": "한국투자증권", "gold": "금현물 계좌"}

// Spends is what the book should hold: one row per withdrawal a person said
// was (partly) spent, at what they said it was for ("월세") or, saying
// nothing, the account it left. A row is named for the answer, so answering
// again replaces it. The day is the withdrawal's - when the money left the
// pot; a hand-kept day has no hour, so it sits at noon. The memo is left to
// the 가계부 screen, as on any row.
func Spends(outflows []Outflow, loc *time.Location) []store.AssetSpend {
	var out []store.AssetSpend
	for _, o := range outflows {
		day, err := time.ParseInLocation("2006-01-02", o.Date, loc)
		if err != nil {
			continue
		}
		account := venueName[o.Venue]
		if account == "" {
			account = o.Venue
		}
		for _, p := range o.Parts {
			if p.Use != "spend" || p.DecidedAt == "" || p.AmountKrw < 1 {
				continue
			}
			merchant := strings.TrimSpace(p.Memo)
			if merchant == "" {
				merchant = account + " 출금"
			}
			out = append(out, store.AssetSpend{
				Ref:        "asset:" + o.Key + "@" + p.DecidedAt,
				ApprovedAt: day.Add(12 * time.Hour),
				Issuer:     account,
				Merchant:   merchant,
				AmountKrw:  int64(math.Round(p.AmountKrw)),
				Category:   p.Category,
			})
		}
	}
	return out
}

type Feed struct {
	base     string
	token    string
	store    *store.Store
	interval time.Duration
	client   *http.Client
	log      *slog.Logger
	loc      *time.Location
	// Loops, when set, hears each sync for /health ("assets").
	Loops *health.Loops
}

func New(base, token string, st *store.Store, interval time.Duration, log *slog.Logger) *Feed {
	return &Feed{
		base:     strings.TrimRight(base, "/"),
		token:    token,
		store:    st,
		interval: interval,
		// A cold portfolio walks both brokerages first: seconds, not milliseconds.
		client: &http.Client{Timeout: 60 * time.Second},
		log:    log,
		loc:    time.Local,
	}
}

// Run reads the list now and every interval. A failed read changes nothing:
// rows are only taken out on a list that came back whole.
func (f *Feed) Run(ctx context.Context) {
	tick := time.NewTicker(f.interval)
	defer tick.Stop()
	for {
		if err := f.Sync(ctx); err != nil && ctx.Err() == nil {
			f.log.Warn("투자 계좌에서 쓴 돈을 읽지 못했습니다", "err", err)
			f.Loops.Fail("assets", err)
		} else if err == nil {
			f.Loops.OK("assets")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (f *Feed) Sync(ctx context.Context) error {
	var payload struct {
		Outflows *[]Outflow `json:"outflows"`
	}
	if err := f.get(ctx, "/outflows", &payload); err != nil {
		return err
	}
	if payload.Outflows == nil {
		return errors.New("자산 서비스가 출금 목록 없이 답했습니다")
	}
	added, removed, err := f.store.SyncAssetSpends(ctx, Spends(*payload.Outflows, f.loc))
	if err != nil {
		return err
	}
	if added > 0 || removed > 0 {
		f.log.Info("투자 계좌에서 쓴 돈을 맞췄습니다", "added", added, "removed", removed)
	}
	return nil
}

func (f *Feed) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.base+path, nil)
	if err != nil {
		return err
	}
	if f.token != "" {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}
	res, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("자산 서비스에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("자산 서비스가 %s 로 답했습니다: %s", res.Status, strings.TrimSpace(string(raw)))
	}
	return json.NewDecoder(res.Body).Decode(out)
}
