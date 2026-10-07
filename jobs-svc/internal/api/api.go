// Package api serves jobs. Same bearer scheme as the rest of the stack:
// everything but /health needs the token.
//
// Two callers. The console's 작업 tab lists jobs, shows one, and instructs,
// pauses, resumes, runs or cancels it. The agent creates jobs from
// conversation, takes the next due one, and - through the tools a background
// run calls - logs progress and decides what the job does next.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/jobs-svc/internal/store"
)

type Server struct {
	store      *store.Store
	token      string
	purgeAfter time.Duration
	log        *slog.Logger
}

func New(s *store.Store, token string, purgeAfter time.Duration, log *slog.Logger) *Server {
	return &Server{store: s, token: token, purgeAfter: purgeAfter, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	// 탭과 대화
	mux.HandleFunc("GET /jobs", s.auth(s.list))
	mux.HandleFunc("POST /jobs", s.auth(s.create))
	mux.HandleFunc("GET /jobs/{id}", s.auth(s.detail))
	mux.HandleFunc("POST /jobs/{id}/instruct", s.auth(s.instruct))
	mux.HandleFunc("POST /jobs/{id}/{action}", s.auth(s.control))
	// 에이전트: 스케줄러
	mux.HandleFunc("GET /due", s.auth(s.due))
	mux.HandleFunc("POST /jobs/{id}/runs", s.auth(s.startRun))
	mux.HandleFunc("POST /runs/orphans", s.auth(s.orphans))
	mux.HandleFunc("POST /runs/{id}/end", s.auth(s.endRun))
	mux.HandleFunc("POST /jobs/{id}/answer", s.auth(s.answer))
	// 에이전트: 실행 중인 작업의 도구
	mux.HandleFunc("POST /runs/{id}/step", s.auth(s.step))
	mux.HandleFunc("POST /runs/{id}/schedule", s.auth(s.schedule))
	mux.HandleFunc("POST /runs/{id}/wait", s.auth(s.wait))
	mux.HandleFunc("POST /runs/{id}/finish", s.auth(s.finish))
	mux.HandleFunc("POST /runs/{id}/report", s.auth(s.report))
	mux.HandleFunc("POST /runs/{id}/memory", s.auth(s.memory))
	mux.HandleFunc("POST /runs/{id}/record", s.auth(s.record))
	// 사이트 로그인, 사진 정리
	mux.HandleFunc("GET /sites", s.auth(s.sites))
	mux.HandleFunc("POST /sites/login", s.auth(s.siteLogin))
	mux.HandleFunc("GET /purge/due", s.auth(s.purgeDue))
	mux.HandleFunc("POST /jobs/{id}/purged", s.auth(s.purged))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "데이터베이스에 연결할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.List(r.Context())
	if err != nil {
		s.fail(w, "작업 목록을 읽지 못했습니다", err)
		return
	}
	sites, err := s.store.Sites(r.Context())
	if err != nil {
		s.fail(w, "사이트 상태를 읽지 못했습니다", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "blockedSites": sites})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in store.NewJob
	if !decode(w, r, &in) {
		return
	}
	in.Title, in.Goal = strings.TrimSpace(in.Title), strings.TrimSpace(in.Goal)
	if in.Title == "" || in.Goal == "" {
		writeErr(w, http.StatusBadRequest, "제목과 목표가 필요합니다.")
		return
	}
	j, err := s.store.Create(r.Context(), in)
	if err != nil {
		s.fail(w, "작업을 만들지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("events"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	d, err := s.store.Detail(r.Context(), id, limit, r.URL.Query().Get("steps") == "0")
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.fail(w, "작업을 읽지 못했습니다", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) instruct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeErr(w, http.StatusBadRequest, "지시가 비어 있습니다.")
		return
	}
	if err := s.store.Instruct(r.Context(), id, strings.TrimSpace(in.Text)); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	switch action {
	case "pause", "resume", "run", "cancel":
	default:
		writeErr(w, http.StatusNotFound, "그런 경로가 없습니다.")
		return
	}
	if err := s.store.Control(r.Context(), id, action); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Card string `json:"card"`
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.Answer(r.Context(), id, in.Card, in.Text); err != nil {
		s.fail(w, "답을 적지 못했습니다", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) due(w http.ResponseWriter, r *http.Request) {
	j, err := s.store.Due(r.Context())
	if err != nil {
		s.fail(w, "할 작업을 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": j})
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	runID, err := s.store.StartRun(r.Context(), id)
	if err != nil {
		s.fail(w, "실행을 시작하지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"runId": runID})
}

func (s *Server) orphans(w http.ResponseWriter, r *http.Request) {
	if err := s.store.CloseOrphans(r.Context()); err != nil {
		s.fail(w, "남은 실행을 정리하지 못했습니다", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) endRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := s.store.EndRun(r.Context(), id, in.Outcome, in.Note)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) step(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.reply(w, s.store.Step(r.Context(), id, in.Kind, in.Text))
}

func (s *Server) schedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		At     time.Time `json:"at"`
		Reason string    `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	at, err := s.store.Schedule(r.Context(), id, in.At, in.Reason)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]time.Time{"at": at})
}

func (s *Server) wait(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Card string `json:"card"`
		What string `json:"what"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.reply(w, s.store.Wait(r.Context(), id, in.Card, in.What))
}

func (s *Server) finish(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Summary string `json:"summary"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.reply(w, s.store.Finish(r.Context(), id, in.Summary))
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Summary string `json:"summary"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.reply(w, s.store.Report(r.Context(), id, in.Summary))
}

func (s *Server) memory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Key) == "" {
		writeErr(w, http.StatusBadRequest, "key 가 비어 있습니다.")
		return
	}
	s.reply(w, s.store.Remember(r.Context(), id, strings.TrimSpace(in.Key), in.Value))
}

func (s *Server) record(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.RecordInput
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Key) == "" || (!in.Remove && strings.TrimSpace(in.Title) == "") {
		writeErr(w, http.StatusBadRequest, "key 와 title 이 필요합니다.")
		return
	}
	s.reply(w, s.store.Record(r.Context(), id, in))
}

func (s *Server) sites(w http.ResponseWriter, r *http.Request) {
	sites, err := s.store.Sites(r.Context())
	if err != nil {
		s.fail(w, "사이트 상태를 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
}

func (s *Server) siteLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Site     string `json:"site"`
		Required bool   `json:"required"`
		Note     string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.SetLogin(r.Context(), in.Site, in.Required, in.Note); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) purgeDue(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.PurgeDue(r.Context(), s.purgeAfter)
	if err != nil {
		s.fail(w, "정리 대상을 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": ps})
}

func (s *Server) purged(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.reply(w, s.store.MarkPurged(r.Context(), id))
}

func (s *Server) reply(w http.ResponseWriter, err error) {
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) fail(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "err", err)
	writeErr(w, http.StatusInternalServerError, msg+".")
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "id 가 올바르지 않습니다.")
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil || json.Unmarshal(body, v) != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
