package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/choigonyok/jarvis/mind-svc/internal/checklist"
)

// The checklists: what the conversation makes, changes and shows, and what
// a tap on a card checks.

// checklistOut is a list with itself in one line, what a tool answers with.
type checklistOut struct {
	checklist.List
	Line string `json:"line"`
}

func out(l checklist.List) checklistOut { return checklistOut{List: l, Line: l.Line()} }

// ownList is whether the list of the path is the caller's own; another's is
// answered as not found.
func (s *Server) ownList(w http.ResponseWriter, r *http.Request) bool {
	svc, ok := s.lists(w)
	if !ok {
		return false
	}
	l, err := svc.Get(r.Context(), r.PathValue("id"))
	if err == nil && l.Person() != whoOf(r) {
		err = checklist.ErrNotFound
	}
	if err != nil {
		s.listFail(w, err)
		return false
	}
	return true
}

func (s *Server) lists(w http.ResponseWriter) (*checklist.Service, bool) {
	if s.m.Lists == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "체크리스트를 둘 곳이 없습니다."})
		return nil, false
	}
	return s.m.Lists, true
}

func (s *Server) listFail(w http.ResponseWriter, err error) {
	var bad checklist.ErrInvalid
	switch {
	case errors.As(err, &bad):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, checklist.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func (s *Server) listChecklists(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lists(w)
	if !ok {
		return
	}
	res := []checklistOut{}
	// ?ids= is the lists the conversation's cards show, open or closed.
	if ids := strings.TrimSpace(r.URL.Query().Get("ids")); ids != "" {
		for i, id := range strings.Split(ids, ",") {
			if i == 100 {
				break
			}
			if l, err := svc.Get(r.Context(), strings.TrimSpace(id)); err == nil {
				if l.Person() == whoOf(r) {
					res = append(res, out(l))
				}
			} else if !errors.Is(err, checklist.ErrNotFound) {
				s.listFail(w, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"checklists": res})
		return
	}
	ls, err := svc.AllOf(r.Context(), whoOf(r), r.URL.Query().Get("all") != "")
	if err != nil {
		s.listFail(w, err)
		return
	}
	for _, l := range ls {
		res = append(res, out(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklists": res})
}

func (s *Server) makeChecklist(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.lists(w); !ok {
		return
	}
	var in checklist.New
	if !readJSON(w, r, &in) {
		return
	}
	in.Who = whoOf(r)
	l, err := s.m.MakeList(r.Context(), in)
	if err != nil {
		s.listFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": out(l)})
}

func (s *Server) getChecklist(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lists(w)
	if !ok || !s.ownList(w, r) {
		return
	}
	l, err := svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.listFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": out(l)})
}

func (s *Server) changeChecklist(w http.ResponseWriter, r *http.Request) {
	if !s.ownList(w, r) {
		return
	}
	var in checklist.Change
	if !readJSON(w, r, &in) {
		return
	}
	l, err := s.m.ChangeList(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.listFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": out(l)})
}

func (s *Server) checkItem(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lists(w)
	if !ok || !s.ownList(w, r) {
		return
	}
	var in struct {
		Done bool   `json:"done"`
		By   string `json:"by"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	by := in.By
	if by == "" {
		by = checklist.ByCard
	}
	l, err := svc.Set(r.Context(), r.PathValue("id"), r.PathValue("item"), in.Done, by)
	if err != nil {
		s.listFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": out(l)})
}

func (s *Server) closeChecklist(w http.ResponseWriter, r *http.Request) {
	if !s.ownList(w, r) {
		return
	}
	l, err := s.m.CloseList(r.Context(), r.PathValue("id"))
	if err != nil {
		s.listFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": out(l)})
}

func (s *Server) showChecklist(w http.ResponseWriter, r *http.Request) {
	if !s.ownList(w, r) {
		return
	}
	var in struct {
		Lead string `json:"lead"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	l, err := s.m.ShowChecklist(r.Context(), r.PathValue("id"), in.Lead)
	if err != nil {
		s.listFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklist": out(l)})
}
