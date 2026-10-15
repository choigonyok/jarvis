package sensor

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// --- assets -----------------------------------------------------------------

type portfolio struct {
	Holdings []struct {
		ID       string  `json:"id"`
		Kind     string  `json:"kind"` // stock | crypto | gold
		Name     string  `json:"name"`
		Symbol   string  `json:"symbol"`
		Quantity float64 `json:"quantity"`
		Since    string  `json:"since"` // YYYY-MM-DD, "" before the history reaches
	} `json:"holdings"`
	Problems  []any `json:"problems"`
	Principal *struct {
		Flows []struct {
			ID        string `json:"id"`
			Venue     string `json:"venue"`
			AmountKrw int64  `json:"amountKrw"`
			Date      string `json:"date"`
			Memo      string `json:"memo"`
		} `json:"flows"`
	} `json:"principal"`
	Realized *struct {
		Sales []struct {
			ID        string  `json:"id"`
			Date      string  `json:"date"`
			Kind      string  `json:"kind"`
			Name      string  `json:"name"`
			Symbol    string  `json:"symbol"`
			Quantity  float64 `json:"quantity"`
			ProfitKrw float64 `json:"profitKrw"`
		} `json:"sales"`
	} `json:"realized"`
}

var venues = map[string]string{"upbit": "업비트", "kis": "한국투자증권", "gold": "금현물 계좌"}

// assetsEvery: the portfolio asks the exchanges, so it is read less often
// than the rest.
const assetsEvery = 5 * time.Minute

// Assets reads what is held (and since when), money moved in and out, and
// sales - not prices, which change every minute and are read live. A
// holding gone from the answer is sold only when every venue answered: a
// venue that failed to load leaves its holdings out.
func (s *Structured) Assets(ctx context.Context) error {
	if s.AssetsURL == "" || s.Now().Sub(s.assetsAt) < assetsEvery {
		return nil
	}
	var p portfolio
	if err := s.get(ctx, s.AssetsURL+"/portfolio", &p); err != nil {
		return err
	}
	s.assetsAt = s.Now()
	now := s.Now().In(s.Loc)
	held, err := s.Store.Occurrences(ctx, time.Time{}, now.Add(time.Minute), "asset")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, x := range p.Holdings {
		unit := map[string]string{"stock": "주", "gold": "g"}[x.Kind]
		id := "asset:holding:" + x.ID
		seen[id] = true
		when := "기록 시작(5월 1일) 전부터"
		at, valid := now, ""
		if x.Since != "" {
			when, valid = x.Since+"부터", s.day(x.Since)
			if t, err := time.ParseInLocation("2006-01-02", x.Since, s.Loc); err == nil {
				at = t
			}
		} else if old, err := s.Store.Occurrence(ctx, id); err == nil {
			at = old.At // first seen
		}
		fact := fmt.Sprintf("%s(%s)을 %s%s 보유 중 (%s)", x.Name, x.Symbol, num(x.Quantity, 4), unit, when)
		if err := s.observe(ctx, model.Occurrence{ID: id, Kind: "asset", At: at, Title: x.Name, Things: []string{x.Symbol},
			Class: "보유", Ref: id, Quote: fact}); err != nil {
			return err
		}
		if err := s.record(ctx, model.Record{Key: "holding:" + x.ID, Relation: "보유", Target: model.Node{Kind: "Asset", Key: x.Symbol, Name: x.Name},
			Fact: fact, ValidAt: valid}); err != nil {
			return err
		}
	}
	if len(p.Problems) == 0 {
		for _, o := range held {
			if o.Class != "보유" || o.Until != nil || seen[o.ID] {
				continue
			}
			symbol := ""
			if len(o.Things) > 0 {
				symbol = o.Things[0]
			}
			end := now
			o.Until, o.Class = &end, "처분"
			if err := s.observe(ctx, o); err != nil {
				return err
			}
			hid := o.ID[len("asset:holding:"):]
			if err := s.record(ctx, model.Record{Key: fmt.Sprintf("holding-sold:%s:%s", hid, now.Format("2006-01-02")), Relation: "처분",
				Target: model.Node{Kind: "Asset", Key: symbol, Name: o.Title},
				Fact:   fmt.Sprintf("%s(%s)을 %s에 모두 처분", o.Title, symbol, now.Format("2006-01-02")), ValidAt: now.Format(time.RFC3339)}); err != nil {
				return err
			}
		}
	}
	if p.Principal != nil {
		for _, f := range p.Principal.Flows {
			verb := "입금"
			if f.AmountKrw < 0 {
				verb = "출금"
			}
			venue := venues[f.Venue]
			if venue == "" {
				venue = f.Venue
			}
			fact := fmt.Sprintf("%s %s에 %s %s", f.Date, venue, wonText(abs(f.AmountKrw)), verb)
			if f.Memo != "" {
				fact += " (" + f.Memo + ")"
			}
			at, _ := time.ParseInLocation("2006-01-02", f.Date, s.Loc)
			id := "asset:flow:" + f.ID
			if err := s.observe(ctx, model.Occurrence{ID: id, Kind: "asset", At: at, Title: venue, Class: verb, Amount: abs(f.AmountKrw), Ref: id, Quote: fact}); err != nil {
				return err
			}
			if err := s.record(ctx, model.Record{Key: "flow:" + f.ID, Relation: verb, Target: model.Node{Kind: "Account", Key: f.Venue, Name: venue},
				Fact: fact, ValidAt: s.day(f.Date)}); err != nil {
				return err
			}
		}
	}
	if p.Realized != nil {
		for _, x := range p.Realized.Sales {
			unit := ""
			if x.Kind == "stock" {
				unit = "주"
			}
			profit := int64(math.Round(x.ProfitKrw))
			fact := fmt.Sprintf("%s %s %s%s 매도, 실현손익 %s", x.Date, x.Name, num(x.Quantity, 8), unit, wonText(profit))
			at, _ := time.ParseInLocation("2006-01-02", x.Date, s.Loc)
			id := "asset:sale:" + x.ID
			if err := s.observe(ctx, model.Occurrence{ID: id, Kind: "asset", At: at, Title: x.Name, Things: []string{x.Symbol}, Class: "매도",
				Amount: profit, Ref: id, Quote: fact}); err != nil {
				return err
			}
			if err := s.record(ctx, model.Record{Key: "sale:" + x.ID, Relation: "매도", Target: model.Node{Kind: "Asset", Key: x.Symbol, Name: x.Name},
				Fact: fact, ValidAt: s.day(x.Date)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// --- jobs -------------------------------------------------------------------

type job struct {
	ID         int64      `json:"id"`
	Title      string     `json:"title"`
	Goal       string     `json:"goal"`
	State      string     `json:"state"`
	Summary    string     `json:"summary"`
	CreatedAt  string     `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

// Jobs reads the work handed to the background browser: each job an
// occurrence from when it was given to when it finished.
func (s *Structured) Jobs(ctx context.Context) error {
	if s.JobsURL == "" {
		return nil
	}
	var out struct {
		Jobs []job `json:"jobs"`
	}
	if err := s.get(ctx, s.JobsURL+"/jobs", &out); err != nil {
		return err
	}
	for _, j := range out.Jobs {
		at, err := time.Parse(time.RFC3339, j.CreatedAt)
		if err != nil {
			continue
		}
		id := fmt.Sprintf("job:%d", j.ID)
		if err := s.observe(ctx, model.Occurrence{ID: id, Kind: "job", At: at, Until: j.FinishedAt, Title: j.Title, Class: j.State,
			Ref: id, Quote: first(j.Goal, 200)}); err != nil {
			return err
		}
		if err := s.record(ctx, s.JobRecord(j)); err != nil {
			return err
		}
	}
	return nil
}
