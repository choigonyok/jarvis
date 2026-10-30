package sensor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// One reader for each source: what mind reads from a service into
// occurrences, it also hands to long-term memory as records - the facts
// memory-svc's collectors used to write after reading the same service
// again. The wording and the edge keys are the collectors', so the graph
// carries on where they left off.

// Recorder takes records to long-term memory (memory.HTTP).
type Recorder interface {
	Records(ctx context.Context, recs []model.Record) error
}

// What memory-svc takes of a record, in characters (RecordIn in
// memory-svc/app/main.py).
const (
	maxFact = 2000
	maxName = 300
)

// record queues r unless memory already has it as it is.
func (s *Structured) record(ctx context.Context, r model.Record) error {
	if s.Records == nil {
		return nil
	}
	// Longer than memory takes is cut to fit here rather than turned down
	// there: a job's summary ran its fact to 2,170 characters (2026-10-23).
	r.Fact = clip(r.Fact, maxFact)
	r.Target.Name = clip(r.Target.Name, maxName)
	if r.Source != nil {
		src := *r.Source
		src.Name = clip(src.Name, maxName)
		r.Source = &src
	}
	sum := sha256.Sum256([]byte(r.Relation + "\x00" + r.Fact + "\x00" + r.ValidAt + "\x00" + r.Target.Key))
	h := hex.EncodeToString(sum[:8])
	old, err := s.Store.RecordHash(ctx, r.Key)
	if err != nil || old == h {
		return err
	}
	s.inMu.Lock()
	defer s.inMu.Unlock()
	s.incoming = append(s.incoming, queued{r, h})
	return nil
}

type queued struct {
	rec  model.Record
	hash string
}

// flush sends what is queued; what memory did not take stays queued for
// the next round. A batch memory turns down as it is goes again a record at
// a time, so that the one record it will not take does not hold back the
// rest - one job's fact kept every record out of memory for three days
// (2026-10-23~26).
func (s *Structured) flush(ctx context.Context) error {
	s.inMu.Lock()
	s.queued, s.incoming = append(s.queued, s.incoming...), nil
	s.inMu.Unlock()
	for len(s.queued) > 0 {
		n := min(len(s.queued), 100)
		batch := make([]model.Record, n)
		for i, q := range s.queued[:n] {
			batch[i] = q.rec
		}
		err := s.Records.Records(ctx, batch)
		if errors.Is(err, model.ErrRefused) {
			for range n {
				if err := s.flushOne(ctx); err != nil {
					return err
				}
			}
			continue
		}
		if err != nil {
			if len(s.queued) > 2000 {
				s.queued = s.queued[len(s.queued)-2000:] // the hashes bring the rest back
			}
			return fmt.Errorf("기억으로 보내지 못했습니다: %w", err)
		}
		if err := s.sent(ctx, s.queued[:n]); err != nil {
			return err
		}
		s.queued = s.queued[n:]
	}
	return nil
}

// flushOne sends the first record queued on its own. One memory turns down
// is let go: marked as sent, it is offered again only once it changes.
func (s *Structured) flushOne(ctx context.Context) error {
	q := s.queued[0]
	if err := s.Records.Records(ctx, []model.Record{q.rec}); errors.Is(err, model.ErrRefused) {
		if s.Out != nil {
			s.Out.Warn("기억이 받지 않는 기록을 건너뜁니다", "key", q.rec.Key, "err", err)
		}
	} else if err != nil {
		return fmt.Errorf("기억으로 보내지 못했습니다: %w", err)
	}
	if err := s.sent(ctx, s.queued[:1]); err != nil {
		return err
	}
	s.queued = s.queued[1:]
	return nil
}

// sent notes that memory has these records as they are.
func (s *Structured) sent(ctx context.Context, qs []queued) error {
	now := s.Now()
	for _, q := range qs {
		if err := s.Store.PutRecordHash(ctx, q.rec.Key, q.hash, now); err != nil {
			return err
		}
		if err := s.Store.PutFactRow(ctx, recordRow(q.rec, now)); err != nil {
			return err
		}
	}
	return nil
}

// recordRow is a record as a row of the facts table: memory's graph can be
// made again from there.
func recordRow(r model.Record, now time.Time) model.FactRow {
	subject := "self"
	if r.Source != nil {
		subject = r.Source.Key
	}
	row := model.FactRow{ID: "record:" + r.Key, Subject: subject, Predicate: r.Relation, Object: r.Target.Key, Text: r.Fact,
		From: now, P: 1, Origin: model.FactRecord}
	if at, err := time.Parse(time.RFC3339, r.ValidAt); err == nil {
		row.From = at
	}
	body, err := json.Marshal(struct {
		ID     string `json:"id"`
		Origin string `json:"origin"`
		model.Record
	}{row.ID, model.FactRecord, r})
	if err == nil {
		row.Body = body
	}
	return row
}

// clip is s cut to at most n characters, the cut marked.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// day is a date's midnight in Seoul, as a record's validAt.
func (s *Structured) day(date string) string {
	t, err := time.ParseInLocation("2006-01-02", date, s.Loc)
	if err != nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// wonText is a sum as the collectors wrote it: "12,000원", "-3,500원".
func wonText(n int64) string {
	if n < 0 {
		return "-" + won(-n) + "원"
	}
	return won(n) + "원"
}

// num is a number as Python prints it from JSON: 60, 62.5.
func num(f float64, places int) string {
	p := math.Pow(10, float64(places))
	return strconv.FormatFloat(math.Round(f*p)/p, 'f', -1, 64)
}

// --- spending ---------------------------------------------------------------

// SpendingRecord is a charge or a cancellation as memory keeps it.
func (s *Structured) SpendingRecord(t tx) model.Record {
	verb := "결제"
	if t.Kind == "cancel" {
		verb = "취소"
	}
	var items []string
	for _, it := range t.Items {
		items = append(items, fmt.Sprintf("%s×%d", it.Name, max(it.Quantity, 1)))
	}
	cat := t.Category
	if cat == "" {
		cat = "기타"
	}
	fact := fmt.Sprintf("%s %s에서 %s %s (%s)", t.ApprovedAt.In(s.Loc).Format("2006-01-02 15:04"), t.Merchant, wonText(t.AmountKrw), verb, cat)
	if len(items) > 0 {
		fact += ": " + strings.Join(items, ", ")
	}
	if t.Memo != "" {
		fact += ". 메모: " + t.Memo
	}
	return model.Record{Key: fmt.Sprintf("spending:%d", t.ID), Relation: verb, Target: model.Node{Kind: "Merchant", Key: t.Merchant, Name: t.Merchant},
		Fact: fact, ValidAt: t.ApprovedAt.Format(time.RFC3339)}
}

// --- calendar ---------------------------------------------------------------

var whose = map[string]string{"partner": "상대(애인)의 일정", "shared": "함께하는 일정"}

// CalendarRecords is an entry, and its place when it has one. One day of a
// repeating entry is not one: the series is enough.
func (s *Structured) CalendarRecords(ev entry) []model.Record {
	if strings.Contains(ev.ID, "#") {
		return nil
	}
	when := ev.Date + " 종일"
	if ev.Start != "" {
		when = ev.Date + " " + ev.Start
		if ev.End != "" {
			when += "~" + ev.End
		}
	}
	if ev.EndDate != "" {
		when += " ~ " + ev.EndDate
	}
	fact := when + " " + ev.Title
	if ev.Place != "" {
		fact += " (장소: " + ev.Place + ")"
	}
	if w, ok := whose[ev.Owner]; ok {
		fact += " - " + w
	}
	if ev.Memo != "" {
		fact += ". 메모: " + ev.Memo
	}
	event := model.Node{Kind: "Event", Key: ev.ID, Name: ev.Title}
	out := []model.Record{{Key: "calendar:" + ev.ID, Relation: "일정", Target: event, Fact: fact, ValidAt: s.day(ev.Date)}}
	if ev.Place != "" {
		out = append(out, model.Record{Key: "calendar-place:" + ev.ID, Relation: "장소", Target: model.Node{Kind: "Place", Key: ev.Place, Name: ev.Place},
			Source: &event, Fact: fmt.Sprintf("%s '%s' 일정의 장소는 %s", ev.Date, ev.Title, ev.Place), ValidAt: s.day(ev.Date)})
	}
	return out
}

// --- workouts ---------------------------------------------------------------

// WorkoutRecords is a session's exercises: sets that count, and the best.
func (s *Structured) WorkoutRecords(w session) []model.Record {
	var out []model.Record
	for _, ex := range w.Exercises {
		var sets []set
		for _, x := range ex.Sets {
			// A set with no reps was ticked by mistake or left blank: not a set.
			if x.Done && !x.Warmup && x.Reps > 0 {
				sets = append(sets, x)
			}
		}
		if len(sets) == 0 {
			continue
		}
		top := sets[0]
		for _, x := range sets[1:] {
			if x.Weight > top.Weight || x.Weight == top.Weight && x.Reps > top.Reps {
				top = x
			}
		}
		best := fmt.Sprintf("맨몸 %d회", top.Reps)
		if top.Weight != 0 {
			best = fmt.Sprintf("%skg×%d회", num(top.Weight, 3), top.Reps)
		}
		out = append(out, model.Record{Key: fmt.Sprintf("workout:%s:%s", w.ID, ex.ID), Relation: "운동",
			Target: model.Node{Kind: "Exercise", Key: ex.Name, Name: ex.Name},
			Fact:   fmt.Sprintf("%s %s %d세트, 최고 %s", w.Date, ex.Name, len(sets), best), ValidAt: s.day(w.Date)})
	}
	return out
}

// --- jobs -------------------------------------------------------------------

var jobState = map[string]string{"active": "진행 중", "waiting": "승인 대기", "paused": "멈춤", "done": "끝남", "cancelled": "그만둠", "failed": "실패"}

func (s *Structured) JobRecord(j job) model.Record {
	state := jobState[j.State]
	if state == "" {
		state = j.State
	}
	fact := fmt.Sprintf("%s에 맡긴 작업 '%s': %s - 지금 %s", first(j.CreatedAt, 10), j.Title, j.Goal, state)
	if j.Summary != "" {
		fact += ". " + j.Summary
	}
	id := strconv.FormatInt(j.ID, 10)
	return model.Record{Key: "job:" + id, Relation: "맡긴 작업", Target: model.Node{Kind: "Job", Key: id, Name: j.Title}, Fact: fact, ValidAt: j.CreatedAt}
}

func first(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
