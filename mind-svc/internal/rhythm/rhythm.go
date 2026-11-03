// Package rhythm is when the operator answers. Of the cards mind rang with,
// how many were answered within answerWithin, by the hour of the day they
// rang - from the intents, which keep when each was raised and answered.
//
// The gate's cost of a buzz was the same at every hour, raised only by how
// many cards the day had seen and by what context-svc says (driving, a
// meeting). An hour the operator does not answer at is a buzz spent for
// nothing: Slow says which, and the gate holds what is worth less for the
// evening then, as it does while they drive. Evening is the hour of the
// evening they answer most at - where the digest goes, offered on the
// notification settings rather than moved: the digest's time is theirs.
package rhythm

import (
	"fmt"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

const (
	// Window is how far back the cards are counted.
	Window = 60 * 24 * time.Hour
	// answerWithin is how soon an answer counts as one to the buzz: later,
	// it was found in the list, not heard.
	answerWithin = 2 * time.Hour
	// prior is how many cards' worth an hour's rate is pulled toward the
	// usual one: an hour with three cards says little alone.
	prior = 3.0
	// minCards and minHour are how many rung cards are needed at all, and
	// around an hour, before anything is said of it.
	minCards = 20
	minHour  = 6
	// slowShare: an hour answered at less than this share of the usual
	// rate is a slow one.
	slowShare = 0.5
)

// Hour is one hour of the day: the cards that rang around it (the hour and
// its two neighbours) and how many were answered in time.
type Hour struct {
	Hour     int     `json:"hour"`
	Rang     int     `json:"rang"`
	Answered int     `json:"answered"`
	Rate     float64 `json:"rate"` // pulled toward Overall by prior
	// Own is what was answered in time of the cards that rang in this
	// very hour: between neighbours that read the same, the hour itself.
	Own int `json:"own"`
}

// Profile is the hours of the day by how the operator answers at them.
type Profile struct {
	Cards    int      `json:"cards"`
	Answered int      `json:"answered"`
	Overall  float64  `json:"overall"`
	Hours    [24]Hour `json:"hours"`
	Since    time.Time
	loc      *time.Location
}

// Rang is whether an intent was a card mind rang with: its own (not one the
// conversation raised and mind keeps the ledger of), pushed now rather than
// kept for the evening.
func Rang(in model.Intent) bool {
	return in.Card && in.Holder == "" && in.Tier == "now"
}

// Answered is whether the operator answered it within answerWithin - on the
// card, or in the conversation.
func Answered(in model.Intent) bool {
	if in.DecidedAt == nil || in.DecidedBy == "policy" || in.DecidedBy == "" {
		return false
	}
	d := in.DecidedAt.Sub(in.CreatedAt)
	return d >= 0 && d <= answerWithin
}

// Build is the profile of the intents raised since now-Window.
func Build(ins []model.Intent, loc *time.Location, now time.Time) Profile {
	p := Profile{Since: now.Add(-Window), loc: loc}
	var rang, answered [24]int
	for _, in := range ins {
		if !Rang(in) || in.CreatedAt.Before(p.Since) || now.Sub(in.CreatedAt) < answerWithin {
			continue // too old, or not yet had its chance to be answered
		}
		h := in.CreatedAt.In(loc).Hour()
		rang[h]++
		p.Cards++
		if Answered(in) {
			answered[h]++
			p.Answered++
		}
	}
	if p.Cards > 0 {
		p.Overall = float64(p.Answered) / float64(p.Cards)
	}
	for h := range 24 {
		hr := Hour{Hour: h}
		for _, d := range []int{-1, 0, 1} {
			i := (h + d + 24) % 24
			hr.Rang += rang[i]
			hr.Answered += answered[i]
		}
		hr.Rate = (float64(hr.Answered) + prior*p.Overall) / (float64(hr.Rang) + prior)
		hr.Own = answered[h]
		p.Hours[h] = hr
	}
	return p
}

// better is whether a is the evening hour to prefer to b: the higher rate,
// then the more answered in the hour itself, then the nearer the usual 21:00.
func better(a, b Hour) bool {
	const same = 1e-9
	switch {
	case a.Rate > b.Rate+same:
		return true
	case a.Rate < b.Rate-same:
		return false
	case a.Own != b.Own:
		return a.Own > b.Own
	}
	return abs(a.Hour-21) < abs(b.Hour-21)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Known is whether there are cards enough to say anything.
func (p Profile) Known() bool { return p.Cards >= minCards }

// Slow is whether the operator is usually slow to answer at this time, and
// the reason the gate gives: cards enough around the hour, answered at less
// than half the usual rate.
func (p Profile) Slow(at time.Time) (bool, string) {
	if !p.Known() || p.loc == nil {
		return false, ""
	}
	h := p.Hours[at.In(p.loc).Hour()]
	if h.Rang < minHour || h.Rate >= slowShare*p.Overall {
		return false, ""
	}
	return true, fmt.Sprintf("평소 %d시 전후엔 카드에 답이 늦어서(최근 응답 %.0f%%, 평소 %.0f%%)", h.Hour, h.Rate*100, p.Overall*100)
}

// Evening is the hour from 18 to 23 the operator answers most at, with its
// rate - where the evening digest would be heard - when there are cards
// enough to say.
func (p Profile) Evening() (Hour, bool) {
	if !p.Known() {
		return Hour{}, false
	}
	var best Hour
	found := false
	for h := 18; h <= 23; h++ {
		hr := p.Hours[h]
		if hr.Rang < minHour {
			continue
		}
		if !found || better(hr, best) {
			best, found = hr, true
		}
	}
	return best, found
}
