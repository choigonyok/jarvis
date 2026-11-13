package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
)

// The calendar as a person changes it on the screen: an entry they add on the
// calendar's form, change or take off. What they typed goes to the adapter by
// the hand, as mind's own writes do, and the screens showing it hear what came
// of it. The assistant's additions still go up on a card first - it is the
// assistant's guess, not what a person typed.
//
// The rest of the couple's space in uniple (the D-day, bucket lists, habit
// checks, the date map) goes the same way, to the same adapter.

// CalendarDoor is where a screen's change to the calendar goes.
type CalendarDoor struct {
	URL   string
	Token string
	// HTTP is the hand's client: a write leaves as an effect.
	HTTP *http.Client
}

// SetCalendar puts the calendar's fixed API behind the screens' changes.
func (s *Server) SetCalendar(door CalendarDoor) { s.calendar = &door }

func (s *Server) editEntry(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽지 못했습니다."})
		return
	}
	s.calendarWrite(w, r, http.MethodPatch, "upsert", body)
}

func (s *Server) removeEntry(w http.ResponseWriter, r *http.Request) {
	s.calendarWrite(w, r, http.MethodDelete, "delete", nil)
}

// addEntry is a new entry a person filled in on the calendar's form.
func (s *Server) addEntry(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽지 못했습니다."})
		return
	}
	// A new entry carries no id: the adapter makes one.
	var in map[string]any
	if json.Unmarshal(body, &in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽지 못했습니다."})
		return
	}
	delete(in, "id")
	body, _ = json.Marshal(in)
	s.forward(w, r, http.MethodPut, "/events", "upsert", body)
}

// coupleWrite carries a change to the couple's space to the adapter as it
// came; the adapter knows each block and refuses what is not one.
func (s *Server) coupleWrite(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Method != http.MethodDelete {
		var err error
		if body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽지 못했습니다."})
			return
		}
	}
	parts := strings.Split(r.PathValue("path"), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	s.forward(w, r, r.Method, "/couple/"+strings.Join(parts, "/"), "", body)
}

// calendarWrite carries one change and answers as the calendar did.
func (s *Server) calendarWrite(w http.ResponseWriter, r *http.Request, method, op string, body []byte) {
	s.forward(w, r, method, "/events/"+url.PathEscape(r.PathValue("id")), op, body)
}

// forward hands one write to the calendar's adapter and answers as it did. An
// entry that changed (op) is told to the screens showing the calendar.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, method, path, op string, body []byte) {
	if s.calendar == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "캘린더가 없습니다."})
		return
	}
	target := strings.TrimRight(s.calendar.URL, "/") + path
	req, err := http.NewRequestWithContext(r.Context(), method, target, strings.NewReader(string(body)))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.calendar.Token)
	guest := whoOf(r) == moments.Guest
	if guest {
		req.Header.Set("X-Jarvis-Side", "partner")
	}
	res, err := s.calendar.HTTP.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "캘린더에 쓰지 못했습니다: " + err.Error()})
		return
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if op != "" && res.StatusCode == http.StatusOK && s.stream != nil {
		if err := s.stream.CalendarChanged(r.Context(), guest, op, json.RawMessage(out)); err != nil {
			s.m.Opts.Logger.Warn("일정이 바뀐 것을 화면에 알리지 못했습니다", "err", err)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(out)
}
