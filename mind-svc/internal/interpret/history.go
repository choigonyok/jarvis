package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// ErrUnreadable is an answer that was not the JSON asked for: the same batch
// fails the same way again, so the caller reads less of it.
var ErrUnreadable = errors.New("해석 결과를 읽지 못했습니다")

// sameSecondCut is where to cut a batch of at most size from msgs (which
// hold more): the next read starts after the last time read, so a cut
// between two messages sent in the same second would skip the second one.
func sameSecondCut(msgs []model.Message, size int) int {
	cut := size
	for cut > 1 && msgs[cut-1].SentAt.Equal(msgs[cut].SentAt) {
		cut--
	}
	return cut
}

// historyNote turns the reading of the past into one for facts only. What
// was agreed back then has happened or not: a plan from it would come back as
// a card about last week. What people are like, live, like and decided
// stays true until something says otherwise.
const historyNote = `
## 지난 대화 읽기
이 대화는 이미 지나간 과거다. plan·loop 는 쓰지 않고 fact 만 쓴다(지난 일정·약속은 기록하지 않는다).
그때 생긴 일(이사·이직·합격·산 물건·다녀온 곳)은 "지난 일 회상"이어도 fact 다.
validAt 에는 근거 메시지를 보낸 날짜(YYYY-MM-DD)를 쓴다. ends 는 쓰지 않는다 - 지난 말로 지금의 기억을 끝내지 않는다.
`

// ReadHistory reads, for facts only, at most size of a room's oldest
// messages sent after `after` and before `before` - a past the live reading
// never saw. It returns the time read through (the next call's `after`) and
// how many it read; none means the room's past is done. Nothing goes to the
// world model: no plan, no loop, no card. A fact without its own date takes
// the day of its evidence, so memory places it in time and does not let it
// end something learned later. facts is how many went into the ledger.
func (c *Conversation) ReadHistory(ctx context.Context, room, name string, after, before time.Time, size int) (through time.Time, read, facts int, err error) {
	c.reading.Lock()
	defer c.reading.Unlock()

	fresh, err := c.st.RoomMessagesFrom(ctx, room, after, before, size+1)
	if err != nil || len(fresh) == 0 {
		return after, 0, 0, err
	}
	if len(fresh) > size {
		fresh = fresh[:sameSecondCut(fresh, size)]
	}
	last := fresh[len(fresh)-1].SentAt
	// As live: nothing but notices is nothing said, read without the model.
	if !slices.ContainsFunc(fresh, func(m model.Message) bool { return !m.Notice }) {
		return last, len(fresh), 0, nil
	}

	earlier, err := c.st.RoomMessages(ctx, room, time.Time{}, fresh[0].SentAt, c.cfg.Context)
	if err != nil {
		return after, 0, 0, err
	}
	var remembered []Memory
	if c.cfg.Recall != nil {
		remembered = c.cfg.Recall(ctx, room, name, fresh)
	}
	answer, err := c.llm.Complete(llm.WithPurpose(ctx, llm.History), system, prompt(name, c.cfg.Owner, last, c.loc, nil, remembered, earlier, fresh)+historyNote)
	if err != nil && !errors.Is(err, llm.ErrNoAnswer) {
		return after, 0, 0, err
	}
	var ops []op
	if err == nil {
		ops, err = parse(answer)
	}
	if err != nil {
		return last, len(fresh), 0, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	day := map[string]string{}
	for _, m := range fresh {
		day[m.Ref] = m.SentAt.In(c.loc).Format("2006-01-02")
	}
	skip := func(why string) (bool, error) {
		c.log.Info("지난 대화의 해석 결과를 버립니다", "room", room, "op", "remember", "why", why)
		return false, nil
	}
	for _, o := range ops {
		if o.Kind != KindFact {
			continue
		}
		evidence := verify(o.Evidence, fresh)
		if len(evidence) == 0 {
			_, _ = skip("근거가 새 메시지에 없음")
			continue
		}
		if o.Confidence < c.cfg.MinConf {
			_, _ = skip(fmt.Sprintf("확신도 %.2f", o.Confidence))
			continue
		}
		o.Ends = nil
		if o.Set == nil {
			o.Set = map[string]json.RawMessage{}
		}
		var v string
		if raw, ok := o.Set["validAt"]; !ok || json.Unmarshal(raw, &v) != nil || !dateRE.MatchString(v) {
			o.Set["validAt"], _ = json.Marshal(day[evidence[len(evidence)-1].Ref])
		}
		ok, err := c.remember(ctx, room, o, evidence, fresh, remembered, skip)
		if err != nil {
			return after, 0, facts, err
		}
		if ok {
			facts++
		}
	}
	c.log.Info("지난 대화를 해석했습니다", "room", room, "messages", len(fresh), "facts", facts, "proposed", len(ops))
	return last, len(fresh), facts, nil
}
