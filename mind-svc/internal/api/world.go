package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
)

// The world model and mind's own word, for the conversation (the agent's look
// rows loops, plans and asked). Reads only, the operator's.

// loops: ?all=1 adds the loops closed in the last two weeks.
func (s *Server) loops(w http.ResponseWriter, r *http.Request) {
	out, err := s.m.Loops(r.Context(), r.URL.Query().Get("all") == "1")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"loops": out})
}

// plans: ?from=&to= (YYYY-MM-DD), by default yesterday to two months on.
func (s *Server) plans(w http.ResponseWriter, r *http.Request) {
	now := s.m.Opts.Now().In(s.m.Opts.Loc)
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	for _, d := range []*string{&from, &to} {
		if *d == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", *d); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "날짜는 YYYY-MM-DD 로 주세요."})
			return
		}
	}
	if from == "" {
		from = now.AddDate(0, 0, -1).Format("2006-01-02")
	}
	if to == "" {
		to = now.AddDate(0, 2, 0).Format("2006-01-02")
	}
	if to < from {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to 가 from 보다 앞입니다."})
		return
	}
	out, err := s.m.Plans(r.Context(), from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "plans": out})
}

// asked: ?days= (1~30, by default 7) of what mind asked and told.
func (s *Server) asked(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 30 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "days 는 1~30 입니다."})
			return
		}
		days = n
	}
	out, err := s.m.Asked(r.Context(), s.m.Opts.Now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "asked": out})
}

// rules is what the operator told the assistant to keep to.
func (s *Server) rules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.m.Rules(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type rule struct {
		ID    string    `json:"id"`
		Text  string    `json:"text"`
		Why   string    `json:"why,omitempty"`
		Since time.Time `json:"since"`
	}
	out := make([]rule, 0, len(rules))
	for _, n := range rules {
		out = append(out, rule{ID: n.ID, Text: n.Text, Why: n.Reason, Since: n.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

// setRule keeps a rule the operator gave in the conversation: {text, said?}.
// It comes as a command (the agent's change board), like every write here.
func (s *Server) setRule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
		Said string `json:"said"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	n, made, err := s.m.SetRule(r.Context(), in.Text, in.Said)
	switch {
	case mind.BadRule(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"rule": n, "new": made})
	}
}

// attention is when the operator answers mind's cards, by the hour of the
// day: the usual rate, each hour's, whether now is an hour they answer late at
// (the gate holds what is worth less then), and the evening hour they answer
// most at - what the notification settings offer for the digest.
func (s *Server) attention(w http.ResponseWriter, r *http.Request) {
	p := s.m.Rhythm(r.Context())
	out := map[string]any{"cards": p.Cards, "answered": p.Answered, "overall": p.Overall, "known": p.Known(), "hours": p.Hours}
	if slow, why := p.Slow(s.m.Opts.Now()); slow {
		out["slowNow"] = why
	}
	if h, ok := p.Evening(); ok {
		out["evening"] = h
	}
	writeJSON(w, http.StatusOK, out)
}
