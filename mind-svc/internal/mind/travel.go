package mind

import (
	"context"
	"fmt"
	"strings"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
)

// How long the way between two places takes is not looked up on a map: it
// is what the stays in regions measured (context-svc /travel) - left the
// office at 18:20, home at 19:05. Each way taken often enough becomes a
// travel note, which a late warning and a plan's guard read.

// Route is one way and how long it usually takes.
type Route struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Trips   int    `json:"trips"`
	Minutes int    `json:"minutes"`
	Leave   string `json:"leave"`
}

// Travel is where routes come from (context-svc); nil: not known.
type Travel interface {
	Routes(ctx context.Context) ([]Route, error)
}

// TravelHTTP is context-svc.
type TravelHTTP struct {
	URL, Token string
}

func (t TravelHTTP) Routes(ctx context.Context) ([]Route, error) {
	var out struct {
		Routes []Route `json:"routes"`
	}
	return out.Routes, getJSON(ctx, strings.TrimRight(t.URL, "/")+"/travel?days=60", t.Token, &out)
}

// StaticTravel is fixed routes (evaluation).
type StaticTravel []Route

func (s StaticTravel) Routes(context.Context) ([]Route, error) { return s, nil }

// routesForNotes: trips a way needs before it is a note.
const routesForNotes = 3

// travelNotes keeps a note for every way taken often enough: "강남에서
// 망원동까지 보통 45분 (최근 6번, 보통 18:20 출발)".
func (m *Mind) travelNotes(ctx context.Context) error {
	if m.Opts.Travel == nil {
		return nil
	}
	routes, err := m.Opts.Travel.Routes(ctx)
	if err != nil {
		return err
	}
	for _, r := range routes {
		if r.Trips < routesForNotes || r.Minutes <= 0 || r.From == "" || r.To == "" {
			continue
		}
		n := model.Note{
			ID:    self.ID("travel", r.From, r.To),
			Text:  fmt.Sprintf("%s에서 %s까지 보통 %d분 (최근 %d번, 보통 %s 출발)", r.From, r.To, r.Minutes, r.Trips, r.Leave),
			Scope: "이동", Confidence: float64(r.Trips) / float64(r.Trips+1),
			Rule: &model.Rule{Kind: "travel", From: r.From, To: r.To, Minutes: r.Minutes, Support: r.Trips},
		}
		if _, _, err := m.Self.Hold(ctx, n, "영역 체류 기록에서 잰 이동 시간"); err != nil {
			return err
		}
	}
	return nil
}
