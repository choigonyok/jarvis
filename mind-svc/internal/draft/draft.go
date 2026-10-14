// Package draft writes the messages the operator may send - a reply, a
// request to settle, an apology for running late - the way the operator
// writes to that person. How someone writes is not extracted as a rule
// ("엄마에게는 ^^"); the draft is shown the operator's own recent messages
// to that person, and, for a kind of moment (a friend writing after months
// of silence), the operator's own first replies in such moments. The
// examples carry the style; the model only follows them.
package draft

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// System is the drafting instruction.
const System = `너는 운영자 대신 보낼 메시지 초안을 쓴다. 보내는 것은 운영자다.
- "예시"는 운영자가 실제로 보낸 메시지다. 예시의 말투를 그대로 따른다: 반말/존댓말, 이모티콘과 ㅋㅋ·ㅎㅎ·^^ 같은 표현을 쓰는 빈도, 문장 길이, 맞춤법 습관.
- 예시에 없는 꾸밈(새 이모지, 과한 인사)을 더하지 않는다.
- 반드시 들어가야 할 사실(금액, 시각, 이름)은 글자 그대로 넣는다.
- 초안 한 줄(또는 두 줄)만 출력한다. 따옴표나 설명 없이.`

type Drafter struct {
	St    store.Store
	LLM   llm.Client
	Owner string
	// Examples is how many of the operator's own messages to show.
	Examples int
}

// Draft writes one message.
func (d *Drafter) Draft(ctx context.Context, req act.DraftRequest) (string, error) {
	if d.LLM == nil || !d.LLM.Ready() {
		return "", fmt.Errorf("초안에 모델이 필요합니다")
	}
	examples, err := d.ExamplesFor(ctx, req)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "받는 사람: %s\n하려는 말: %s\n", req.To, req.Purpose)
	if len(req.Facts) > 0 {
		fmt.Fprintf(&b, "꼭 들어갈 사실: %s\n", strings.Join(req.Facts, " / "))
	}
	if req.Incoming == "" && req.IncomingRef != "" {
		if m, err := d.St.Message(ctx, req.IncomingRef); err == nil {
			req.Incoming = m.Text
		}
	}
	if req.Incoming != "" {
		fmt.Fprintf(&b, "상대가 보낸 메시지: %s\n", oneLine(req.Incoming))
	}
	b.WriteString("\n## 예시 (운영자가 실제로 보낸 메시지)\n")
	if len(examples) == 0 {
		b.WriteString("없음 - 짧고 자연스럽게\n")
	}
	for _, e := range examples {
		b.WriteString("- " + e + "\n")
	}
	out, err := d.LLM.Complete(ctx, System, b.String())
	if err != nil {
		return "", err
	}
	return strings.Trim(strings.TrimSpace(out), "\"“”"), nil
}

// ExamplesFor is the operator's own words to show: to this person first,
// then in moments like this one.
func (d *Drafter) ExamplesFor(ctx context.Context, req act.DraftRequest) ([]string, error) {
	n := d.Examples
	if n == 0 {
		n = 6
	}
	out := append([]string{}, req.Examples...)
	mine, err := d.ToPerson(ctx, req.To, req.Room, n)
	if err != nil {
		return nil, err
	}
	out = append(out, mine...)
	if req.Situation != "" {
		like, err := d.InMoment(ctx, req.Situation, req.Room, 5)
		if err != nil {
			return nil, err
		}
		out = append(out, like...)
	}
	return out, nil
}

// ToPerson is the operator's latest messages in the one-to-one room with
// the person (or in room, when given).
func (d *Drafter) ToPerson(ctx context.Context, person, room string, n int) ([]string, error) {
	if room == "" {
		rooms, err := d.St.Rooms(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rooms {
			if r.Name == person || strings.HasSuffix(r.Name, person) && !strings.ContainsAny(r.Name, ",·&") {
				room = r.Room
				break
			}
		}
	}
	if room == "" {
		return nil, nil
	}
	msgs, err := d.St.RoomMessages(ctx, room, time.Time{}, time.Time{}, 80)
	if err != nil {
		return nil, err
	}
	var out []string
	for i := len(msgs) - 1; i >= 0 && len(out) < n; i-- {
		if msgs[i].IsMine && strings.TrimSpace(msgs[i].Text) != "" {
			out = append([]string{oneLine(msgs[i].Text)}, out...)
		}
	}
	return out, nil
}

// InMoment is the operator's first replies in moments of a kind, from other
// rooms than this one.
func (d *Drafter) InMoment(ctx context.Context, kind, room string, n int) ([]string, error) {
	ex, err := d.St.ReplyExamples(ctx, kind, n+1)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ex {
		if e.Room == room || len(out) == n {
			continue
		}
		m, err := d.St.Message(ctx, e.OutRef)
		if err != nil || strings.TrimSpace(m.Text) == "" {
			continue
		}
		out = append(out, oneLine(m.Text))
	}
	return out, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Gap is how long a silence must be for a message to be a reconnection.
const Gap = 60 * 24 * time.Hour

// Index records the moments a message opens or answers, as it is observed:
// someone writing after a long silence opens a reconnection; the
// operator's next message in that room answers it. One query per message:
// the message before it.
func Index(ctx context.Context, st store.Store, msg model.Message) (opened bool, gap time.Duration, err error) {
	if msg.IsMine {
		return false, 0, st.AnswerReplySituations(ctx, msg.Room, msg.Ref)
	}
	prev, err := st.RoomMessages(ctx, msg.Room, time.Time{}, msg.SentAt, 1)
	if err != nil || len(prev) == 0 {
		return false, 0, err
	}
	gap = msg.SentAt.Sub(prev[len(prev)-1].SentAt)
	if gap < Gap {
		return false, gap, nil
	}
	return true, gap, st.AddReplySituation(ctx, model.ReplySituation{Kind: "reconnect", Room: msg.Room, InRef: msg.Ref,
		GapDays: int(gap.Hours() / 24), At: msg.SentAt})
}
