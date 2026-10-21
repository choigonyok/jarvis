package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/choigonyok/jarvis/mind-svc/internal/checklist"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
)

// The operator's checklists - what to pack, what to do on getting home -
// kept here with what is done (checklist), and shown in the conversation by
// the agent: when the conversation brings one up, at a reminder's moment
// (the reminder carries the list, and its card shows it as it stands then),
// or when mind itself puts one in front of the operator (ShowChecklist).

// ChecklistShown asks the agent to put a list in the conversation now, as a
// card: data {checklist, title, lead}.
const ChecklistShown = "checklist.shown"

// reminderList is the list a reminder carries, if it still has one.
func (m *Mind) reminderList(ctx context.Context, r moments.Reminder) (checklist.List, bool) {
	if r.Checklist == "" || m.Lists == nil {
		return checklist.List{}, false
	}
	l, err := m.Lists.Get(ctx, r.Checklist)
	if err != nil {
		m.Opts.Logger.Warn("알림에 붙은 체크리스트를 읽지 못했습니다", "reminder", r.ID, "checklist", r.Checklist, "err", err)
		return checklist.List{}, false
	}
	return l, true
}

// ShowChecklist has the agent put the list in the conversation now, under
// lead (a line on why now; empty is none).
func (m *Mind) ShowChecklist(ctx context.Context, id, lead string) (checklist.List, error) {
	if m.Lists == nil {
		return checklist.List{}, checklist.ErrNotFound
	}
	l, err := m.Lists.Get(ctx, id)
	if err != nil {
		return l, err
	}
	now := m.Opts.Now()
	data, _ := json.Marshal(map[string]string{"checklist": l.ID, "title": l.Title, "lead": strings.TrimSpace(lead)})
	return l, m.Opts.Log.Append(ctx, obs.Observation{
		Source: "mind", Type: ChecklistShown, Subject: l.ID, Data: data,
		Key: ChecklistShown + ":" + l.ID + ":" + strconv.FormatInt(now.UnixNano(), 36), OccurredAt: now,
	})
}

// checklistLine is the lists open, for the conversation's situation: what
// it answers "뭐 남았지?" from, and the ids its tools take.
func (m *Mind) checklistLine(ctx context.Context) string {
	if m.Lists == nil {
		return ""
	}
	ls, err := m.Lists.All(ctx, false)
	if err != nil {
		m.Opts.Logger.Warn("체크리스트를 읽지 못했습니다", "err", err)
		return ""
	}
	if len(ls) == 0 {
		return ""
	}
	var parts []string
	for i, l := range ls {
		if i == 6 {
			parts = append(parts, fmt.Sprintf("… 그 밖에 %d개", len(ls)-6))
			break
		}
		parts = append(parts, l.Line())
	}
	return "- 체크리스트(대화에 카드로 띄우려면 show_checklist, 고칠 때는 이 id 로): " + strings.Join(parts, " / ")
}
