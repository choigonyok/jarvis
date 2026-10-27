// Package api serves jobs. Same bearer scheme as the rest of the stack:
// everything but /health needs the token.
//
// Two callers. The console's 작업 tab lists jobs, shows one, and instructs,
// pauses, resumes, runs or cancels it. The agent creates jobs from
// conversation, takes the next due one, and - through the tools a background
// run calls - logs progress and decides what the job does next.
//
// Two people. The services' token is the operator's, the guest's token
// (the guest's agent's, and the gateway's for the guest's 작업 tab) the
// guest's: each sees, changes and runs only their own jobs - another's job
// is not found. A guest's job opens no website and takes no photo: the
// browser is the operator's, signed in as them.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/jobs-svc/internal/store"
)

type Server struct {
	store      *store.Store
	token      string
	guestToken string
	purgeAfter time.Duration
	log        *slog.Logger
	// wake tells whose agent's scheduler a job may be due now.
	wake func(who string)
}

func New(s *store.Store, token, guestToken string, purgeAfter time.Duration, log *slog.Logger) *Server {
	return &Server{store: s, token: token, guestToken: guestToken, purgeAfter: purgeAfter, log: log}
}

// SetWake names what to call when a job may have come due by someone's
// hand - made, instructed, answered, run now, resumed, a site's login done.
// It is the agent's scheduler - the job's own person's agent - which
// otherwise looks only on its own long clock: the 작업 tab talks to this
// service, not to the agent.
func (s *Server) SetWake(fn func(who string)) { s.wake = fn }

func (s *Server) woke(r *http.Request) {
	if s.wake != nil {
		go s.wake(who(r))
	}
}

// noBrowser is why a guest's job may not name a website or a photo.
const noBrowser = "손님 작업은 웹사이트를 열거나 사진을 쓸 수 없어요 - 브라우저가 운영자 계정으로 로그인돼 있어서요."

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	// 탭과 대화
	mux.HandleFunc("GET /jobs", s.auth(s.list))
	mux.HandleFunc("POST /jobs", s.auth(s.create))
	mux.HandleFunc("GET /jobs/{id}", s.job(s.detail))
	mux.HandleFunc("POST /jobs/{id}/instruct", s.job(s.instruct))
	mux.HandleFunc("POST /jobs/{id}/sites", s.job(s.addSites))
	mux.HandleFunc("POST /jobs/{id}/{action}", s.job(s.control))
	// 에이전트: 스케줄러
	mux.HandleFunc("GET /due", s.auth(s.due))
	mux.HandleFunc("POST /jobs/{id}/runs", s.job(s.startRun))
	mux.HandleFunc("POST /runs/orphans", s.auth(s.orphans))
	mux.HandleFunc("POST /runs/{id}/end", s.run(s.endRun))
	mux.HandleFunc("POST /jobs/{id}/answer", s.job(s.answer))
	// 에이전트: 실행 중인 작업의 도구
	mux.HandleFunc("POST /runs/{id}/step", s.run(s.step))
	mux.HandleFunc("POST /runs/{id}/schedule", s.run(s.schedule))
	mux.HandleFunc("POST /runs/{id}/wait", s.run(s.wait))
	mux.HandleFunc("POST /runs/{id}/finish", s.run(s.finish))
	mux.HandleFunc("POST /runs/{id}/report", s.run(s.report))
	mux.HandleFunc("POST /runs/{id}/memory", s.run(s.memory))
	mux.HandleFunc("POST /runs/{id}/record", s.run(s.record))
	// 사이트 로그인, 사진 정리
	mux.HandleFunc("GET /sites", s.auth(s.sites))
	mux.HandleFunc("POST /sites/login", s.auth(s.siteLogin))
	mux.HandleFunc("GET /purge/due", s.auth(s.purgeDue))
	mux.HandleFunc("POST /jobs/{id}/purged", s.job(s.purged))
	return mux
}

type caller struct{}

// who is whose the request is: the operator's (the services' token) or the
// guest's.
func who(r *http.Request) string {
	if w, ok := r.Context().Value(caller{}).(string); ok {
		return w
	}
	return store.Owner
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, as := r.Header.Get("Authorization"), store.Owner
		switch {
		case s.guestToken != "" && same(got, "Bearer "+s.guestToken):
			as = store.Guest
		case s.token != "" && !same(got, "Bearer "+s.token):
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), caller{}, as)))
	}
}

// job is a route on one job, the caller's own; another's is not found.
func (s *Server) job(next http.HandlerFunc) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		s.mine(w, r, next, func(ctx context.Context) (string, error) { return s.store.WhoOf(ctx, id) }, "그런 작업이 없습니다.")
	})
}

// run is a route on one run, of one of the caller's own jobs.
func (s *Server) run(next http.HandlerFunc) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		s.mine(w, r, next, func(ctx context.Context) (string, error) { return s.store.WhoOfRun(ctx, id) }, "그런 실행이 없습니다.")
	})
}

func (s *Server) mine(w http.ResponseWriter, r *http.Request, next http.HandlerFunc, whose func(context.Context) (string, error), missing string) {
	owner, err := whose(r.Context())
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && owner != who(r):
		writeErr(w, http.StatusNotFound, missing)
	case err != nil:
		s.fail(w, "작업을 읽지 못했습니다", err)
	default:
		next(w, r)
	}
}

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// overdueAfter is how long a job may wait past its time before the agents'
// schedulers count as not taking it. They start a job only between turns of
// the conversation, so a busy hour holds it up without anything being wrong.
const overdueAfter = 2 * time.Hour

// health answers in the stack's contract (gocommon/health): whether the
// agents' schedulers are taking what is due, and the operator's jobs that
// stopped after failing too often - the held box of the jobs.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.Ping(ctx); err != nil {
		health.Write(w, http.StatusServiceUnavailable, health.Report{})
		return
	}
	now := time.Now()
	sched := health.Loop{Name: "scheduler", Label: "작업 실행(에이전트의 스케줄러)", StaleSec: int(overdueAfter.Seconds()), StartedAt: now, LastOK: &now}
	n, oldest, err := s.store.Overdue(ctx)
	if err != nil {
		health.Write(w, http.StatusServiceUnavailable, health.Report{})
		return
	}
	if n > 0 && oldest != nil {
		// It was keeping up until the oldest of them came due.
		sched.Behind, sched.LastOK = oldest, oldest
		sched.LastError = fmt.Sprintf("때가 지난 작업 %d개가 실행되지 않고 있습니다", n)
	}
	rep := health.Report{Loops: []health.Loop{sched}}
	stopped, err := s.store.StoppedJobs(ctx)
	if err != nil {
		health.Write(w, http.StatusServiceUnavailable, rep)
		return
	}
	if len(stopped) > 0 {
		box := &health.Held{Count: len(stopped), Where: "작업 탭에서 확인하고 다시 시작하거나 그만두세요."}
		for i, j := range stopped {
			if box.Oldest == nil || j.At.Before(*box.Oldest) {
				at := j.At
				box.Oldest = &at
			}
			if i < health.ShownItems {
				box.Items = append(box.Items, health.Item{What: j.Title, Why: strings.TrimPrefix(j.Summary, "실패: "), At: j.At})
			}
		}
		rep.Held = box
	}
	health.Write(w, http.StatusOK, rep)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.List(r.Context(), who(r))
	if err != nil {
		s.fail(w, "작업 목록을 읽지 못했습니다", err)
		return
	}
	sites := []store.Site{}
	if who(r) == store.Owner {
		if sites, err = s.store.Sites(r.Context()); err != nil {
			s.fail(w, "사이트 상태를 읽지 못했습니다", err)
			return
		}
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
	in.Who = who(r)
	if in.Who == store.Guest && (len(in.Sites) > 0 || len(in.Photos) > 0) {
		writeErr(w, http.StatusBadRequest, noBrowser)
		return
	}
	j, err := s.store.Create(r.Context(), in)
	if err != nil {
		s.fail(w, "작업을 만들지 못했습니다", err)
		return
	}
	s.woke(r)
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
	s.woke(r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addSites(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Add []string `json:"add"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Add) == 0 || len(in.Add) > 20 {
		writeErr(w, http.StatusBadRequest, "더할 사이트를 1~20개 주세요.")
		return
	}
	if who(r) == store.Guest {
		writeErr(w, http.StatusForbidden, noBrowser)
		return
	}
	j, err := s.store.AddSites(r.Context(), id, in.Add)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "그런 작업이 없습니다.")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, j)
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
	if action == "run" || action == "resume" {
		s.woke(r)
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
		switch {
		case errors.Is(err, store.ErrEnded):
			// Gone: the agent tells the card so, and does not try again.
			writeErr(w, http.StatusGone, err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, err.Error())
		default:
			s.fail(w, "답을 적지 못했습니다", err)
		}
		return
	}
	s.woke(r)
	w.WriteHeader(http.StatusNoContent)
}

// due is the jobs whose turn it is, most overdue first: `job` is the first
// (what the scheduler read before it ran several at once), `jobs` up to
// ?limit= of them, and `next` when the soonest of the rest comes due by its
// own clock (null when none is on one) - so the scheduler sleeps until then.
func (s *Server) due(w http.ResponseWriter, r *http.Request) {
	n := 1
	if v := r.URL.Query().Get("limit"); v != "" {
		if k, err := strconv.Atoi(v); err == nil && k > 0 && k <= 20 {
			n = k
		}
	}
	jobs, err := s.store.DueN(r.Context(), who(r), n)
	if err != nil {
		s.fail(w, "할 작업을 읽지 못했습니다", err)
		return
	}
	var first *store.Job
	if len(jobs) > 0 {
		first = &jobs[0]
	}
	if jobs == nil {
		jobs = []store.Job{}
	}
	next, err := s.store.NextDue(r.Context(), who(r))
	if err != nil {
		s.fail(w, "다음 작업 시각을 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": first, "jobs": jobs, "next": next})
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
	if err := s.store.CloseOrphans(r.Context(), who(r)); err != nil {
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
	if who(r) == store.Guest {
		writeJSON(w, http.StatusOK, map[string]any{"sites": []store.Site{}})
		return
	}
	sites, err := s.store.Sites(r.Context())
	if err != nil {
		s.fail(w, "사이트 상태를 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
}

func (s *Server) siteLogin(w http.ResponseWriter, r *http.Request) {
	if who(r) == store.Guest {
		writeErr(w, http.StatusForbidden, noBrowser)
		return
	}
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
	if !in.Required {
		s.woke(r)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) purgeDue(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.PurgeDue(r.Context(), who(r), s.purgeAfter)
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
