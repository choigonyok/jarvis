package claudecode

import (
	"encoding/json"
	"slices"
	"time"
	"unicode/utf8"
)

// requestUsage is one API request's tokens, as the CLI reports them on the
// assistant lines of its stream (message.usage) and, summed, on its result
// line (usage). What the subscription is charged for is this, by the API's
// weights: a cache read costs 0.1 of an input token, a write 1.25 (five
// minutes) or 2 (an hour).
type requestUsage struct {
	Input      int `json:"input_tokens"`
	CacheRead  int `json:"cache_read_input_tokens"`
	CacheWrite int `json:"cache_creation_input_tokens"`
	Output     int `json:"output_tokens"`
	Creation   struct {
		FiveMin int `json:"ephemeral_5m_input_tokens"`
		OneHour int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// context is everything the request sent the model: what it read from the
// cache, wrote to it and paid for in full.
func (u requestUsage) context() int { return u.Input + u.CacheRead + u.CacheWrite }

func (u *requestUsage) add(o requestUsage) {
	u.Input += o.Input
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Output += o.Output
	u.Creation.FiveMin += o.Creation.FiveMin
	u.Creation.OneHour += o.Creation.OneHour
}

// tally adds up one run of the CLI: each request once - a response that
// arrives as several assistant lines, one per content block, carries the same
// message id on each - and the totals the result line gives.
type tally struct {
	ids  []string
	byID map[string]requestUsage
	// From the result line.
	total    *requestUsage
	models   []string
	costUSD  float64
	numTurns int
	// When the run's process started, and when it showed itself: its session
	// opened (system init), its first word to the operator appeared, its
	// result came. What a turn costs before the model says anything is the
	// CLI starting, reading its settings and dialling every MCP server - none
	// of it in the token counts. The CLI's own durations are on the result.
	started, initAt, firstAt, doneAt time.Time
	durationMS, apiMS                int64
}

// begin marks the run's process started.
func (t *tally) begin(at time.Time) { t.started = at }

// clocked notes when the run showed what ev is, at `at`.
func (t *tally) clocked(ev event, at time.Time) {
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" && t.initAt.IsZero() {
			t.initAt = at
		}
	case "stream_event":
		if ev.ParentToolUseID != "" || !t.firstAt.IsZero() || len(ev.Event) == 0 {
			return
		}
		var se streamEvent
		if json.Unmarshal(ev.Event, &se) == nil && se.Type == "content_block_delta" && se.Delta.Type == "text_delta" && se.Delta.Text != "" {
			t.firstAt = at
		}
	case "assistant":
		if t.firstAt.IsZero() && ev.ParentToolUseID == "" && !ev.IsAPIErrorMessage && hasText(ev.Message) {
			t.firstAt = at
		}
	case "result":
		if t.doneAt.IsZero() {
			t.doneAt = at
		}
		t.durationMS, t.apiMS = ev.DurationMS, ev.DurationAPIMS
	}
}

// since is how long after the start `at` was, in milliseconds; -1 when it
// did not happen.
func (t *tally) since(at time.Time) int64 {
	if t.started.IsZero() || at.IsZero() {
		return -1
	}
	return at.Sub(t.started).Milliseconds()
}

func (t *tally) observe(ev event) {
	switch ev.Type {
	case "assistant":
		if ev.IsAPIErrorMessage || len(ev.Message) == 0 {
			return
		}
		var m struct {
			ID    string        `json:"id"`
			Usage *requestUsage `json:"usage"`
		}
		if json.Unmarshal(ev.Message, &m) != nil || m.ID == "" || m.Usage == nil {
			return
		}
		if t.byID == nil {
			t.byID = map[string]requestUsage{}
		}
		prev, seen := t.byID[m.ID]
		if !seen {
			t.ids = append(t.ids, m.ID)
		}
		// The lines of one response repeat its input; the output count is
		// final on the last of them.
		if !seen || m.Usage.Output >= prev.Output {
			t.byID[m.ID] = *m.Usage
		}
	case "result":
		var total requestUsage
		if len(ev.Usage) > 0 && json.Unmarshal(ev.Usage, &total) == nil {
			t.total = &total
		}
		var models map[string]json.RawMessage
		if json.Unmarshal(ev.ModelUsage, &models) == nil {
			t.models = t.models[:0]
			for name := range models {
				t.models = append(t.models, name)
			}
			slices.Sort(t.models)
		}
		t.costUSD, t.numTurns = ev.TotalCostUSD, ev.NumTurns
	}
}

func (t *tally) requests() int { return len(t.ids) }

func (t *tally) first() requestUsage {
	if len(t.ids) == 0 {
		return requestUsage{}
	}
	return t.byID[t.ids[0]]
}

func (t *tally) last() requestUsage {
	if len(t.ids) == 0 {
		return requestUsage{}
	}
	return t.byID[t.ids[len(t.ids)-1]]
}

// sum is the run's totals: the result line's when it came, else the
// requests added up.
func (t *tally) sum() requestUsage {
	if t.total != nil {
		return *t.total
	}
	var s requestUsage
	for _, id := range t.ids {
		s.add(t.byID[id])
	}
	return s
}

// attrs are the log fields for one run: tokens by kind, how many requests,
// and how big the last one was (the context the next request starts from).
func (t *tally) attrs() []any {
	s := t.sum()
	return []any{
		"requests", t.requests(),
		"input", s.Input,
		"cache_read", s.CacheRead,
		"cache_write", s.CacheWrite,
		"cache_write_5m", s.Creation.FiveMin,
		"cache_write_1h", s.Creation.OneHour,
		"output", s.Output,
		"context", t.last().context(),
		"models", t.models,
		"turns", t.numTurns,
		"cost_usd", t.costUSD,
		// What the operator waits through: the process up to its session,
		// to the first word of the answer, to the end; and the CLI's own
		// account of the run and of the time spent waiting on the API.
		"startup_ms", t.since(t.initAt),
		"first_text_ms", t.since(t.firstAt),
		"total_ms", t.since(t.doneAt),
		"cli_ms", t.durationMS,
		"api_ms", t.apiMS,
	}
}

// estimateTokens is a rough upper bound on what text costs: a token per
// character outside ASCII (Korean, mostly), one per four ASCII bytes. Used
// only to take a session's first message out of its first request, which
// leaves what every request sends anyway.
func estimateTokens(s string) int {
	ascii, other := 0, 0
	for _, r := range s {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			other++
		}
	}
	return other + (ascii+3)/4
}
