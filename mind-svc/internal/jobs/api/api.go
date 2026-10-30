// Package api is the jobs' part of mind's HTTP surface: reads at the window, and
// writes that reach here the way all of mind's do, by the stream (mind's
// api.Command).
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
	"github.com/choigonyok/jarvis/mind-svc/internal/jobs/store"
)

// Prefix is where the jobs are on mind's API.
const Prefix = "/api/jobs"

type Server struct {
	store      *store.Store
	purgeAfter time.Duration
	log        *slog.Logger
	// who is whose a request is, as mind's API says.
	who func(*http.Request) string
	// wake tells whose agent's scheduler a job may be due now.
	wake func(who string)
}

func New(s *store.Store, purgeAfter time.Duration, log *slog.Logger) *Server {
	return &Server{store: s, purgeAfter: purgeAfter, log: log, who: func(*http.Request) string { return store.Owner }}
}

// SetWho says how to tell whose a request is.
func (s *Server) SetWho(fn func(*http.Request) string) { s.who = fn }

// SetWake names what to call when a job may have come due by someone's
// hand - made, instructed, answered, run now, resumed, a site's login done.
// It reaches the agent's scheduler - the job's own person's agent - which
// otherwise looks only on its own long clock: the 작업 tab does not talk to it.
func (s *Server) SetWake(fn func(who string)) { s.wake = fn }

func (s *Server) woke(r *http.Request) {
	if s.wake != nil {
		s.wake(s.who(r))
	}
}

// noBrowser is why a guest's job may not name a website or a photo.
const noBrowser = "손님 작업은 웹사이트를 열거나 사진을 쓸 수 없어요 - 브라우저가 운영자 계정으로 로그인돼 있어서요."

// Health is the jobs' own /health, open like the service's was.
func (s *Server) Health() http.HandlerFunc { return s.health }

// Mount puts the routes on mind's mux: read for what only looks, write for
// what changes a job.
func (s *Server) Mount(read, write func(pattern string, h http.HandlerFunc)) {
	// 탭과 대화
	read("GET "+Prefix+"/jobs", s.list)
	write("POST "+Prefix+"/jobs", s.create)
	read("GET "+Prefix+"/jobs/{id}", s.job(s.detail))
	write("POST "+Prefix+"/jobs/{id}/instruct", s.job(s.instruct))
	write("POST "+Prefix+"/jobs/{id}/sites", s.job(s.addSites))
	write("POST "+Prefix+"/jobs/{id}/{action}", s.job(s.control))
	// 에이전트: 스케줄러
	read("GET "+Prefix+"/due", s.due)
	write("POST "+Prefix+"/jobs/{id}/runs", s.job(s.startRun))
	write("POST "+Prefix+"/runs/orphans", s.orphans)
	write("POST "+Prefix+"/runs/{id}/end", s.run(s.endRun))
	write("POST "+Prefix+"/jobs/{id}/answer", s.job(s.answer))
	// 에이전트: 실행 중인 작업의 도구
	write("POST "+Prefix+"/runs/{id}/step", s.run(s.step))
	write("POST "+Prefix+"/runs/{id}/schedule", s.run(s.schedule))
	write("POST "+Prefix+"/runs/{id}/wait", s.run(s.wait))
	write("POST "+Prefix+"/runs/{id}/finish", s.run(s.finish))
	write("POST "+Prefix+"/runs/{id}/report", s.run(s.report))
	write("POST "+Prefix+"/runs/{id}/memory", s.run(s.memory))
	write("POST "+Prefix+"/runs/{id}/record", s.run(s.record))
	// 사이트 로그인, 사진 정리
	read("GET "+Prefix+"/sites", s.sites)
	write("POST "+Prefix+"/sites/login", s.siteLogin)
	read("GET "+Prefix+"/purge/due", s.purgeDue)
	write("POST "+Prefix+"/jobs/{id}/purged", s.job(s.purged))
}

// job is a route on one job, the caller's own; another's is not found.
func (s *Server) job(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		s.mine(w, r, next, func(ctx context.Context) (string, error) { return s.store.WhoOf(ctx, id) }, "그런 작업이 없습니다.")
	}
}

// run is a route on one run, of one of the caller's own jobs.
func (s *Server) run(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		s.mine(w, r, next, func(ctx context.Context) (string, error) { return s.store.WhoOfRun(ctx, id) }, "그런 실행이 없습니다.")
	}
}

func (s *Server) mine(w http.ResponseWriter, r *http.Request, next http.HandlerFunc, whose func(context.Context) (string, error), missing string) {
	owner, err := whose(r.Context())
	switch {
	case errors.Is(err, store.ErrNotFound) || err == nil && owner != s.who(r):
		writeErr(w, http.StatusNotFound, missing)
	case err != nil:
		s.fail(w, "작업을 읽지 못했습니다", err)
	default:
		next(w, r)
	}
}

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
	jobs, err := s.store.List(r.Context(), s.who(r))
	if err != nil {
		s.fail(w, "작업 목록을 읽지 못했습니다", err)
		return
	}
	sites := []store.Site{}
	if s.who(r) == store.Owner {
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
	in.Who = s.who(r)
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
	if s.who(r) == store.Guest {
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
	case "pause", "resume", "run", "first", "cancel":
	default:
		writeErr(w, http.StatusNotFound, "그런 경로가 없습니다.")
		return
	}
	if err := s.store.Control(r.Context(), id, action); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if action == "run" || action == "resume" || action == "first" {
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

// CreateJob makes a job of the operator's (act.Jobs).
func (s *Server) CreateJob(ctx context.Context, title, goal, instructions string, sites []string) (int64, error) {
	j, err := s.store.Create(ctx, store.NewJob{Title: title, Goal: goal, Instructions: instructions, Sites: sites})
	if err == nil && s.wake != nil {
		s.wake(store.Owner)
	}
	return j.ID, err
}

// CancelJob ends one (act.Jobs).
func (s *Server) CancelJob(ctx context.Context, id int64) error {
	return s.store.Control(ctx, id, "cancel")
}

// ReadJobs decodes the operator's jobs into into, for the structured lane.
func (s *Server) ReadJobs(ctx context.Context, into any) error {
	jobs, err := s.store.List(ctx, store.Owner)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(jobs)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
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
	jobs, err := s.store.DueN(r.Context(), s.who(r), n)
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
	next, err := s.store.NextDue(r.Context(), s.who(r))
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
	if err := s.store.CloseOrphans(r.Context(), s.who(r)); err != nil {
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
	if s.who(r) == store.Guest {
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
	if s.who(r) == store.Guest {
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
	ps, err := s.store.PurgeDue(r.Context(), s.who(r), s.purgeAfter)
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
