package sensor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// The records carry memory-svc's collectors' wording and edge keys, so the
// graph carries on where they left off.
func TestRecordsWording(t *testing.T) {
	s := &Structured{Loc: seoul}
	at := time.Date(2026, 10, 14, 19, 5, 0, 0, seoul)
	r := s.SpendingRecord(tx{ID: 812, Kind: "approval", AmountKrw: 32000, Merchant: "육전식당 강남점", Category: "식비", ApprovedAt: at, Memo: "회식",
		Items: []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Quantity  int    `json:"quantity"`
			AmountKrw int64  `json:"amountKrw"`
			Category  string `json:"category"`
		}{{ID: 1, Name: "육전", Quantity: 2}}})
	if r.Key != "spending:812" || r.Relation != "결제" || r.Fact != "2026-10-14 19:05 육전식당 강남점에서 32,000원 결제 (식비): 육전×2. 메모: 회식" {
		t.Errorf("spending: %+v", r)
	}
	if c := s.SpendingRecord(tx{ID: 9, Kind: "cancel", AmountKrw: -500, Merchant: "편의점", ApprovedAt: at}); c.Fact != "2026-10-14 19:05 편의점에서 -500원 취소 (기타)" {
		t.Errorf("cancel: %q", c.Fact)
	}
	recs := s.CalendarRecords(entry{ID: "e1", Title: "민수와 저녁", Date: "2026-10-20", Start: "19:00", End: "21:00", Place: "강남", Owner: "shared", Memo: "예약함"})
	if len(recs) != 2 || recs[0].Fact != "2026-10-20 19:00~21:00 민수와 저녁 (장소: 강남) - 함께하는 일정. 메모: 예약함" ||
		recs[1].Key != "calendar-place:e1" || recs[1].Source == nil || recs[1].Fact != "2026-10-20 '민수와 저녁' 일정의 장소는 강남" ||
		recs[0].ValidAt != "2026-10-20T00:00:00+09:00" {
		t.Errorf("calendar: %+v", recs)
	}
	if got := s.CalendarRecords(entry{ID: "s#2026-10-21", Date: "2026-10-21"}); got != nil {
		t.Errorf("a day of a series: %+v", got)
	}
	if got := s.CalendarRecords(entry{ID: "e2", Title: "제주", Date: "2026-11-01", EndDate: "2026-11-03"}); got[0].Fact != "2026-11-01 종일 ~ 2026-11-03 제주" {
		t.Errorf("all-day: %q", got[0].Fact)
	}
	w := session{ID: "w1", Date: "2026-10-14"}
	w.Exercises = append(w.Exercises, struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Sets []set  `json:"sets"`
	}{ID: "x1", Name: "스쿼트", Sets: []set{{Weight: 60, Reps: 5, Done: true}, {Weight: 62.5, Reps: 3, Done: true}, {Weight: 20, Reps: 10, Done: true, Warmup: true}, {Weight: 70, Done: true}}})
	w.Exercises = append(w.Exercises, struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Sets []set  `json:"sets"`
	}{ID: "x2", Name: "턱걸이", Sets: []set{{Reps: 8, Done: true}}})
	ws := s.WorkoutRecords(w)
	if len(ws) != 2 || ws[0].Key != "workout:w1:x1" || ws[0].Fact != "2026-10-14 스쿼트 2세트, 최고 62.5kg×3회" || ws[1].Fact != "2026-10-14 턱걸이 1세트, 최고 맨몸 8회" {
		t.Errorf("workout: %+v", ws)
	}
	j := s.JobRecord(job{ID: 3, Title: "장작 담기", Goal: "쿠팡 장바구니에 장작", State: "done", Summary: "담음", CreatedAt: "2026-10-12T09:00:00+09:00"})
	if j.Key != "job:3" || j.Fact != "2026-10-12에 맡긴 작업 '장작 담기': 쿠팡 장바구니에 장작 - 지금 끝남. 담음" {
		t.Errorf("job: %+v", j)
	}
	city, country := "Gapyeong", "South Korea"
	day := time.Date(2026, 10, 11, 0, 0, 0, 0, seoul)
	a := asset{ID: "a1"}
	a.ExifInfo = &struct {
		City             *string `json:"city"`
		State            *string `json:"state"`
		Country          *string `json:"country"`
		DateTimeOriginal *string `json:"dateTimeOriginal"`
	}{City: &city, Country: &country}
	a.People = append(a.People, struct {
		Name string `json:"name"`
	}{"지수"})
	ps := PhotoDayRecords(day, []asset{a, a})
	if len(ps) != 2 || ps[0].Fact != "2026-10-11 Gapyeong, South Korea에서 사진 2장 (함께: 지수)" || ps[1].Fact != "2026-10-11 지수와(과) 함께 찍은 사진 2장 (Gapyeong, South Korea)" {
		t.Errorf("photos: %+v", ps)
	}
	if ps := PhotoDayRecords(day, []asset{{ID: "b"}}); len(ps) != 1 || ps[0].Fact != "2026-10-11에 사진·영상 1개" {
		t.Errorf("bare photos: %+v", ps)
	}
}

type recorder struct{ got []model.Record }

func (r *recorder) Records(_ context.Context, recs []model.Record) error {
	r.got = append(r.got, recs...)
	return nil
}

// refuser is memory-svc's check on a record: one it will not take turns the
// whole request down (422). down is memory not answering at all.
type refuser struct {
	bad  string
	down bool
	got  []model.Record
}

func (r *refuser) Records(_ context.Context, recs []model.Record) error {
	if r.down {
		return errors.New("memory-svc POST /records: 502")
	}
	for _, x := range recs {
		if x.Key == r.bad || utf8.RuneCountInString(x.Fact) > maxFact || utf8.RuneCountInString(x.Target.Name) > maxName {
			return fmt.Errorf("%w: memory-svc POST /records: 422", model.ErrRefused)
		}
	}
	r.got = append(r.got, recs...)
	return nil
}

// A record longer than memory takes is cut to fit, and one memory still
// turns down does not hold back the rest: it is let go, not offered again
// until it changes, and the rest go in. A failure that may pass keeps
// everything queued (2026-10-23~26: one job's 2,170-character fact kept
// every record out for three days).
func TestFlushLetsARefusedRecordGo(t *testing.T) {
	ctx := context.Background()
	rec := &refuser{bad: "job:2"}
	s := &Structured{Store: store.NewMemory(), Loc: seoul, Now: func() time.Time { return time.Date(2026, 10, 26, 23, 0, 0, 0, seoul) }, Records: rec}
	long := s.JobRecord(job{ID: 1, Title: "캠핑 의자 비교", Goal: "추천", State: "done", CreatedAt: "2026-10-23T09:00:00+09:00", Summary: strings.Repeat("가", 2500)})
	bad := model.Record{Key: "job:2", Relation: "맡긴 작업", Target: model.Node{Kind: "Job", Key: "2", Name: "받지 않을 것"}, Fact: "받지 않을 것"}
	fine := model.Record{Key: "job:3", Relation: "맡긴 작업", Target: model.Node{Kind: "Job", Key: "3", Name: strings.Repeat("이", 400)}, Fact: "괜찮은 것"}
	for _, r := range []model.Record{long, bad, fine} {
		if err := s.record(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.flush(ctx); err != nil {
		t.Fatalf("one refused record failed the flush: %v", err)
	}
	got := map[string]model.Record{}
	for _, r := range rec.got {
		got[r.Key] = r
	}
	if f := got["job:1"].Fact; utf8.RuneCountInString(f) != maxFact || !strings.HasSuffix(f, "…") || !strings.HasPrefix(f, "2026-10-23에 맡긴 작업") {
		t.Errorf("a long fact should be cut to %d characters: %d %q", maxFact, utf8.RuneCountInString(f), f[:40])
	}
	if n := utf8.RuneCountInString(got["job:3"].Target.Name); n != maxName {
		t.Errorf("a long name should be cut to %d characters: %d", maxName, n)
	}
	if _, ok := got["job:2"]; ok || len(s.queued) != 0 {
		t.Fatalf("the refused record should be let go: got %v, queued %d", ok, len(s.queued))
	}
	if err := s.record(ctx, bad); err != nil || len(s.queued) != 0 {
		t.Fatalf("a refused record should not be offered again unchanged: queued %d %v", len(s.queued), err)
	}
	changed := bad
	changed.Key, changed.Fact = "job:2b", "고친 것"
	_ = s.record(ctx, changed)

	rec.down = true
	if err := s.flush(ctx); err == nil || len(s.queued) != 1 {
		t.Fatalf("memory down should keep the queue: queued %d %v", len(s.queued), err)
	}
	rec.down = false
	if err := s.flush(ctx); err != nil || len(s.queued) != 0 {
		t.Fatalf("memory back should take what waited: queued %d %v", len(s.queued), err)
	}
}

// Assets: holdings, flows and sales become occurrences and records; a
// holding gone from a complete answer is sold, once; one gone while a venue
// failed is not.
func TestAssetsAndJobs(t *testing.T) {
	ctx := context.Background()
	portfolio := `{"holdings":[{"id":"kis:GLD","kind":"stock","name":"금 ETF","symbol":"GLD","quantity":3,"since":"2026-06-02"},
	                           {"id":"upbit:BTC","kind":"crypto","name":"비트코인","symbol":"BTC","quantity":0.01234567}],
	               "problems":[],"principal":{"flows":[{"id":"f1","venue":"kis","amountKrw":-300000,"date":"2026-10-01","memo":"생활비"}]},
	               "realized":{"sales":[{"id":"s1","date":"2026-10-02","kind":"stock","name":"금 ETF","symbol":"GLD","quantity":1,"profitKrw":-1200.4}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			w.Write([]byte(`{"jobs":[{"id":3,"title":"장작 담기","goal":"장작","state":"active","createdAt":"2026-10-12T09:00:00+09:00"}]}`))
		}
	}))
	defer srv.Close()
	st := store.NewMemory()
	rec := &recorder{}
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, seoul)
	s := &Structured{Store: st, Observer: observer{st}, Loc: seoul, Now: func() time.Time { return now }, JobsURL: srv.URL, Records: rec}
	assets := func() error {
		return s.Take(ctx, queue.Message{Kind: KindHoldings, Payload: json.RawMessage(portfolio), OccurredAt: now})
	}
	if err := assets(); err != nil {
		t.Fatal(err)
	}
	if err := s.Jobs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.flush(ctx); err != nil {
		t.Fatal(err)
	}
	facts := map[string]string{}
	for _, r := range rec.got {
		facts[r.Key] = r.Fact
	}
	for key, want := range map[string]string{
		"holding:kis:GLD":   "금 ETF(GLD)을 3주 보유 중 (2026-06-02부터)",
		"holding:upbit:BTC": "비트코인(BTC)을 0.0123 보유 중 (기록 시작(5월 1일) 전부터)",
		"flow:f1":           "2026-10-01 한국투자증권에 300,000원 출금 (생활비)",
		"sale:s1":           "2026-10-02 금 ETF 1주 매도, 실현손익 -1,200원",
		"job:3":             "2026-10-12에 맡긴 작업 '장작 담기': 장작 - 지금 진행 중",
	} {
		if facts[key] != want {
			t.Errorf("%s = %q, want %q", key, facts[key], want)
		}
	}
	if o, err := st.Occurrence(ctx, "asset:flow:f1"); err != nil || o.Class != "출금" || o.Amount != 300000 {
		t.Errorf("flow occurrence: %+v %v", o, err)
	}
	if o, err := st.Occurrence(ctx, "job:3"); err != nil || o.Kind != "job" || o.Class != "active" {
		t.Errorf("job occurrence: %+v %v", o, err)
	}

	// Nothing changed: nothing is sent again.
	rec.got = nil
	now = now.Add(10 * time.Minute)
	if err := assets(); err != nil {
		t.Fatal(err)
	}
	if err := s.flush(ctx); err != nil || len(rec.got) != 0 {
		t.Fatalf("sent again: %+v %v", rec.got, err)
	}

	// A venue failed: BTC missing is not a sale.
	portfolio = strings.Replace(portfolio, `"problems":[]`, `"problems":["upbit"]`, 1)
	portfolio = strings.Replace(portfolio, `,
	                           {"id":"upbit:BTC","kind":"crypto","name":"비트코인","symbol":"BTC","quantity":0.01234567}`, "", 1)
	now = now.Add(10 * time.Minute)
	if err := assets(); err != nil {
		t.Fatal(err)
	}
	_ = s.flush(ctx)
	for _, r := range rec.got {
		if strings.HasPrefix(r.Key, "holding-sold:") {
			t.Fatalf("sold while a venue failed: %+v", r)
		}
	}
	// Every venue answered: now it is.
	portfolio = strings.Replace(portfolio, `"problems":["upbit"]`, `"problems":[]`, 1)
	now = now.Add(10 * time.Minute)
	if err := assets(); err != nil {
		t.Fatal(err)
	}
	_ = s.flush(ctx)
	sold := 0
	for _, r := range rec.got {
		if strings.HasPrefix(r.Key, "holding-sold:upbit:BTC:") && r.Fact == "비트코인(BTC)을 2026-10-15에 모두 처분" {
			sold++
		}
	}
	if sold != 1 {
		t.Fatalf("sold %d times: %+v", sold, rec.got)
	}
	o, _ := st.Occurrence(ctx, "asset:holding:upbit:BTC")
	if o.Until == nil || o.Class != "처분" {
		t.Errorf("sold holding: %+v", o)
	}
}
