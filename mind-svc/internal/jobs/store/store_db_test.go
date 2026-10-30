package store

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/jobs/rules"
)

// Runs against a throwaway database as svc_mind (scripts/dbtest mind-svc).
func TestAddSitesKeepsEachOnce(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest mind-svc)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Create(ctx, NewJob{Title: "시험", Goal: "사이트 더하기", Sites: []string{"www.yes24.com"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AddSites(ctx, j.ID, []string{"https://ticket.melon.com/search", "WWW.YES24.COM", " "})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Sites, []string{"ticket.melon.com", "www.yes24.com"}) {
		t.Fatalf("사이트: %v", got.Sites)
	}
	if _, err := s.AddSites(ctx, j.ID, []string{" "}); err == nil {
		t.Fatal("빈 사이트만 오면 오류")
	}
	if _, err := s.AddSites(ctx, 999999, []string{"a.com"}); err != ErrNotFound {
		t.Fatalf("없는 작업: %v", err)
	}
}

// Against a throwaway Postgres, as svc_jobs (scripts/dbtest): each run in a
// row that gave way to the conversation pushes the next one further out, and
// a run that went through starts the count over.
func TestYieldedRunsBackOff(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	j, err := s.Create(ctx, NewJob{Title: "dbtest 양보", Goal: "기다리기", Sites: []string{"shop.example"}})
	if err != nil {
		t.Fatal(err)
	}

	end := func(outcome string) time.Duration {
		t.Helper()
		run, err := s.StartRun(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if outcome == rules.OK {
			if _, err := s.Schedule(ctx, run, time.Now().Add(time.Hour), "다음"); err != nil {
				t.Fatal(err)
			}
		}
		before := time.Now()
		if _, err := s.EndRun(ctx, run, outcome, ""); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, j.ID)
		if err != nil || got.NextAt == nil {
			t.Fatalf("다음 실행: %+v %v", got, err)
		}
		return got.NextAt.Sub(before).Round(time.Minute)
	}
	for i, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute} {
		if got := end(rules.Yielded); got != want {
			t.Fatalf("%d번째 양보 뒤 %v, 기대 %v", i+1, got, want)
		}
	}
	if got := end(rules.OK); got != time.Hour {
		t.Fatalf("스스로 정한 다음 실행: %v", got)
	}
	if got := end(rules.Yielded); got != 5*time.Minute {
		t.Fatalf("끝까지 간 실행 뒤로는 다시 5분부터: %v", got)
	}
	d, err := s.Detail(ctx, j.ID, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Events) == 0 || !strings.Contains(d.Events[0].Text, "5분 뒤에 다시") {
		t.Fatalf("기록: %+v", d.Events)
	}
}

// Against a throwaway Postgres, as svc_jobs (scripts/dbtest): a card's answer
// wakes the job at once, whether it was waiting on that card or had put
// itself on a clock after asking (job 30, 2026-10-21); what comes while a
// run is in flight sends the job again the moment the run ends, whatever the
// run set; and the scheduler is told when the next clock comes due.
func TestAnswersAndInstructionsWakeAtOnce(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	j, err := s.Create(ctx, NewJob{Title: "dbtest 깨우기", Goal: "카드 답에 바로 이어 가기", Sites: []string{"wake.example"}})
	if err != nil {
		t.Fatal(err)
	}
	soon := func(what string) Job {
		t.Helper()
		got, err := s.Get(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != rules.Active || got.NextAt == nil || time.Until(*got.NextAt) > time.Second || got.WaitingCard != "" {
			t.Fatalf("%s: 바로 다시 돌아야 합니다: %+v", what, got)
		}
		return got
	}
	run := func() int64 {
		t.Helper()
		id, err := s.StartRun(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	end := func(id int64) Ended {
		t.Helper()
		out, err := s.EndRun(ctx, id, rules.OK, "")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Waiting on the card it asked: the answer wakes it.
	r := run()
	if err := s.Wait(ctx, r, "p-dbtest-1", "물어봄"); err != nil {
		t.Fatal(err)
	}
	if out := end(r); out.Again {
		t.Fatal("실행 중에 온 것이 없는데 다시 돌립니다")
	}
	if got, _ := s.Get(ctx, j.ID); got.State != rules.Waiting || got.NextAt != nil {
		t.Fatalf("카드를 기다리는 작업에는 시계가 없습니다: %+v", got)
	}
	if err := s.Answer(ctx, j.ID, "p-dbtest-1", "[승인] 물어봄"); err != nil {
		t.Fatal(err)
	}
	soon("기다리던 카드의 답")

	// Asked, then put itself on a clock to "check the answer": the answer
	// still wakes it now, not at the clock.
	r = run()
	if _, err := s.Schedule(ctx, r, time.Now().Add(time.Hour), "카드 답 확인"); err != nil {
		t.Fatal(err)
	}
	end(r)
	if err := s.Answer(ctx, j.ID, "p-dbtest-2", "[승인] 다른 카드"); err != nil {
		t.Fatal(err)
	}
	soon("시계 위의 작업에 온 답")

	// An instruction while the run is in flight, and the run then sets its
	// own clock: the job goes again as the run ends.
	r = run()
	if err := s.Instruct(ctx, j.ID, "가격을 내려줘"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule(ctx, r, time.Now().Add(time.Hour), "다음"); err != nil {
		t.Fatal(err)
	}
	if out := end(r); !out.Again {
		t.Fatal("실행 중에 온 지시가 있으면 Again")
	}
	soon("실행 중에 온 지시")
	d, err := s.Detail(ctx, j.ID, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Inbox) != 1 || d.Inbox[0].Kind != "instruction" {
		t.Fatalf("그 지시는 읽히지 않은 채 남아야 합니다: %+v", d.Inbox)
	}

	// The same while the run waits on a card of its own.
	r = run()
	if err := s.Answer(ctx, j.ID, "p-dbtest-3", "[반려] 지난 카드"); err != nil {
		t.Fatal(err)
	}
	if err := s.Wait(ctx, r, "p-dbtest-4", "새 카드"); err != nil {
		t.Fatal(err)
	}
	if out := end(r); !out.Again {
		t.Fatal("실행 중에 온 답이 있으면 Again")
	}
	soon("실행 중에 온 카드 답")

	// The next clock: a job scheduled ten minutes out is at most that far.
	r = run()
	at, err := s.Schedule(ctx, r, time.Now(), "다음")
	if err != nil {
		t.Fatal(err)
	}
	end(r)
	next, err := s.NextDue(ctx, Owner)
	if err != nil || next == nil || next.After(at.Add(time.Second)) {
		t.Fatalf("다음 시각 %v (잡은 시각 %v): %v", next, at, err)
	}
}

// Against a throwaway Postgres, as svc_jobs (scripts/dbtest): a run that
// left its job waiting on a card and then ended badly is counted, but it is
// the answer that wakes the job - no retry time is set and the log does not
// promise one. Three bad runs in a row stop the job, and it is in the held
// box /health shows (StoppedJobs) until it is resumed; one past its time with
// nothing running counts as overdue.
func TestABadRunWaitingOnACardWaitsForTheAnswer(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	j, err := s.Create(ctx, NewJob{Title: "dbtest 카드 대기", Goal: "답 기다리기", Sites: []string{"shop.example"}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.StartRun(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Wait(ctx, run, "p-1", "카드 답 대기: 가격"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndRun(ctx, run, rules.Error, "브라우저가 닫혔습니다"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, j.ID)
	if err != nil || got.State != rules.Waiting || got.NextAt != nil || got.Attempts != 1 {
		t.Fatalf("카드를 기다리는 채로, 다시 시도할 시각 없이: %+v %v", got, err)
	}
	d, _ := s.Detail(ctx, j.ID, 5, true)
	if len(d.Events) == 0 || !strings.Contains(d.Events[0].Text, "카드에 답이 오면 다시") || strings.Contains(d.Events[0].Text, "15분") {
		t.Fatalf("기록: %+v", d.Events)
	}

	k, err := s.Create(ctx, NewJob{Title: "dbtest 멈춤", Goal: "실패하기", Sites: []string{"shop.example"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rules.MaxAttempts; i++ {
		run, err := s.StartRun(ctx, k.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.EndRun(ctx, run, rules.Error, "주문서가 열리지 않습니다"); err != nil {
			t.Fatal(err)
		}
	}
	stopped, err := s.StoppedJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sj := range stopped {
		found = found || (sj.ID == k.ID && strings.Contains(sj.Summary, "주문서가 열리지 않습니다"))
	}
	if !found {
		t.Fatalf("세 번 실패해 멈춘 작업이 격리함에 있어야 합니다: %+v", stopped)
	}
	if err := s.Control(ctx, k.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	stopped, _ = s.StoppedJobs(ctx)
	for _, sj := range stopped {
		if sj.ID == k.ID {
			t.Fatal("다시 시작한 작업은 격리함에서 나갑니다")
		}
	}

	if _, err := s.pool.Exec(ctx, `update mind.jobs set next_at = now() - interval '3 hours' where id = $1`, k.ID); err != nil {
		t.Fatal(err)
	}
	n, oldest, err := s.Overdue(ctx)
	if err != nil || n < 1 || oldest == nil || time.Since(*oldest) < 2*time.Hour {
		t.Fatalf("때가 지난 작업: %d %v %v", n, oldest, err)
	}
	_ = s.Control(ctx, k.ID, "cancel")
}

// A job that is over keeps nothing unread, and an answer to it goes into its
// history only (job 6: an answer before it was called off; job 18: one to a
// card it raised and then finished without waiting for, 2026-10-08/18). An
// instruction that came while it was finishing brings it back.
func TestAJobThatIsOverKeepsNothingUnread(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest mind-svc)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	unread := func(id int64) int {
		t.Helper()
		var n int
		if err := s.pool.QueryRow(ctx, `select count(*) from mind.job_inbox where job_id = $1 and consumed_run is null`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Called off with an answer and an instruction unread: both go, the card too.
	a, err := s.Create(ctx, NewJob{Title: "dbtest 취소", Goal: "돗자리 예약"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.StartRun(ctx, a.ID)
	if err := s.Wait(ctx, run, "p-over-1", "이 곳으로 진행할까요?"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndRun(ctx, run, rules.OK, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Answer(ctx, a.ID, "p-over-1", "[반려] 이 곳으로 진행할까요?"); err != nil {
		t.Fatal(err)
	}
	if err := s.Instruct(ctx, a.ID, "평점 좋은 곳만"); err != nil {
		t.Fatal(err)
	}
	if err := s.Control(ctx, a.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if n := unread(a.ID); n != 0 {
		t.Fatalf("그만둔 작업에 읽지 않은 것 %d개", n)
	}
	if got, _ := s.Get(ctx, a.ID); got.WaitingCard != "" || got.State != rules.Cancelled {
		t.Fatalf("그만둔 작업은 카드를 기다리지 않습니다: %+v", got)
	}
	if err := s.Answer(ctx, a.ID, "p-over-1", "[승인] 늦은 답"); !errors.Is(err, ErrEnded) {
		t.Fatalf("그만둔 작업의 답은 ErrEnded: %v", err)
	}
	if n := unread(a.ID); n != 0 {
		t.Fatalf("그만둔 작업의 답은 쌓이지 않습니다: %d", n)
	}

	// Asked and finished in one run: over, its card's answer goes nowhere.
	b, err := s.Create(ctx, NewJob{Title: "dbtest 끝냄", Goal: "탁상시계 결제"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = s.StartRun(ctx, b.ID)
	if err := s.Wait(ctx, run, "p-over-2", "어떻게 할까요?"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, run, "주문 미완료, 재시도 여부 문의"); err != nil {
		t.Fatal(err)
	}
	out, err := s.EndRun(ctx, run, rules.OK, "")
	if err != nil || !out.Over {
		t.Fatalf("끝낸 작업은 Over: %+v %v", out, err)
	}
	if got, _ := s.Get(ctx, b.ID); got.WaitingCard != "" || got.State != rules.Done {
		t.Fatalf("끝낸 작업은 카드를 기다리지 않습니다: %+v", got)
	}
	if err := s.Answer(ctx, b.ID, "p-over-2", "[반려] 어떻게 할까요?"); !errors.Is(err, ErrEnded) {
		t.Fatalf("끝낸 작업의 답은 ErrEnded: %v", err)
	}
	if n := unread(b.ID); n != 0 {
		t.Fatalf("끝낸 작업에 읽지 않은 것 %d개", n)
	}
	d, _ := s.Detail(ctx, b.ID, 10, true)
	if !slices.ContainsFunc(d.Events, func(e Event) bool { return e.Kind == "answer" && strings.Contains(e.Text, "전하지 않았습니다") }) {
		t.Fatalf("늦은 답은 이력에 남습니다: %+v", d.Events)
	}

	// An instruction while the last run was finishing: back it comes for it.
	c, err := s.Create(ctx, NewJob{Title: "dbtest 끝내는 중 지시", Goal: "가격 알아보기"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = s.StartRun(ctx, c.ID)
	if err := s.Instruct(ctx, c.ID, "배송비도 봐 줘"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, run, "가격 정리"); err != nil {
		t.Fatal(err)
	}
	out, err = s.EndRun(ctx, run, rules.OK, "")
	if err != nil || out.Over || !out.Again {
		t.Fatalf("끝내는 중에 온 지시로 다시: %+v %v", out, err)
	}
	if got, _ := s.Get(ctx, c.ID); got.State != rules.Active || got.NextReason != "끝내는 중에 온 지시" {
		t.Fatalf("다시 깨어나야 합니다: %+v", got)
	}
	if n := unread(c.ID); n != 1 {
		t.Fatalf("지시는 다음 실행 앞에 남습니다: %d", n)
	}
	_ = s.Control(ctx, c.ID, "cancel")
}

// Against a throwaway Postgres, as svc_jobs (scripts/dbtest): a job the
// operator puts first (the agent's 먼저 card) goes ahead of every due job
// and of the run in flight; the run it stops goes on right after it, ahead of
// the job asked for while it ran - and that is no attempt (2026-10-28).
func TestAJobPutFirstAndTheRunItStops(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	create := func(title string) Job {
		t.Helper()
		j, err := s.Create(ctx, NewJob{Title: title, Goal: "차례 지키기", Sites: []string{"shop.example"}})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	a := create("dbtest 먼저 A")
	run, err := s.StartRun(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	b := create("dbtest 먼저 B")
	c := create("dbtest 먼저 C")
	if err := s.Control(ctx, b.ID, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndRun(ctx, run, rules.Deferred, b.Title); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueN(ctx, Owner, 100)
	if err != nil {
		t.Fatal(err)
	}
	var order []int64
	for _, j := range due {
		if j.ID == a.ID || j.ID == b.ID || j.ID == c.ID {
			order = append(order, j.ID)
		}
	}
	if !slices.Equal(order, []int64{b.ID, a.ID, c.ID}) {
		t.Fatalf("차례: %v, 기대 B %d · A %d · C %d", order, b.ID, a.ID, c.ID)
	}
	got, err := s.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 0 || got.State != rules.Active || got.NextReason != "먼저 한 작업 다음에 이어서" {
		t.Fatalf("양보한 작업: %+v", got)
	}
	d, err := s.Detail(ctx, a.ID, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Events) == 0 || !strings.Contains(d.Events[0].Text, "'dbtest 먼저 B' 작업을 먼저 하기로") {
		t.Fatalf("기록: %+v", d.Events)
	}
	// Only a job in line can be put first.
	if err := s.Control(ctx, c.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if err := s.Control(ctx, c.ID, "first"); err == nil {
		t.Fatal("멈춘 작업은 먼저 할 수 없습니다")
	}
}
