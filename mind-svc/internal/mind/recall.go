package mind

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Recollection is what the assistant knows that bears on a question, from
// both places it keeps things: the world model (plans and loops, exact) and
// long-term memory (facts ranked for the question, and people's profiles).
type Recollection struct {
	Query    string           `json:"query"`
	Facts    []memory.Hit     `json:"facts"`
	People   []memory.Profile `json:"people"`
	Entities []model.Entity   `json:"entities"`
}

// Recall is the one way in to what is known, for the console and for anyone
// else who asks: the world model's live plans and loops with these people
// (or naming the query), memory's facts for the query centred on the first
// person it knows, and those people's profiles.
func (m *Mind) Recall(ctx context.Context, q string, people []string, limit int) (Recollection, error) {
	r := Recollection{Query: q, Facts: []memory.Hit{}, People: []memory.Profile{}, Entities: []model.Entity{}}
	live, err := m.World.Entities(ctx, "", "", true)
	if err != nil {
		return r, err
	}
	for _, e := range live {
		if mentions(e, q, people) {
			r.Entities = append(r.Entities, e)
		}
	}
	if m.Opts.Memory == nil {
		return r, nil
	}
	nodes, err := m.nodesOf(ctx, people)
	if err != nil {
		return r, err
	}
	for _, id := range nodes {
		if p, err := m.Opts.Memory.Profile(ctx, id); err == nil {
			r.People = append(r.People, p)
		}
	}
	if q != "" {
		center := ""
		if len(nodes) > 0 {
			center = nodes[0]
		}
		if r.Facts, err = m.Opts.Memory.Search(ctx, q, center, limit); err != nil {
			return r, err
		}
	}
	return r, nil
}

func mentions(e model.Entity, q string, people []string) bool {
	for _, p := range people {
		if slices.Contains(e.State.People, p) || strings.Contains(e.State.Title, p) {
			return true
		}
	}
	return q != "" && (strings.Contains(q, e.State.Title) || (e.State.Place != "" && strings.Contains(q, e.State.Place)))
}

// nodesOf is the memory nodes for these names, where known.
func (m *Mind) nodesOf(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	aliases, err := m.Opts.Store.Aliases(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		for _, a := range aliases {
			if a.Name == n && !slices.Contains(out, a.Node) {
				out = append(out, a.Node)
			}
		}
	}
	return out, nil
}

// recallRoom is what memory holds that bears on a stretch of conversation,
// for the interpreter to read it with: centred on the person the room is
// named after when memory knows them. Facts memory holds as ended are left
// out - the interpreter needs what is true now, and names by id the ones a
// new fact ends.
func (m *Mind) recallRoom(ctx context.Context, room, roomName string, fresh []model.Message) []interpret.Memory {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	people := []string{roomName}
	for _, msg := range fresh {
		if !msg.IsMine && msg.Sender != "" && !slices.Contains(people, msg.Sender) {
			people = append(people, msg.Sender)
		}
	}
	nodes, _ := m.nodesOf(ctx, people)
	center := ""
	if len(nodes) > 0 {
		center = nodes[0]
	}
	var text strings.Builder
	text.WriteString(roomName)
	for _, msg := range fresh {
		if text.Len() > 600 {
			break
		}
		text.WriteString(" " + msg.Text)
	}
	hits, err := m.Opts.Memory.Search(ctx, text.String(), center, 8)
	if err != nil {
		m.Opts.Logger.Warn("기억을 찾지 못했습니다", "room", room, "err", err)
		return nil
	}
	var out []interpret.Memory
	for _, h := range hits {
		if !h.Ended() {
			out = append(out, interpret.Memory{ID: h.ID, Fact: h.Fact})
		}
	}
	return out
}

// recallPeople is a few facts about the people a plan is with, for a
// reminder: what is true now, at most three.
func (m *Mind) recallPeople(ctx context.Context, people []string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	nodes, err := m.nodesOf(ctx, people)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	var out []string
	for _, id := range nodes {
		p, err := m.Opts.Memory.Profile(ctx, id)
		if err != nil {
			continue
		}
		for _, f := range p.Facts {
			if !f.Ended && len(out) < 3 {
				out = append(out, f.Fact)
			}
		}
	}
	return out
}

// roomLabel is how a room reads in a card: "카카오톡 '지수' 대화".
func (m *Mind) roomLabel(ctx context.Context, room string) string {
	rooms, err := m.Opts.Store.Rooms(ctx)
	if err != nil {
		return "대화"
	}
	for _, r := range rooms {
		if r.Room != room {
			continue
		}
		app := map[string]string{"kakao": "카카오톡", "imessage": "iMessage", "chat": "jarvis", "sim": "시뮬레이터"}[r.Source]
		if app == "" {
			app = r.Source
		}
		return fmt.Sprintf("%s '%s' 대화", app, r.Name)
	}
	return "대화"
}
