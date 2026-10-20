package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
)

// The reminders: what the operator asked to be told at a moment, as the
// conversation sets, changes and cancels them. Times are the operator's
// clock - "2026-10-24T08:00" - or RFC 3339.

type reminderIn struct {
	Text   *string  `json:"text"`
	Body   *string  `json:"body"`
	At     string   `json:"at"`
	Place  *string  `json:"place"`
	Move   *string  `json:"move"`
	After  string   `json:"after"`
	Until  string   `json:"until"`
	URL    string   `json:"url"`
	Source string   `json:"source"`
	Clear  []string `json:"clear"`
}

// reminderOut is a reminder with its moment in words, what the conversation
// tells the operator.
type reminderOut struct {
	moments.Reminder
	When string `json:"when"`
	Line string `json:"line"`
}

func (s *Server) reminderOut(r moments.Reminder) reminderOut {
	return reminderOut{Reminder: r, When: r.When(s.m.Opts.Loc), Line: r.Line(s.m.Opts.Loc)}
}

func (s *Server) moments(w http.ResponseWriter) (*moments.Service, bool) {
	if s.m.Moments == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "알림을 둘 곳이 없습니다."})
		return nil, false
	}
	return s.m.Moments, true
}

func (s *Server) reminderFail(w http.ResponseWriter, err error) {
	var bad moments.ErrInvalid
	switch {
	case errors.As(err, &bad):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, moments.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// times reads the times given; an empty one is not given.
func (s *Server) times(in reminderIn) (at, after, until *time.Time, err error) {
	read := func(v string) (*time.Time, error) {
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		t, err := moments.ParseTime(v, s.m.Opts.Loc)
		return &t, err
	}
	if at, err = read(in.At); err != nil {
		return
	}
	if after, err = read(in.After); err != nil {
		return
	}
	until, err = read(in.Until)
	return
}

func (s *Server) listReminders(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.moments(w)
	if !ok {
		return
	}
	rs, err := svc.List(r.Context(), r.URL.Query().Get("all") != "")
	if err != nil {
		s.reminderFail(w, err)
		return
	}
	out := make([]reminderOut, len(rs))
	for i, x := range rs {
		out[i] = s.reminderOut(x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminders": out})
}

func (s *Server) setReminder(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.moments(w)
	if !ok {
		return
	}
	var in reminderIn
	if !readJSON(w, r, &in) {
		return
	}
	at, after, until, err := s.times(in)
	if err != nil {
		s.reminderFail(w, err)
		return
	}
	// From outside, only the operator's own: the assistant's are made here.
	source := in.Source
	if source == "" {
		source = moments.Asked
	}
	if source != moments.Asked && source != moments.Console {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source 는 chat 이나 console 입니다."})
		return
	}
	rem := moments.Reminder{Source: source, At: at, After: after, Until: until, URL: in.URL}
	if in.Text != nil {
		rem.Text = *in.Text
	}
	if in.Body != nil {
		rem.Body = *in.Body
	}
	if in.Place != nil {
		rem.Place = *in.Place
	}
	if in.Move != nil {
		rem.Move = *in.Move
	}
	saved, err := svc.Set(r.Context(), rem)
	if err != nil {
		s.reminderFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminder": s.reminderOut(saved)})
}

func (s *Server) changeReminder(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.moments(w)
	if !ok {
		return
	}
	var in reminderIn
	if !readJSON(w, r, &in) {
		return
	}
	at, after, until, err := s.times(in)
	if err != nil {
		s.reminderFail(w, err)
		return
	}
	saved, err := svc.Change(r.Context(), r.PathValue("id"), moments.Change{
		Text: in.Text, Body: in.Body, At: at, Place: in.Place, Move: in.Move, After: after, Until: until, Clear: in.Clear,
	})
	if err != nil {
		s.reminderFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminder": s.reminderOut(saved)})
}

func (s *Server) cancelReminder(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.moments(w)
	if !ok {
		return
	}
	saved, err := svc.Cancel(r.Context(), r.PathValue("id"))
	if err != nil {
		s.reminderFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reminder": s.reminderOut(saved)})
}
