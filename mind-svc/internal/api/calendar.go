package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
)

// The calendar as a person changes it on the screen. A new entry does not come
// this way: the calendar is shared, and the operator asked that every addition
// be approved on a card. What they typed goes to the adapter by the hand, as
// mind's own writes do, and the screens showing it hear what came of it.

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

// calendarWrite carries one change and answers as the calendar did.
func (s *Server) calendarWrite(w http.ResponseWriter, r *http.Request, method, op string, body []byte) {
	if s.calendar == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "캘린더가 없습니다."})
		return
	}
	target := strings.TrimRight(s.calendar.URL, "/") + "/events/" + url.PathEscape(r.PathValue("id"))
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
	if res.StatusCode == http.StatusOK && s.stream != nil {
		if err := s.stream.CalendarChanged(r.Context(), guest, op, json.RawMessage(out)); err != nil {
			s.m.Opts.Logger.Warn("일정이 바뀐 것을 화면에 알리지 못했습니다", "err", err)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(out)
}
