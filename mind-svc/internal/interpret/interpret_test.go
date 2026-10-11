package interpret

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

func setup(t *testing.T, answer string) (*Conversation, *store.Memory, *llm.Script) {
	t.Helper()
	st := store.NewMemory()
	now := func() time.Time { return time.Date(2026, 10, 12, 20, 5, 0, 0, seoul) }
	w := world.New(st, now)
	script := &llm.Script{Answer: func(_, _ string) (string, error) { return answer, nil }}
	c := NewConversation(Config{Owner: "윤"}, st, w, script, seoul, now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return c, st, script
}

func say(t *testing.T, st *store.Memory, ref, text string, mine bool, at time.Time) {
	t.Helper()
	if err := st.PutMessage(context.Background(), model.Message{Ref: ref, Source: "sim", Room: "r", RoomName: "지수", Sender: "지수", IsMine: mine, SentAt: at, Text: text}); err != nil {
		t.Fatal(err)
	}
}

// A change whose quote is not in the line it cites is not believed.
func TestEvidenceMustBeInTheMessage(t *testing.T) {
	c, st, _ := setup(t, `{"changes":[
		{"op":"create","kind":"plan","set":{"title":"지어낸 약속","status":"confirmed","date":"2026-10-20"},"evidence":[{"line":1,"quote":"20일에 보자"}],"confidence":0.9},
		{"op":"create","kind":"plan","set":{"title":"없는 줄","status":"confirmed"},"evidence":[{"line":7,"quote":"그래"}],"confidence":0.9},
		{"op":"create","kind":"plan","set":{"title":"진짜 약속","status":"proposed","date":"2026-10-17"},"evidence":[{"line":1,"quote":"강릉  갈까"}],"confidence":0.8}
	]}`)
	say(t, st, "sim:1", "이번 주말에 강릉 갈까?", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(context.Background(), "r"); err != nil {
		t.Fatal(err)
	}
	plans, _ := st.Entities(context.Background(), model.KindPlan, "", false)
	if len(plans) != 1 || plans[0].State.Title != "진짜 약속" {
		t.Fatalf("근거가 맞는 것만 남아야 합니다: %+v", plans)
	}
	cs, _ := st.Changes(context.Background(), plans[0].ID, 10)
	if len(cs) != 1 || cs[0].Evidence[0].Ref != "sim:1" || cs[0].Actor != "interpreter" {
		t.Fatalf("이력에 근거가 남아야 합니다: %+v", cs)
	}
}

// What is already known is shown by short refs, and a create that names
// the same plan updates it instead of making a second one.
func TestKnownPlansAndSamePlan(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, `{"changes":[{"op":"create","kind":"plan","set":{"title":"지수와 강릉 여행","status":"confirmed","date":"bad-date"},"evidence":[{"line":1,"quote":"그러자"}],"confidence":0.9}]}`)
	if _, err := c.world.Create(ctx, model.KindPlan, "r", model.State{Title: "지수와 강릉 여행", Status: model.Proposed, Date: "2026-10-17", Place: "강릉"}, world.Meta{Actor: "interpreter"}); err != nil {
		t.Fatal(err)
	}
	say(t, st, "sim:1", "이번 주말에 강릉 갈까?", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	say(t, st, "sim:2", "그러자!", true, time.Date(2026, 10, 12, 20, 2, 0, 0, seoul))
	_ = st.MarkRoom(ctx, "r", time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	p := script.Prompts[0]
	for _, want := range []string{"P1 [plan] 지수와 강릉 여행", "앞선 대화", "이번 주말에 강릉 갈까?", "[1] 10/12(월) 20:02 윤(나): 그러자!"} {
		if !strings.Contains(p, want) {
			t.Fatalf("프롬프트에 %q 가 없습니다:\n%s", want, p)
		}
	}
	plans, _ := st.Entities(ctx, model.KindPlan, "", false)
	if len(plans) != 1 || plans[0].State.Status != model.Confirmed || plans[0].State.Date != "2026-10-17" {
		t.Fatalf("같은 일정이 확정으로 바뀌고, 잘못된 날짜는 버려져야 합니다: %+v", plans)
	}
}

// Nothing new, nothing read; the room is not read twice.
func TestReadOnlyWhatIsNew(t *testing.T) {
	c, st, script := setup(t, `{"changes":[]}`)
	say(t, st, "sim:1", "ㅋㅋ", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	for range 2 {
		if err := c.Read(context.Background(), "r"); err != nil {
			t.Fatal(err)
		}
	}
	if script.Calls() != 1 {
		t.Fatalf("한 번만 읽어야 합니다: %d", script.Calls())
	}
}

// A room waits until it is quiet, and a notes-to-self room waits less.
func TestQuietBeforeReading(t *testing.T) {
	c, _, _ := setup(t, `{"changes":[]}`)
	c.cfg.Idle, c.cfg.IdleSelf = 3*time.Minute, 30*time.Second
	now := c.now()
	c.Observe(model.Message{Room: "self", Source: "kakao", IsMine: true})
	c.Observe(model.Message{Room: "chat", Source: "kakao", IsMine: false})
	due := c.Pending()
	if !due["self"].Equal(now.Add(30*time.Second)) || !due["chat"].Equal(now.Add(3*time.Minute)) {
		t.Fatalf("기다리는 시간이 틀렸습니다: %v", due)
	}
}

// The date table names each day the way a message would.
func TestDateTable(t *testing.T) {
	tbl := dateTable(time.Date(2026, 10, 13, 10, 0, 0, 0, seoul), seoul) // a Tuesday
	for _, want := range []string{
		"2026-10-13 (화) 오늘 · 이번 주",
		"2026-10-14 (수) 내일 · 이번 주",
		"2026-10-15 (목) 모레 · 이번 주",
		"2026-10-18 (일) 이번 주",
		"2026-10-19 (월) 다음 주",
		"2026-10-26 (월) 다다음 주",
	} {
		if !strings.Contains(tbl, want) {
			t.Fatalf("날짜표에 %q 가 없습니다:\n%s", want, tbl)
		}
	}
}
