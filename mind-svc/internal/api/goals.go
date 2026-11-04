package api

import (
	"errors"
	"net/http"

	"github.com/choigonyok/jarvis/mind-svc/internal/goals"
)

// The goals: what a person means to keep doing, and each day's word on it.
// Reads are here; a change comes as a record (written), each person's own.

// goalOut is a goal with where it stands.
type goalOut struct {
	goals.Goal
	Rule     string         `json:"rule"`
	Progress goals.Progress `json:"progress"`
	Line     string         `json:"line"`
}

func (s *Server) goalOut(r *http.Request, g goals.Goal, marks map[string]goals.Mark) goalOut {
	p := g.Of(marks, s.m.Goals.Today())
	return goalOut{Goal: g, Rule: g.Rule(), Progress: p, Line: g.Line(p)}
}

func (s *Server) goalFail(w http.ResponseWriter, err error) {
	var bad goals.ErrInvalid
	switch {
	case errors.As(err, &bad):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, goals.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// ownGoal is the goal of the path, when it is the caller's; another's is
// answered as not found.
func (s *Server) ownGoal(w http.ResponseWriter, r *http.Request) (goals.Goal, bool) {
	if s.m.Goals == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "목표를 둘 곳이 없습니다."})
		return goals.Goal{}, false
	}
	g, err := s.m.Goals.Get(r.Context(), r.PathValue("id"))
	if err == nil && g.Person() != whoOf(r) {
		err = goals.ErrNotFound
	}
	if err != nil {
		s.goalFail(w, err)
		return g, false
	}
	return g, true
}

func (s *Server) listGoals(w http.ResponseWriter, r *http.Request) {
	if s.m.Goals == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "목표를 둘 곳이 없습니다."})
		return
	}
	gs, err := s.m.Goals.AllOf(r.Context(), whoOf(r), r.URL.Query().Get("all") != "")
	if err != nil {
		s.goalFail(w, err)
		return
	}
	out := make([]goalOut, 0, len(gs))
	for _, g := range gs {
		marks, err := s.m.Goals.Marks(r.Context(), g.ID)
		if err != nil {
			s.goalFail(w, err)
			return
		}
		out = append(out, s.goalOut(r, g, marks))
	}
	writeJSON(w, http.StatusOK, map[string]any{"goals": out, "today": s.m.Goals.Today()})
}

func (s *Server) getGoal(w http.ResponseWriter, r *http.Request) {
	g, ok := s.ownGoal(w, r)
	if !ok {
		return
	}
	marks, err := s.m.Goals.Marks(r.Context(), g.ID)
	if err != nil {
		s.goalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": s.goalOut(r, g, marks), "stats": g.StatsOf(marks, s.m.Goals.Today())})
}

func (s *Server) makeGoal(w http.ResponseWriter, r *http.Request) {
	if s.m.Goals == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "목표를 둘 곳이 없습니다."})
		return
	}
	var in goals.New
	if !readJSON(w, r, &in) {
		return
	}
	in.Who = whoOf(r)
	g, err := s.m.Goals.Make(r.Context(), in)
	if err != nil {
		s.goalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": s.goalOut(r, g, nil)})
}

func (s *Server) changeGoal(w http.ResponseWriter, r *http.Request) {
	g, ok := s.ownGoal(w, r)
	if !ok {
		return
	}
	var in goals.Change
	if !readJSON(w, r, &in) {
		return
	}
	g, err := s.m.Goals.Change(r.Context(), g.ID, in)
	if err != nil {
		s.goalFail(w, err)
		return
	}
	marks, err := s.m.Goals.Marks(r.Context(), g.ID)
	if err != nil {
		s.goalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": s.goalOut(r, g, marks)})
}

func (s *Server) removeGoal(w http.ResponseWriter, r *http.Request) {
	g, ok := s.ownGoal(w, r)
	if !ok {
		return
	}
	if err := s.m.Goals.Remove(r.Context(), g.ID); err != nil {
		s.goalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": g.ID})
}

func (s *Server) markGoal(w http.ResponseWriter, r *http.Request) {
	g, ok := s.ownGoal(w, r)
	if !ok {
		return
	}
	var in struct {
		Day  string  `json:"day"`
		Mark *string `json:"mark"`
		// Of a goal of more than one a day: how many times that day, or how
		// many more.
		Count *int   `json:"count"`
		Add   int    `json:"add"`
		Note  string `json:"note"`
		By    string `json:"by"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	// An empty mark takes a day back, and no day is today: a body that lost its
	// mark on the way must not read as that.
	if in.Mark == nil && in.Count == nil && in.Add == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "기록(mark)이나 횟수(count, add)가 없습니다."})
		return
	}
	if in.By == "" {
		in.By = goals.ByCard
	}
	g, marks, err := s.m.Goals.CheckDay(r.Context(), g.ID, goals.Check{Day: in.Day, Mark: in.Mark, Count: in.Count, Add: in.Add, Note: in.Note, By: in.By})
	if err != nil {
		s.goalFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goal": s.goalOut(r, g, marks), "stats": g.StatsOf(marks, s.m.Goals.Today())})
}
