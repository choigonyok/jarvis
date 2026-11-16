package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/choigonyok/jarvis/context-svc/internal/sleep"
)

// The nights' sleep (internal/sleep). A shortcut or Health Auto Export on the
// operator's phone posts what the health store says of sleep each morning
// (POST /sleep - the signal token, through the gateway like /signals), and
// the 수면 screen reads the nights back (GET /sleep - the stack's token: the
// operator's alone, the guest's token opens none of it).

const (
	// sleepBody is how much one send may be: Health Auto Export's carries
	// every metric its automation was given, and only the sleep is read.
	sleepBody = 8 << 20
	// sleepRaw is how much of a send is kept as it came, for as long as the
	// raw reports are kept; past it, what was read from it is kept instead.
	sleepRaw = 256 << 10
	// sleepSource is the source the health baseline's readings of a night go
	// in by, and are found by when the night comes again.
	sleepSource = "sleep"
)

// slept is what became of one sleep sent, as the reply says it: the day it
// belongs to, its window and length, and what it is now (sleep.Result).
type slept struct {
	Night         string       `json:"night"`
	BedAt         time.Time    `json:"bedAt"`
	WakeAt        time.Time    `json:"wakeAt"`
	AsleepMinutes int          `json:"asleepMinutes"`
	Result        sleep.Result `json:"result"`
}

// postSleep takes one send (sleep.Parse says the shapes), keeps it raw, makes
// its samples into sleeps, and merges each day's with the night kept for that
// day (sleep.Merge) - the same send twice leaves the same nights. A night that
// changed is passed on as the health baseline's health.sleep. The reply says
// what became of each sleep, and the values not read as any stage.
func (s *Server) postSleep(w http.ResponseWriter, r *http.Request) {
	if !s.signaller(r) {
		writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, sleepBody))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "본문이 너무 크거나 읽을 수 없습니다. Health Auto Export 라면 지표를 수면 분석만 두세요.")
		return
	}
	in, err := sleep.Parse(raw, s.loc)
	if errors.Is(err, sleep.ErrJSON) {
		writeErr(w, http.StatusBadRequest, "본문을 JSON 으로 읽지 못했습니다. 요청 본문을 JSON 으로 보내세요.")
		return
	}
	ctx := r.Context()
	source := cmp.Or(in.Source, "shortcut")
	if in.Format == "health-auto-export" {
		source = in.Format
	}
	if err := s.st.Raw(ctx, source, "sleep", s.now(), keepable(raw, in)); err != nil {
		s.fail(w, "원본을 남기지 못했습니다", err)
		return
	}
	sent := append(sleep.Sleeps(in.Samples, s.loc), in.Nights...)
	if err != nil || len(sent) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "수면으로 읽은 것이 없습니다. 시작·종료 시각과 수면 분석의 값(samples), 또는 start·end 가 필요합니다.",
			"unknown": orEmpty(in.Unknown), "skipped": in.Skipped,
		})
		return
	}

	// Each day's sleeps together, the days in order.
	byDay := map[string][]int{}
	for i, n := range sent {
		byDay[n.Night] = append(byDay[n.Night], i)
	}
	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	slices.Sort(days)

	s.sleepMu.Lock()
	defer s.sleepMu.Unlock()
	out := make([]slept, len(sent))
	var nights []sleep.Night
	for _, day := range days {
		idx := byDay[day]
		group := make([]sleep.Night, len(idx))
		for k, i := range idx {
			group[k] = sent[i]
		}
		kept, err := s.st.Night(ctx, day)
		if err != nil {
			s.fail(w, "수면을 읽지 못했습니다", err)
			return
		}
		now, results := sleep.Merge(kept, group)
		for k, i := range idx {
			n := sent[i].In(s.loc)
			out[i] = slept{Night: n.Night, BedAt: n.BedAt, WakeAt: n.WakeAt, AsleepMinutes: n.AsleepMinutes, Result: results[k]}
		}
		if now == nil || !slices.ContainsFunc(results, func(r sleep.Result) bool { return r == sleep.Saved || r == sleep.Napped }) {
			continue
		}
		if err := s.st.PutNight(ctx, *now); err != nil {
			s.fail(w, "수면을 남기지 못했습니다", err)
			return
		}
		if slices.Contains(results, sleep.Saved) {
			nights = append(nights, *now)
		}
	}
	for _, n := range nights {
		if err := s.feedSleep(ctx, n); err != nil {
			s.fail(w, "수면 시간을 기저선에 남기지 못했습니다", err)
			return
		}
	}
	if len(nights) > 0 {
		s.Check(ctx)
	}
	writeJSON(w, http.StatusOK, map[string]any{"nights": out, "unknown": orEmpty(in.Unknown), "skipped": in.Skipped})
}

// feedSleep passes a night on as the health baseline's sleep (health.sleep,
// hours asleep), the same way a shortcut's signal goes in: one reading a
// night, at its waking, so the baseline's day is the night's. What the night
// left there when it was sent before goes first.
func (s *Server) feedSleep(ctx context.Context, n sleep.Night) error {
	if err := s.st.DropReadings(ctx, "health.sleep", sleepSource, n.Night); err != nil {
		return err
	}
	hours := strconv.FormatFloat(float64(n.AsleepMinutes)/60, 'f', 2, 64)
	raw, err := json.Marshal(map[string]string{
		"source": sleepSource, "kind": "health.sleep", "state": hours, "at": n.WakeAt.Format(time.RFC3339), "night": n.Night,
	})
	if err != nil {
		return err
	}
	return s.signal(ctx, sleepSource, "health.sleep", hours, n.WakeAt, raw)
}

// keepable is the send as it is kept raw: as it came, or - past sleepRaw,
// an export carrying much more than sleep - what was read from it.
func keepable(raw []byte, in sleep.Input) []byte {
	if len(raw) <= sleepRaw {
		var probe json.RawMessage
		if json.Unmarshal(raw, &probe) == nil {
			return raw
		}
	}
	body, err := json.Marshal(map[string]any{
		"format": in.Format, "bytes": len(raw), "samples": in.Samples, "nights": in.Nights, "unknown": in.Unknown, "skipped": in.Skipped,
	})
	if err != nil {
		return []byte(`{}`)
	}
	return body
}

// getSleep is the nights of the days asked - from and to as YYYY-MM-DD or
// RFC 3339, Seoul's days, both kept; the 30 up to today when not said -
// newest first, with the week and the month up to `to` summed up, and how
// many days a night's timeline is kept (keepDays).
func (s *Server) getSleep(w http.ResponseWriter, r *http.Request) {
	today := s.now().In(s.loc).Format("2006-01-02")
	to, err1 := s.dayOf(r.URL.Query().Get("to"), today)
	from, err2 := s.dayOf(r.URL.Query().Get("from"), shiftDay(to, -29))
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "from, to 는 YYYY-MM-DD 나 RFC 3339 입니다.")
		return
	}
	if from > to || from < shiftDay(to, -400) {
		writeErr(w, http.StatusBadRequest, "from 은 to 와 같거나 앞이고, 400일 안이어야 합니다.")
		return
	}
	// The month summed up needs its 30 days, whatever was asked.
	all, err := s.st.Nights(r.Context(), min(from, shiftDay(to, -29)), to)
	if err != nil {
		s.fail(w, "수면을 읽지 못했습니다", err)
		return
	}
	nights := []sleep.Night{}
	for _, n := range all {
		if n.Night >= from {
			nights = append(nights, n.In(s.loc))
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "nights": nights,
		"summary": sleep.Summarize(all, to, s.loc), "keepDays": s.cfg.RawDays,
	})
}

// dayOf is a day as YYYY-MM-DD, from that or from a moment (RFC 3339) on
// Seoul's clock; def when empty.
func (s *Server) dayOf(v, def string) (string, error) {
	if v == "" {
		return def, nil
	}
	if d, err := time.ParseInLocation("2006-01-02", v, s.loc); err == nil {
		return d.Format("2006-01-02"), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return "", err
	}
	return t.In(s.loc).Format("2006-01-02"), nil
}

// shiftDay is a day n days on (YYYY-MM-DD).
func shiftDay(day string, n int) string {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return d.AddDate(0, 0, n).Format("2006-01-02")
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
