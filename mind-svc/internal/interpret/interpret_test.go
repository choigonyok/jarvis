package interpret

import (
	"context"
	"fmt"
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

// A fact is believed like a change - its quote must be in its line - and goes
// to the ledger pending, with "나" as the operator and the relation tidied.
// Read again, the same sentence is not written twice.
func TestFactsGoToTheLedger(t *testing.T) {
	ctx := context.Background()
	c, st, _ := setup(t, `{"changes":[
		{"op":"remember","kind":"fact","set":{"subject":"지수","subjectKind":"Person","relation":"dislikes","object":"매운 음식","objectKind":"Food","text":"지수는 매운 음식을 못 먹는다"},"evidence":[{"line":1,"quote":"매운 거 못 먹어"}],"confidence":0.9},
		{"op":"remember","kind":"fact","set":{"subject":"윤","relation":"FRIEND_OF","object":"지수","objectKind":"Person","text":"지수는 나의 대학 동기다"},"evidence":[{"line":2,"quote":"대학 동기"}],"confidence":0.8},
		{"op":"remember","kind":"fact","set":{"subject":"지수","relation":"LIVES_IN","object":"부산","text":"지수는 부산에 산다"},"evidence":[{"line":1,"quote":"부산 살아"}],"confidence":0.9},
		{"op":"remember","kind":"fact","set":{"subject":"지수","relation":"","object":"x","text":"관계 없음"},"evidence":[{"line":1,"quote":"매운"}],"confidence":0.9}
	]}`)
	nudged := 0
	c.cfg.OnFact = func() { nudged++ }
	say(t, st, "sim:1", "나 매운 거 못 먹어 ㅠ", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	say(t, st, "sim:2", "우리 대학 동기잖아", true, time.Date(2026, 10, 12, 20, 2, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	fs, _ := st.Facts(ctx, 10)
	if len(fs) != 2 || nudged != 2 {
		t.Fatalf("근거가 맞고 빠진 데 없는 사실 둘만 남아야 합니다: %+v (nudged %d)", fs, nudged)
	}
	byText := map[string]model.Fact{}
	for _, f := range fs {
		byText[f.Text] = f
	}
	spicy := byText["지수는 매운 음식을 못 먹는다"]
	if spicy.Relation != "DISLIKES" || spicy.Object.Kind != "Thing" || spicy.Status != model.FactPending || spicy.Evidence[0].Ref != "sim:1" {
		t.Fatalf("관계 이름·종류·상태·근거: %+v", spicy)
	}
	if friend := byText["지수는 나의 대학 동기다"]; !friend.Subject.Self || friend.Subject.Name != "나" {
		t.Fatalf("운영자 이름은 나로: %+v", friend)
	}

	say(t, st, "sim:3", "ㅋㅋ", false, time.Date(2026, 10, 12, 20, 3, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if fs, _ := st.Facts(ctx, 10); len(fs) != 2 {
		t.Fatalf("같은 문장은 다시 적지 않습니다: %d", len(fs))
	}
}

// What memory already holds is shown to the model as context, apart from the
// conversation.
func TestRecallIsShown(t *testing.T) {
	c, st, script := setup(t, `{"changes":[]}`)
	c.cfg.Recall = func(context.Context, string, string, []model.Message) []string {
		return []string{"지수는 매운 음식을 못 먹는다"}
	}
	say(t, st, "sim:1", "저녁 뭐 먹지", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(context.Background(), "r"); err != nil {
		t.Fatal(err)
	}
	if script.Calls() != 1 || !strings.Contains(script.Prompts[0], "## 이미 기억하는 것") || !strings.Contains(script.Prompts[0], "매운 음식") {
		t.Fatalf("기억이 프롬프트에 있어야 합니다: %v", script.Prompts)
	}
}

// A backlog longer than one batch is read on, not left until the room speaks
// again.
func TestBacklogIsReadOn(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, `{"changes":[]}`)
	c.cfg.MaxBatch = 2
	for i, text := range []string{"하나", "둘", "셋"} {
		say(t, st, fmt.Sprintf("sim:%d", i+1), text, false, time.Date(2026, 10, 12, 20, i, 0, 0, seoul))
	}
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if _, due := c.Pending()["r"]; !due {
		t.Fatal("남은 메시지가 있으면 다시 읽을 차례여야 합니다")
	}
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if _, due := c.Pending()["r"]; due || script.Calls() != 2 {
		t.Fatalf("다 읽으면 끝: due=%v calls=%d", due, script.Calls())
	}
}
