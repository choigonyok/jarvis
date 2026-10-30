package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	shown := map[string]string{"checklist": l.ID, "title": l.Title, "lead": strings.TrimSpace(lead)}
	if l.Person() == checklist.Guest {
		shown["who"] = checklist.Guest // not the operator's conversation's
	}
	data, _ := json.Marshal(shown)
	return l, m.Opts.Log.Append(ctx, obs.Observation{
		Source: "mind", Type: ChecklistShown, Subject: l.ID, Data: data,
		Key: ChecklistShown + ":" + l.ID + ":" + strconv.FormatInt(now.UnixNano(), 36), OccurredAt: now,
	})
}

// MakeList makes a list - and a list for a day is brought up by mind
// itself the evening before (planList).
func (m *Mind) MakeList(ctx context.Context, n checklist.New) (checklist.List, error) {
	l, err := m.Lists.Make(ctx, n)
	if err == nil {
		m.planList(ctx, l)
	}
	return l, err
}

// ChangeList changes a list, and its moment with its day.
func (m *Mind) ChangeList(ctx context.Context, id string, c checklist.Change) (checklist.List, error) {
	l, err := m.Lists.Change(ctx, id, c)
	if err == nil && (c.Date != nil || c.Reopen) {
		m.planList(ctx, l)
	}
	return l, err
}

// CloseList closes a list, and its moment with it.
func (m *Mind) CloseList(ctx context.Context, id string) (checklist.List, error) {
	l, err := m.Lists.Archive(ctx, id)
	if err == nil {
		m.planList(ctx, l)
	}
	return l, err
}

// The evening before a list's day: from 18:00, on coming home (being home
// counts), at 21:30 at the latest.
const (
	listFrom   = 18 * time.Hour
	listLatest = 21*time.Hour + 30*time.Minute
)

// planList keeps a list's own moment: what is left, the evening before its
// day, at home - packing is done at home. None for a list with no day, one
// closed, or one made when that evening has already begun (it is in front
// of the operator already). Said only if something is left by then (say).
func (m *Mind) planList(ctx context.Context, l checklist.List) {
	if m.Moments == nil {
		return
	}
	topic := "checklist:" + l.ID
	cancel := func() {
		if err := m.Moments.CancelTopic(ctx, topic); err != nil {
			m.Opts.Logger.Warn("체크리스트의 알림을 거두지 못했습니다", "checklist", l.ID, "err", err)
		}
	}
	d, err := time.ParseInLocation("2006-01-02", l.Date, m.Opts.Loc)
	if l.Date == "" || err != nil || l.Status != checklist.Active {
		cancel()
		return
	}
	eve := d.AddDate(0, 0, -1)
	from, latest := eve.Add(listFrom), eve.Add(listLatest)
	if !from.After(m.Opts.Now()) {
		cancel()
		return
	}
	if _, err := m.Moments.Set(ctx, moments.Reminder{
		Source: moments.Lists, Topic: topic, Text: "내일 " + l.Title, Checklist: l.ID,
		Place: "home", Move: moments.Arrive, After: &from, At: &latest, URL: "/",
		Who: l.Who, // the guest's list, at the guest's home
	}); err != nil {
		m.Opts.Logger.Warn("체크리스트의 알림을 잡지 못했습니다", "checklist", l.ID, "err", err)
	}
}

// closeLists closes, at night, the lists done with: every item done a
// while ago (a list ticked off this evening stays open till the next
// night), or a day past. What the lists are and what is on them stays
// mind's to keep; this is what keeps the open ones the live ones.
func (m *Mind) closeLists(ctx context.Context) error {
	if m.Lists == nil {
		return nil
	}
	ls, err := m.Lists.AllOf(ctx, "", false) // the guest's too
	if err != nil {
		return err
	}
	now := m.Opts.Now()
	today := now.In(m.Opts.Loc).Format("2006-01-02")
	for _, l := range ls {
		finished := len(l.Items) > 0 && len(l.Left()) == 0 && now.Sub(l.UpdatedAt) >= 12*time.Hour
		past := l.Date != "" && l.Date < today
		if !finished && !past {
			continue
		}
		if _, err := m.CloseList(ctx, l.ID); err != nil {
			return err
		}
		m.Opts.Logger.Info("다 쓴 체크리스트를 닫았습니다", "checklist", l.ID, "title", l.Title, "finished", finished, "past", past)
	}
	return nil
}

// checklistLine is the lists open, for the conversation's situation: what
// it answers "뭐 남았지?" from, and the ids its tools take.
func (m *Mind) checklistLine(ctx context.Context) string {
	return m.checklistLineOf(ctx, checklist.Owner)
}

// checklistLineOf is checklistLine of who's lists.
func (m *Mind) checklistLineOf(ctx context.Context, who string) string {
	if m.Lists == nil {
		return ""
	}
	ls, err := m.Lists.AllOf(ctx, who, false)
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
	return "- 체크리스트(대화에 카드로 띄우려면 change 의 checklist.show, 고칠 때는 이 id 로): " + strings.Join(parts, " / ")
}
