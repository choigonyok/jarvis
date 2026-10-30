package interpret

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
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

// The calendar's coming plans are shown in every room, apart from the room's
// own and nearest first - not the partner's, not the past, not past two
// months - and a room can call one off but not retitle it. A plan there with
// no place lets the read search where the office in its title is.
func TestCalendarPlansAreShownEverywhere(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, `{"changes":[
		{"op":"cancel","kind":"plan","ref":"P4","set":{"title":"바뀐 제목"},"evidence":[{"line":1,"quote":"11월에 판교 안 간대"}],"confidence":0.6,"why":"가나전자 본사는 판교 - 검색"},
		{"op":"create","kind":"plan","set":{"title":"PT","status":"confirmed","date":"2026-10-14"},"evidence":[{"line":1,"quote":"안 간대"}],"confidence":0.9}
	]}`)
	c.cfg.Search = 3
	for _, p := range []struct {
		room string
		s    model.State
	}{
		{"r", model.State{Title: "지수와 강릉 여행", Status: model.Confirmed, Date: "2026-10-17", CalendarID: "c7"}},
		{"calendar", model.State{Title: "가나전자 본사 출근", Status: model.Confirmed, Date: "2026-11-02", CalendarID: "c1"}},
		{"calendar", model.State{Title: "PT", Status: model.Confirmed, Date: "2026-10-14", Start: "19:00", Place: "헬스장", CalendarID: "c2"}},
		{"calendar", model.State{Title: "미용실", Status: model.Confirmed, Date: "2026-10-15", Owner: "partner", CalendarID: "c3"}},
		{"calendar", model.State{Title: "지난 일", Status: model.Confirmed, Date: "2026-10-01", CalendarID: "c4"}},
		{"calendar", model.State{Title: "먼 일", Status: model.Confirmed, Date: "2027-01-20", CalendarID: "c5"}},
		{"q", model.State{Title: "민수와 저녁", Status: model.Confirmed, Date: "2026-10-20", CalendarID: "c6"}},
		{"q", model.State{Title: "민수와 영화", Status: model.Proposed, Date: "2026-10-21"}},
	} {
		if _, err := c.world.Create(ctx, model.KindPlan, p.room, p.s, world.Meta{Actor: "sensor:calendar"}); err != nil {
			t.Fatal(err)
		}
	}
	say(t, st, "sim:1", "아 그리고 11월에 판교 안 간대!", true, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	p := script.Prompts[0]
	for _, want := range []string{"P1 [plan] 지수와 강릉 여행", "## 캘린더에 있는 다가오는 일정", "P2 [plan] PT", "P3 [plan] 민수와 저녁", "P4 [plan] 가나전자 본사 출근"} {
		if !strings.Contains(p, want) {
			t.Fatalf("프롬프트에 %q 가 없습니다:\n%s", want, p)
		}
	}
	for _, not := range []string{"미용실", "지난 일", "먼 일", "민수와 영화"} {
		if strings.Contains(p, not) {
			t.Fatalf("프롬프트에 %q 가 없어야 합니다:\n%s", not, p)
		}
	}
	if strings.Count(p, "지수와 강릉 여행") != 1 {
		t.Fatalf("이 방의 일정은 한 번만 보여야 합니다:\n%s", p)
	}
	if script.Searches[0] != 3 {
		t.Fatalf("장소 없는 캘린더 일정이 있으면 검색을 허락해야 합니다: %v", script.Searches)
	}
	plans, _ := st.Entities(ctx, model.KindPlan, "", false)
	pts := 0
	for _, e := range plans {
		switch e.State.Title {
		case "가나전자 본사 출근":
			if e.State.Status != model.Cancelled {
				t.Fatalf("다른 방의 말로 캘린더 일정이 취소돼야 합니다: %+v", e.State)
			}
		case "바뀐 제목":
			t.Fatalf("캘린더 일정의 제목은 바꾸지 않아야 합니다: %+v", e)
		case "PT":
			pts++
		}
	}
	if pts != 2 {
		t.Fatalf("같은 제목이라도 캘린더 일정은 새로 만든 일정에 덮이지 않아야 합니다(이어 주는 건 일정 스킬): PT %d개", pts)
	}

	// Every calendar plan with its place: nothing to search for.
	if placeless([]view{{elsewhere: true, entity: model.Entity{State: model.State{Place: "헬스장"}}}, {entity: model.Entity{}}}) {
		t.Fatal("장소가 다 있으면 검색할 것이 없습니다")
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
	c.cfg.Recall = func(context.Context, string, string, []model.Message) []Memory {
		return []Memory{{ID: "e1", Fact: "지수는 매운 음식을 못 먹는다"}}
	}
	say(t, st, "sim:1", "저녁 뭐 먹지", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(context.Background(), "r"); err != nil {
		t.Fatal(err)
	}
	if script.Calls() != 1 || !strings.Contains(script.Prompts[0], "## 이미 기억하는 것") || !strings.Contains(script.Prompts[0], "M1 지수는 매운 음식을 못 먹는다") {
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

// However long the backlog, it is read from its oldest message on: none is
// passed over between one batch and the next.
func TestLongBacklogSkipsNothing(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, `{"changes":[]}`)
	c.cfg.MaxBatch = 40
	start := time.Date(2026, 10, 12, 10, 0, 0, 0, seoul)
	for i := range 250 {
		say(t, st, fmt.Sprintf("sim:%03d", i), fmt.Sprintf("메시지%03d", i), false, start.Add(time.Duration(i)*time.Second))
	}
	for range 7 {
		if err := c.Read(ctx, "r"); err != nil {
			t.Fatal(err)
		}
	}
	seen := strings.Join(script.Prompts, "\n")
	for i := range 250 {
		if !strings.Contains(seen, fmt.Sprintf("메시지%03d", i)) {
			t.Fatalf("메시지%03d 를 건너뛰었습니다", i)
		}
	}
	if !strings.Contains(script.Prompts[0], "메시지000") {
		t.Fatal("가장 오래된 것부터 읽어야 합니다")
	}
}

// A room that never falls quiet is still read once a batch's worth waits.
func TestBusyRoomIsReadWithoutQuiet(t *testing.T) {
	c, _, _ := setup(t, `{"changes":[]}`)
	c.cfg.MaxBatch, c.cfg.Idle = 3, time.Hour
	for i := range 3 {
		c.Observe(model.Message{Ref: fmt.Sprintf("sim:%d", i), Source: "kakao", Room: "busy", RoomName: "단톡", Sender: "누군가", SentAt: c.now()})
		if i < 2 {
			if c.Pending()["busy"].Equal(c.now()) {
				t.Fatalf("%d번째에서 벌써 읽을 차례", i+1)
			}
		}
	}
	if !c.Pending()["busy"].Equal(c.now()) {
		t.Fatal("한 번에 읽을 만큼 쌓이면 조용해지기를 기다리지 않아야 합니다")
	}
}

// "나" in someone else's message is them: a fact about the operator has to
// rest on something the operator said, and one that does not is dropped.
func TestOperatorFactsNeedTheOperatorsWords(t *testing.T) {
	ctx := context.Background()
	c, st, _ := setup(t, `{"changes":[
		{"op":"remember","kind":"fact","set":{"subject":"나","relation":"CANNOT_EAT","object":"고수","text":"나는 고수를 못 먹는다"},"evidence":[{"line":1,"quote":"나 고수 진짜 못 먹어"}],"confidence":0.9},
		{"op":"remember","kind":"fact","set":{"subject":"민수","subjectKind":"Person","relation":"CANNOT_EAT","object":"고수","text":"민수는 고수를 못 먹는다"},"evidence":[{"line":1,"quote":"나 고수 진짜 못 먹어"}],"confidence":0.9},
		{"op":"remember","kind":"fact","set":{"subject":"나","relation":"LIKES","object":"고수","text":"나는 고수를 잘 먹는다"},"evidence":[{"line":2,"quote":"난 잘 먹는데"}],"confidence":0.9}
	]}`)
	say(t, st, "sim:1", "나 고수 진짜 못 먹어", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	say(t, st, "sim:2", "난 잘 먹는데", true, time.Date(2026, 10, 12, 20, 2, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	fs, _ := st.Facts(ctx, 10)
	var texts []string
	for _, f := range fs {
		texts = append(texts, f.Text)
	}
	if len(fs) != 2 || slices.Contains(texts, "나는 고수를 못 먹는다") {
		t.Fatalf("남의 말로 나를 적으면 안 됩니다: %v", texts)
	}
}

// A changed fact names what it ends by the M ref it was shown, and the ledger
// keeps memory's id for it.
func TestFactEndsWhatItReplaces(t *testing.T) {
	ctx := context.Background()
	c, st, _ := setup(t, `{"changes":[
		{"op":"remember","kind":"fact","set":{"subject":"지수","relation":"EATS","object":"매운 음식","text":"지수는 요즘 매운 음식도 조금 먹는다"},"evidence":[{"line":1,"quote":"매운 것도 조금씩은 먹어"}],"confidence":0.9,"ends":["M2","M9"]}
	]}`)
	c.cfg.Recall = func(context.Context, string, string, []model.Message) []Memory {
		return []Memory{{ID: "e1", Fact: "지수는 서울에 산다"}, {ID: "e2", Fact: "지수는 매운 음식을 못 먹는다"}}
	}
	say(t, st, "sim:1", "요즘은 매운 것도 조금씩은 먹어", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	fs, _ := st.Facts(ctx, 10)
	if len(fs) != 1 || len(fs[0].Ends) != 1 || fs[0].Ends[0].ID != "e2" {
		t.Fatalf("M2 만, 기억의 id 로: %+v", fs)
	}
}

// An answer that cannot be read shrinks the batch, backs off longer each
// time, and a single message that still cannot be read is skipped.
func TestUnreadableBatchShrinksThenQuarantines(t *testing.T) {
	ctx := context.Background()
	c, st, _ := setup(t, `{"changes":[`)
	c.cfg.MaxBatch = 4
	for i, text := range []string{"하나", "둘", "셋", "넷"} {
		say(t, st, fmt.Sprintf("sim:%d", i+1), text, false, time.Date(2026, 10, 12, 20, i, 0, 0, seoul))
	}
	var waits []time.Duration
	for i := 0; i < 6; i++ {
		_ = c.Read(ctx, "r")
		c.mu.Lock()
		if !c.backoff["r"].IsZero() {
			waits = append(waits, c.backoff["r"].Sub(c.now()))
		}
		c.mu.Unlock()
	}
	if c.shrink["r"] != 1 && c.fails["r"] != 0 {
		t.Fatalf("배치가 한 건까지 줄어야 합니다: shrink=%d", c.shrink["r"])
	}
	if len(waits) < 3 || waits[1] <= waits[0] || waits[2] <= waits[1] {
		t.Fatalf("대기가 늘어나야 합니다: %v", waits)
	}
	marks, _ := st.Rooms(ctx)
	if len(marks) == 0 || marks[0].Interpreted.IsZero() {
		t.Fatal("끝내 못 읽은 메시지는 건너뛰어야 합니다")
	}
}

// A call that ended without an answer - the model thought until max_tokens -
// is read the way an unreadable answer is: the batch shrinks and waits, it is
// not retried whole forever.
func TestNoAnswerShrinksTheBatch(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, "")
	script.Answer = func(_, _ string) (string, error) {
		return "", fmt.Errorf("%w (멈춘 이유: max_tokens)", llm.ErrNoAnswer)
	}
	c.cfg.MaxBatch = 4
	for i, text := range []string{"하나", "둘", "셋", "넷"} {
		say(t, st, fmt.Sprintf("sim:%d", i+1), text, false, time.Date(2026, 10, 12, 20, i, 0, 0, seoul))
	}
	if err := c.Read(ctx, "r"); err == nil || !errors.Is(err, llm.ErrNoAnswer) {
		t.Fatalf("답 없음은 오류로 남아야 합니다: %v", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shrink["r"] != 2 || c.backoff["r"].IsZero() {
		t.Fatalf("배치를 줄이고 기다려야 합니다: shrink=%d backoff=%v", c.shrink["r"], c.backoff["r"])
	}
}

// 15: what someone else says about the operator is kept, as hearsay with a
// low confidence - not dropped, and not taken for the operator's own words.
func TestHearsayAboutTheOperatorIsKeptLow(t *testing.T) {
	ctx := context.Background()
	c, st, _ := setup(t, `{"changes":[
		{"op":"remember","kind":"fact","set":{"subject":"나","relation":"CANNOT_EAT","object":"고수","text":"나는 고수를 못 먹는다"},"evidence":[{"line":1,"quote":"너 고수 못 먹잖아"}],"confidence":0.9,"why":"지수가 말함"}
	]}`)
	say(t, st, "sim:1", "너 고수 못 먹잖아", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	fs, _ := st.Facts(ctx, 10)
	if len(fs) != 1 {
		t.Fatalf("상대가 운영자에 대해 한 말은 남아야 합니다: %+v", fs)
	}
	if fs[0].Confidence > hearsayConfidence || !strings.Contains(fs[0].Why, "상대가 한 말") || !fs[0].Subject.Self {
		t.Fatalf("낮은 확신도의 들은 말로 남아야 합니다: %+v", fs[0])
	}
}

func notice(t *testing.T, st *store.Memory, ref, text string, at time.Time) {
	t.Helper()
	if err := st.PutMessage(context.Background(), model.Message{Ref: ref, Source: "chat", Room: "r", RoomName: "jarvis",
		Sender: model.NoticeSender, SentAt: at, Text: text, Notice: true}); err != nil {
		t.Fatal(err)
	}
}

// A notice - a reminder mind rang, a job's news, a checklist shown - is mind's
// own output come back. The interpreter sees it, named as one, as context for
// what the operator says to it; a change resting on it alone is not believed,
// and the operator's answer to it is.
func TestANoticeIsContextNotEvidence(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, `{"changes":[
		{"op":"create","kind":"loop","set":{"title":"치과 예약 확인하기","owner":"me"},"evidence":[{"line":1,"quote":"치과 예약 확인하기"}],"confidence":0.9},
		{"op":"remember","kind":"fact","set":{"subject":"나","relation":"BOUGHT","object":"탁상시계","objectKind":"Thing","text":"나는 탁상시계를 샀다"},"evidence":[{"line":2,"quote":"탁상시계 주문을 마쳤어요"}],"confidence":0.9},
		{"op":"cancel","kind":"plan","ref":"P1","evidence":[{"line":3,"quote":"그거 취소됐어"}],"confidence":0.9,"why":"알림에 답함"}
	]}`)
	if _, err := c.world.Create(ctx, model.KindPlan, "r", model.State{Title: "치과", Status: model.Confirmed, Date: "2026-10-13", Start: "10:00"}, world.Meta{Actor: "interpreter"}); err != nil {
		t.Fatal(err)
	}
	notice(t, st, "chat:n1", "리마인드 · 내일 10시 치과 예약 확인하기", time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	notice(t, st, "chat:n2", "작업 소식 · 탁상시계 주문을 마쳤어요", time.Date(2026, 10, 12, 20, 2, 0, 0, seoul))
	say(t, st, "chat:u3", "그거 취소됐어. 병원에서 연락 왔어", true, time.Date(2026, 10, 12, 20, 3, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	p := script.Prompts[0]
	for _, want := range []string{"[1] 10/12(월) 20:01 Jarvis(알림): 리마인드", "[3] 10/12(월) 20:03 윤(나): 그거 취소됐어"} {
		if !strings.Contains(p, want) {
			t.Fatalf("프롬프트에 %q 가 없습니다:\n%s", want, p)
		}
	}
	if !strings.Contains(system, `"Jarvis(알림)"인 줄은 비서가 스스로 보낸 알림`) {
		t.Fatal("해석기 규칙에 알림이 있어야 합니다")
	}
	plans, _ := st.Entities(ctx, model.KindPlan, "", false)
	if len(plans) != 1 || plans[0].State.Status != model.Cancelled {
		t.Fatalf("운영자가 알림에 답한 말은 해석합니다: %+v", plans)
	}
	if loops, _ := st.Entities(ctx, model.KindLoop, "", false); len(loops) != 0 {
		t.Fatalf("알림만으로 고리를 만들지 않습니다: %+v", loops)
	}
	if facts, _ := st.Facts(ctx, 10); len(facts) != 0 {
		t.Fatalf("알림만으로 사실을 남기지 않습니다: %+v", facts)
	}
}

// Nothing but notices is nothing said: the room is marked read without
// asking the model, and the notices are there as context for the next read.
func TestNoticesAloneAreNotRead(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, `{"changes":[]}`)
	notice(t, st, "chat:n1", "리마인드 · 장갑 챙기기", time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if len(script.Prompts) != 0 {
		t.Fatal("알림만 있으면 모델을 부르지 않습니다")
	}
	rooms, _ := st.Rooms(ctx)
	if len(rooms) != 1 || rooms[0].Uninterpreted != 0 {
		t.Fatalf("읽은 것으로 표시해야 합니다: %+v", rooms)
	}
	say(t, st, "chat:u2", "다 챙겼어", true, time.Date(2026, 10, 12, 20, 4, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if len(script.Prompts) != 1 || !strings.Contains(script.Prompts[0], "Jarvis(알림): 리마인드 · 장갑 챙기기") || !strings.Contains(script.Prompts[0], "[1] 10/12(월) 20:04 윤(나): 다 챙겼어") {
		t.Fatalf("다음 읽기에 알림이 앞선 대화로 보여야 합니다: %v", script.Prompts)
	}
}

// A notice coming after what was said does not put its reading off; alone,
// it is only read (marked) after the usual quiet.
func TestANoticeDoesNotPutOffARead(t *testing.T) {
	st := store.NewMemory()
	clock := time.Date(2026, 10, 12, 20, 0, 0, 0, seoul)
	now := func() time.Time { return clock }
	c := NewConversation(Config{Owner: "윤", Idle: 3 * time.Minute}, st, world.New(st, now), &llm.Script{}, seoul, now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.Observe(model.Message{Ref: "chat:u1", Source: "chat", Room: "r", Sender: "지수", SentAt: clock, Text: "그럼 토요일에 봐"})
	want := c.Pending()["r"]
	clock = clock.Add(2 * time.Minute)
	c.Observe(model.Message{Ref: "chat:n2", Source: "chat", Room: "r", Sender: model.NoticeSender, Notice: true, SentAt: clock, Text: "작업 소식"})
	if got := c.Pending()["r"]; !got.Equal(want) {
		t.Fatalf("알림이 읽기를 미뤘습니다: %v, 원래 %v", got, want)
	}
	c.Observe(model.Message{Ref: "chat:n3", Source: "chat", Room: "q", Sender: model.NoticeSender, Notice: true, SentAt: clock, Text: "리마인드"})
	if _, ok := c.Pending()["q"]; !ok {
		t.Fatal("알림뿐인 방도 읽음 표시를 위해 한 번은 돕니다")
	}
}

// The check of the to-be structure: a read that ends twenty seconds after it
// began does not undo what a person did three seconds ago. The room is read
// again as it stands, and what the model then says of it is what counts.
func TestALateReadDoesNotUndoAPerson(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, "")
	e, err := c.world.Create(ctx, model.KindPlan, "r", model.State{Title: "지수와 강릉 여행", Status: model.Proposed, Date: "2026-10-17"}, world.Meta{Actor: "interpreter"})
	if err != nil {
		t.Fatal(err)
	}
	say(t, st, "sim:1", "강릉 말고 속초 가자", false, time.Date(2026, 10, 12, 20, 2, 0, 0, seoul))
	reads := 0
	script.Answer = func(_, _ string) (string, error) {
		reads++
		if reads == 1 {
			// While the model thinks, the operator says the plan is off.
			if _, _, err := c.world.Update(ctx, e.ID, func(s *model.State) bool { s.Status = model.Cancelled; return true }, world.Meta{Actor: "operator"}); err != nil {
				t.Fatal(err)
			}
		}
		return `{"changes":[{"op":"update","kind":"plan","ref":"P1","set":{"status":"confirmed","title":"지수와 속초 여행"},"evidence":[{"line":1,"quote":"속초 가자"}],"confidence":0.9}]}`, nil
	}
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Entity(ctx, e.ID)
	if got.State.Status != model.Cancelled || got.State.Title != "지수와 강릉 여행" {
		t.Fatalf("운영자가 한 일을 덮었습니다: %+v", got.State)
	}
	if due := c.Pending(); due["r"].IsZero() {
		t.Fatalf("다시 읽어야 합니다: %v", due)
	}
	marks, _ := st.Rooms(ctx)
	for _, m := range marks {
		if m.Room == "r" && !m.Interpreted.IsZero() {
			t.Fatalf("아직 읽은 것으로 치면 안 됩니다: %+v", m)
		}
	}
	// Read again as it stands: nothing moves under it now, so what it says holds.
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if reads != 2 {
		t.Fatalf("다시 읽기: %d", reads)
	}
	got, _ = st.Entity(ctx, e.ID)
	if got.State.Title != "지수와 속초 여행" {
		t.Fatalf("다시 읽은 것은 지금 상태 위에 적습니다: %+v", got.State)
	}
}

// Moved again under the second read: what moved is left, and the room is not
// read a third time.
func TestAReadIsRepeatedOnce(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, "")
	e, err := c.world.Create(ctx, model.KindPlan, "r", model.State{Title: "지수와 강릉 여행", Status: model.Proposed, Date: "2026-10-17"}, world.Meta{Actor: "interpreter"})
	if err != nil {
		t.Fatal(err)
	}
	say(t, st, "sim:1", "속초 가자", false, time.Date(2026, 10, 12, 20, 2, 0, 0, seoul))
	reads := 0
	script.Answer = func(_, _ string) (string, error) {
		reads++
		title := fmt.Sprintf("운영자가 고친 제목 %d", reads)
		if _, _, err := c.world.Update(ctx, e.ID, func(s *model.State) bool { s.Title = title; return true }, world.Meta{Actor: "operator"}); err != nil {
			t.Fatal(err)
		}
		return `{"changes":[{"op":"update","kind":"plan","ref":"P1","set":{"title":"지수와 속초 여행"},"evidence":[{"line":1,"quote":"속초 가자"}],"confidence":0.9}]}`, nil
	}
	for range 2 {
		if err := c.Read(ctx, "r"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := st.Entity(ctx, e.ID)
	if got.State.Title != "운영자가 고친 제목 2" {
		t.Fatalf("%+v", got.State)
	}
	if _, due := c.Pending()["r"]; due {
		t.Fatalf("세 번 읽지 않습니다: %v", c.Pending())
	}
}
