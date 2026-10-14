package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/draft"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// reply indexes the moment a message opens or answers, and when someone
// writes after a long silence, offers a first reply drafted the operator's
// way (S3). When the operator then writes their own reply, how far it was
// from the draft is recorded - the numbers, not the text.
func (m *Mind) reply(ctx context.Context, msg model.Message) error {
	opened, gap, err := draft.Index(ctx, m.Opts.Store, msg)
	if err != nil {
		return err
	}
	if msg.IsMine {
		return m.compare(ctx, msg)
	}
	// Only a fresh message, in a conversation with one person.
	if !opened || m.Opts.Now().Sub(msg.SentAt) > 6*time.Hour || strings.HasSuffix(msg.Room, ":owner") || strings.ContainsAny(msg.RoomName, ",·&") {
		return nil
	}
	who := msg.Sender
	if who == "" {
		who = msg.RoomName
	}
	loc := m.Opts.Loc
	day := func(t time.Time) time.Time {
		t = t.In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
	days := int(math.Round(day(msg.SentAt).Sub(day(msg.SentAt.Add(-gap))).Hours() / 24))
	payload, _ := json.Marshal(map[string]string{"to": who, "purpose": fmt.Sprintf("%d일 만에 연락 온 상대에게 반갑게 첫 답장", days),
		"situation": "reconnect", "room": msg.Room, "incomingRef": msg.Ref})
	_, err = m.Gate.Submit(ctx, model.Intent{
		Skill: "reply", Kind: "message.draft", Topic: "reply:" + msg.Ref, Context: "reconnect", Payload: payload,
		Title:  fmt.Sprintf("%s님이 %d일 만에 연락했어요. 답장 초안 만들어 뒀어요", who, days),
		Reason: "오랜만의 연락", Confidence: 0.8, Value: 0.9, Reversible: true,
		Evidence: []model.Evidence{{Ref: msg.Ref, Quote: clip(msg.Text, 40)}},
	})
	return err
}

// compare records how the operator's own reply differs from the draft
// offered for that room in the last hours.
func (m *Mind) compare(ctx context.Context, msg model.Message) error {
	recent, err := m.Opts.Store.IntentsSince(ctx, msg.SentAt.Add(-12*time.Hour))
	if err != nil {
		return err
	}
	for _, in := range recent {
		if in.Kind != "message.draft" || in.Skill != "reply" || in.Status == model.Superseded {
			continue
		}
		var p struct {
			Room string `json:"room"`
			Text string `json:"text"`
		}
		if json.Unmarshal(in.Payload, &p) != nil || p.Room != msg.Room || p.Text == "" {
			continue
		}
		if len(in.Result) > 0 && strings.Contains(string(in.Result), "sentRef") {
			continue
		}
		sim := similarity(p.Text, msg.Text)
		in.Result, _ = json.Marshal(map[string]any{"sentRef": msg.Ref, "similarity": sim, "edited": sim < 0.95})
		if in.Status == model.Pending {
			at := m.Opts.Now()
			in.Status, in.DecidedBy, in.DecidedAt, in.Error = model.Superseded, "user", &at, "직접 답장함"
		}
		if err := m.Opts.Store.PutIntent(ctx, in); err != nil {
			return err
		}
	}
	return nil
}

// similarity is how alike two messages read: the overlap of their
// character pairs (1 the same, 0 nothing shared).
func similarity(a, b string) float64 {
	pairs := func(s string) map[string]bool {
		r := []rune(strings.Join(strings.Fields(s), ""))
		out := map[string]bool{}
		for i := 0; i+1 < len(r); i++ {
			out[string(r[i:i+2])] = true
		}
		return out
	}
	x, y := pairs(a), pairs(b)
	inter, union := 0, len(y)
	for k := range x {
		if y[k] {
			inter++
		} else {
			union++
		}
	}
	if union == 0 {
		return 1
	}
	return float64(int(float64(inter)/float64(union)*100)) / 100
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// RebuildReplies makes the reply-situation index again from the messages it
// is derived from - every room, on purpose.
func (m *Mind) RebuildReplies(ctx context.Context) (int, error) {
	st := m.Opts.Store
	if err := st.ResetReplySituations(ctx); err != nil {
		return 0, err
	}
	rooms, err := st.Rooms(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rooms {
		msgs, err := st.RoomMessages(ctx, r.Room, time.Time{}, time.Time{}, 1_000_000)
		if err != nil {
			return n, err
		}
		for i, msg := range msgs {
			if msg.IsMine {
				if err := st.AnswerReplySituations(ctx, msg.Room, msg.Ref); err != nil {
					return n, err
				}
				continue
			}
			if i == 0 || msg.SentAt.Sub(msgs[i-1].SentAt) < draft.Gap {
				continue
			}
			gap := msg.SentAt.Sub(msgs[i-1].SentAt)
			if err := st.AddReplySituation(ctx, model.ReplySituation{Kind: "reconnect", Room: msg.Room, InRef: msg.Ref,
				GapDays: int(gap.Hours() / 24), At: msg.SentAt}); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
