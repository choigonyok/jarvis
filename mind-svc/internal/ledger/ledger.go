// Package ledger builds the result ledger: every judgement the assistant made
// with what came of it. It reads, it does not decide - nothing acts on it yet.
// It is what the constants are measured with and what the engine will calibrate
// on.
//
// It is rebuilt from the intents, the predictions and the occurrences, so it
// can be deleted and made again. A prediction is scored here from what the
// world's record says, not taken from how it was scored at its deadline: a
// payment that reached the assistant late (card alerts stopped from 10/28 to
// 10/30 while the KakaoTalk app was logged out) did happen, and scoring it as
// a miss teaches the engine that the operator stopped eating.
//
// Silence is held to account the same way: what the assistant held back and
// the operator then did by hand is marked Manual. Without that, a judgement
// that stays quiet can never be wrong.
package ledger

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/backtest"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
)

// A card nobody answered for this long was ignored, which is not refused.
// An action done and told stands once this long has gone without an undo.
const (
	ignoredAfter = 7 * 24 * time.Hour
	keptAfter    = 24 * time.Hour
	// manualWithin is how long after a held-back purchase buying it by hand
	// still counts as that purchase.
	manualWithin = 14 * 24 * time.Hour
)

// Inputs is what the ledger is built from.
type Inputs struct {
	Intents     []model.Intent
	Predictions []model.Prediction
	Occurrences []model.Occurrence
	// Thing is an item's class by common sense, as the predictor reads it.
	Thing func(name string) string
	Now   time.Time
	Loc   *time.Location
}

// Build is the ledger, in the order things were judged.
func Build(in Inputs) []model.Outcome {
	if in.Loc == nil {
		in.Loc = time.UTC
	}
	idx := index(in.Occurrences, in.Thing)
	done := doneThrough(in.Intents)
	out := make([]model.Outcome, 0, len(in.Intents)+len(in.Predictions))
	for _, it := range in.Intents {
		out = append(out, ofIntent(it, in, idx, done))
	}
	for _, pr := range in.Predictions {
		out = append(out, ofPrediction(pr, in.Now, idx))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID < out[j].ID
		}
		return out[i].At.Before(out[j].At)
	})
	return out
}

// occurrences keeps what happened under each name it happened by.
type occurrences struct {
	byKey map[string][]time.Time
	all   []model.Occurrence
	thing func(string) string
}

func index(list []model.Occurrence, thing func(string) string) occurrences {
	idx := occurrences{byKey: map[string][]time.Time{}, all: list, thing: thing}
	for _, o := range list {
		keys := predict.Keys(o, thing)
		if o.Kind == "purchase.item" && thing != nil {
			if c := thing(o.Title); c != "" {
				keys = append(keys, "purchase.item:"+c)
			}
		}
		for _, k := range keys {
			idx.byKey[k] = append(idx.byKey[k], o.At)
		}
	}
	return idx
}

func (x occurrences) happened(key string, from, to time.Time) bool {
	return slices.ContainsFunc(x.byKey[key], func(at time.Time) bool { return !at.Before(from) && !at.After(to) })
}

func ofIntent(it model.Intent, in Inputs, idx occurrences, done map[string]bool) model.Outcome {
	o := model.Outcome{ID: "intent:" + it.ID, Skill: it.Skill, Context: it.ContextKey(), Kind: it.Kind,
		Rule: it.Reason, P: it.Confidence, At: it.CreatedAt, ResolvedAt: it.DecidedAt}
	switch {
	case it.Kind == "info":
		// Only said: a brief, a reminder. There is nothing to get wrong.
		o.Source, o.Result = "notice", model.ResultSent
	case it.Decision == model.Ask:
		o.Source = "card"
		o.Result = cardResult(it, in.Now)
	case it.Decision == model.Notify:
		o.Source = "told"
		switch {
		case it.Status == model.Undone:
			o.Result = model.ResultUndone
		case it.Status == model.Executed && in.Now.Sub(it.CreatedAt) >= keptAfter:
			o.Result = model.ResultKept
		default:
			o.Result = model.ResultOpen
		}
	case it.Status == model.Dropped:
		o.Source, o.Result = "quiet", model.ResultDropped
	default:
		o.Source, o.Result = "notice", model.ResultSent
	}
	switch o.Result {
	case model.ResultRejected, model.ResultIgnored, model.ResultDropped:
		o.Manual = !done[it.EntityID] && doneByHand(it, idx, in.Loc)
	}
	return o
}

// corrected is whether a form was approved with its answer changed: the card
// went through, and what the assistant said was wrong all the same.
func corrected(it model.Intent) bool {
	var r struct {
		Corrected bool `json:"corrected"`
	}
	return json.Unmarshal(it.Result, &r) == nil && r.Corrected
}

// doneThrough is the plans an approved card acted on: an entry there came
// through the assistant, whichever card of the plan it was.
func doneThrough(intents []model.Intent) map[string]bool {
	done := map[string]bool{}
	for _, it := range intents {
		if it.EntityID != "" && it.Status == model.Executed && it.DecidedBy == "user" {
			done[it.EntityID] = true
		}
	}
	return done
}

func cardResult(it model.Intent, now time.Time) string {
	switch it.Status {
	case model.Executed, model.Failed:
		// Failed is the action's: the operator did say yes.
		if it.DecidedBy == "user" {
			if corrected(it) {
				return model.ResultCorrected
			}
			return model.ResultApproved
		}
		return model.ResultKept
	case model.Rejected:
		return model.ResultRejected
	case model.Undone:
		return model.ResultUndone
	case model.Superseded:
		switch {
		case it.Covered():
			return model.ResultCovered
		case it.DecidedBy == "user" && it.Error == "":
			return model.ResultSuperseded
		case strings.Contains(it.Error, "답이 없어"):
			return model.ResultIgnored
		}
		return model.ResultSuperseded
	case model.Pending:
		if now.Sub(it.CreatedAt) >= ignoredAfter {
			return model.ResultIgnored
		}
	}
	return model.ResultOpen
}

// doneByHand looks for what a held-back action would have done. It can tell
// for what leaves a record: a calendar entry, a purchase.
func doneByHand(it model.Intent, idx occurrences, loc *time.Location) bool {
	switch it.Kind {
	case "calendar.create":
		var a model.CalendarAction
		if json.Unmarshal(it.Payload, &a) != nil || a.Event.Date == "" {
			return false
		}
		want := model.State{Title: a.Event.Title, Date: a.Event.Date, Start: a.Event.Start}
		return slices.ContainsFunc(idx.all, func(o model.Occurrence) bool {
			if o.Kind != "calendar" {
				return false
			}
			at := o.At.In(loc)
			return model.SameEvent(want, model.State{Title: o.Title, Date: at.Format("2006-01-02"), Start: at.Format("15:04"), People: o.People})
		})
	case "cart.add":
		var c struct {
			Item  string `json:"item"`
			Class string `json:"class"`
		}
		if json.Unmarshal(it.Payload, &c) != nil {
			return false
		}
		until := it.CreatedAt.Add(manualWithin)
		return slices.ContainsFunc(idx.all, func(o model.Occurrence) bool {
			if o.Kind != "purchase.item" || o.At.Before(it.CreatedAt) || o.At.After(until) {
				return false
			}
			if c.Item != "" && strings.Contains(o.Title, c.Item) {
				return true
			}
			return c.Class != "" && (o.Class == c.Class || idx.thing != nil && idx.thing(o.Title) == c.Class)
		})
	}
	return false
}

func ofPrediction(pr model.Prediction, now time.Time, idx occurrences) model.Outcome {
	o := model.Outcome{ID: "prediction:" + pr.ID, Source: "prediction", Kind: "prediction", Skill: "predict",
		Context: keyHead(pr), Rule: pr.Key, P: pr.Prob, At: pr.CreatedAt, ResolvedAt: pr.ScoredAt}
	if pr.Base > 0 {
		base := pr.Base
		o.Base = &base
	}
	if now.Before(pr.Due) {
		o.Result = model.ResultOpen
		return o
	}
	happened, known := false, false
	if pr.Key != "" {
		from := pr.Due.Add(-24 * time.Hour)
		if pr.From != nil {
			from = *pr.From
		}
		happened, known = idx.happened(pr.Key, from, pr.Due), true
	}
	if !known {
		if pr.Outcome == nil {
			o.Result = model.ResultOpen
			return o
		}
		happened = *pr.Outcome
	}
	o.Result = model.ResultMiss
	if happened {
		o.Result = model.ResultHit
	}
	if pr.Outcome != nil && *pr.Outcome != happened {
		o.Live = pr.Outcome
	}
	return o
}

// keyHead is the broad name a prediction groups under: purchase, delivery.
func keyHead(pr model.Prediction) string {
	key := pr.Key
	if key == "" {
		key = pr.Target
	}
	if i := strings.IndexAny(key, "/:@"); i >= 0 {
		key = key[:i]
	}
	return key
}

// Cases are the ledger's scored rows as a backtest reads them.
func Cases(rows []model.Outcome) []backtest.Case {
	var out []backtest.Case
	for _, o := range rows {
		hit, ok := o.Scored()
		if !ok {
			continue
		}
		out = append(out, backtest.Case{Source: o.Source, Group: group(o), P: o.P, Hit: hit, Base: o.Base, At: o.At})
	}
	return out
}

func group(o model.Outcome) string {
	if o.Source == "prediction" {
		return o.Context
	}
	return o.Skill + "/" + o.Context
}

// Counts is what the ledger says beyond the scores.
type Counts struct {
	Rows, Scored, Open, Ignored, Quiet int
	// Manual is what was not done through the assistant and the operator
	// did by hand; ManualQuiet those the gate held back on its own.
	Manual, ManualQuiet int
	// Rescored is how many predictions the world's record disagrees with
	// their score at the deadline about.
	Rescored int
}

// Count adds the ledger up.
func Count(rows []model.Outcome) Counts {
	var c Counts
	c.Rows = len(rows)
	for _, o := range rows {
		if _, ok := o.Scored(); ok {
			c.Scored++
		}
		switch o.Result {
		case model.ResultOpen:
			c.Open++
		case model.ResultIgnored:
			c.Ignored++
		case model.ResultDropped:
			c.Quiet++
		}
		if o.Manual {
			c.Manual++
			if o.Source == "quiet" {
				c.ManualQuiet++
			}
		}
		if o.Live != nil {
			c.Rescored++
		}
	}
	return c
}
