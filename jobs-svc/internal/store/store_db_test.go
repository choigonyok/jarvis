package store

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/jobs-svc/internal/rules"
)

// Runs against a throwaway database as svc_jobs (scripts/dbtest jobs-svc).
func TestAddSitesKeepsEachOnce(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest jobs-svc)")
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
