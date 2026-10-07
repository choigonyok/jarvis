// Package store keeps jobs, their runs, what they remember and record, and
// which sites are waiting on a login, in Postgres.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/jobs-svc/internal/rules"
)

var ErrNotFound = errors.New("그런 작업이 없습니다.")

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 해석: %w", err)
	}
	cfg.MaxConns = 6
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("풀 생성: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("연결 확인: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

type Job struct {
	ID           int64      `json:"id"`
	Title        string     `json:"title"`
	Goal         string     `json:"goal"`
	Instructions string     `json:"instructions"`
	Sites        []string   `json:"sites"`
	Photos       []string   `json:"photos"`
	State        string     `json:"state"`
	NextAt       *time.Time `json:"nextAt,omitempty"`
	NextReason   string     `json:"nextReason,omitempty"`
	WaitingCard  string     `json:"waitingCard,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	Attempts     int        `json:"attempts"`
	LastRunAt    *time.Time `json:"lastRunAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	PhotosGone   bool       `json:"photosGone,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	// Running is set while a background run of this job is in flight, with
	// the last thing it did.
	Running     bool   `json:"running"`
	CurrentStep string `json:"currentStep,omitempty"`
	// BlockedSites are this job's sites that are waiting on a login.
	BlockedSites []string `json:"blockedSites,omitempty"`
	Records      int      `json:"records"`
}

const jobCols = `j.id, j.title, j.goal, j.instructions, j.sites, j.photos, j.state, j.next_at,
	j.next_reason, j.waiting_card, j.summary, j.attempts, j.last_run_at, j.finished_at,
	j.photos_purged_at is not null, j.created_at,
	exists (select 1 from job_runs r where r.job_id = j.id and r.ended_at is null),
	coalesce((select e.text from job_events e where e.job_id = j.id order by e.id desc limit 1), ''),
	coalesce((select array_agg(s.site) from job_sites s where s.login_required and s.site = any(j.sites)), '{}'),
	(select count(*) from job_records r where r.job_id = j.id)`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Title, &j.Goal, &j.Instructions, &j.Sites, &j.Photos, &j.State,
		&j.NextAt, &j.NextReason, &j.WaitingCard, &j.Summary, &j.Attempts, &j.LastRunAt,
		&j.FinishedAt, &j.PhotosGone, &j.CreatedAt, &j.Running, &j.CurrentStep, &j.BlockedSites, &j.Records)
	if j.Sites == nil {
		j.Sites = []string{}
	}
	if j.Photos == nil {
		j.Photos = []string{}
	}
	if !j.Running {
		j.CurrentStep = ""
	}
	return j, err
}

type NewJob struct {
	Title        string   `json:"title"`
	Goal         string   `json:"goal"`
	Instructions string   `json:"instructions"`
	Sites        []string `json:"sites"`
	Photos       []string `json:"photos"`
}

// Create stores a job due now: the first run is what works out the plan,
// including how often to come back.
func (s *Store) Create(ctx context.Context, n NewJob) (Job, error) {
	sites := []string{}
	for _, site := range n.Sites {
		if h := rules.Host(site); h != "" {
			sites = append(sites, h)
		}
	}
	if n.Photos == nil {
		n.Photos = []string{}
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		insert into jobs (title, goal, instructions, sites, photos, next_at, next_reason)
		values ($1, $2, $3, $4, $5, now(), '첫 실행')
		returning id`, n.Title, n.Goal, n.Instructions, sites, n.Photos).Scan(&id)
	if err != nil {
		return Job{}, err
	}
	s.event(ctx, id, nil, "created", "작업을 만들었습니다: "+n.Goal)
	return s.Get(ctx, id)
}

func (s *Store) Get(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `select `+jobCols+` from jobs j where j.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

// List returns live jobs first (running, waiting on a person, scheduled),
// then finished ones, newest first within each.
func (s *Store) List(ctx context.Context) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `select `+jobCols+` from jobs j
		order by case j.state when 'waiting' then 0 when 'active' then 1 when 'paused' then 2 else 3 end,
		         j.created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

type Event struct {
	ID    int64     `json:"id"`
	RunID *int64    `json:"runId,omitempty"`
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
}

type Record struct {
	Key       string          `json:"key"`
	Title     string          `json:"title"`
	Status    string          `json:"status"`
	Amount    *int64          `json:"amount,omitempty"`
	Metrics   json.RawMessage `json:"metrics"`
	Image     string          `json:"image,omitempty"`
	URL       string          `json:"url,omitempty"`
	Note      string          `json:"note,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type Inbox struct {
	ID   int64     `json:"id"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type Detail struct {
	Job     Job               `json:"job"`
	Events  []Event           `json:"events"`
	Records []Record          `json:"records"`
	Memory  map[string]string `json:"memory"`
	Inbox   []Inbox           `json:"inbox"`
}

// Detail is one job with everything a person or the next run needs to know.
// events bounds how much history comes along.
func (s *Store) Detail(ctx context.Context, id int64, events int, skipSteps bool) (Detail, error) {
	j, err := s.Get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Job: j, Events: []Event{}, Records: []Record{}, Memory: map[string]string{}, Inbox: []Inbox{}}

	filter := ""
	if skipSteps {
		filter = " and kind <> 'step'"
	}
	rows, err := s.pool.Query(ctx, `select id, run_id, kind, text, at from job_events
		where job_id = $1`+filter+` order by id desc limit $2`, id, events)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.RunID, &e.Kind, &e.Text, &e.At); err != nil {
			rows.Close()
			return d, err
		}
		d.Events = append(d.Events, e)
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `select key, title, status, amount, metrics, image, url, note, updated_at
		from job_records where job_id = $1 order by updated_at desc`, id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.Key, &r.Title, &r.Status, &r.Amount, &r.Metrics, &r.Image, &r.URL, &r.Note, &r.UpdatedAt); err != nil {
			rows.Close()
			return d, err
		}
		d.Records = append(d.Records, r)
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `select key, value from job_memory where job_id = $1 order by key`, id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return d, err
		}
		d.Memory[k] = v
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `select id, kind, text, at from job_inbox
		where job_id = $1 and consumed_run is null order by id`, id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var in Inbox
		if err := rows.Scan(&in.ID, &in.Kind, &in.Text, &in.At); err != nil {
			return d, err
		}
		d.Inbox = append(d.Inbox, in)
	}
	return d, rows.Err()
}

func (s *Store) event(ctx context.Context, jobID int64, runID *int64, kind, text string) {
	_, _ = s.pool.Exec(ctx, `insert into job_events (job_id, run_id, kind, text) values ($1, $2, $3, $4)`,
		jobID, runID, kind, clamp(text, 2000))
}

// --- what a person does ------------------------------------------------------

// Instruct hands a job a new instruction and wakes it now. A finished or
// failed job comes back to life for it; a cancelled one does not.
func (s *Store) Instruct(ctx context.Context, id int64, text string) error {
	// A job waiting on a card still runs for a new instruction - the
	// instruction may be the answer to what the card asked.
	tag, err := s.pool.Exec(ctx, `
		update jobs set state = 'active', next_at = now(), next_reason = '새 지시', attempts = 0,
		       finished_at = null, updated_at = now()
		 where id = $1 and state <> 'cancelled'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("그만둔 작업에는 지시할 수 없습니다.")
	}
	if _, err := s.pool.Exec(ctx, `insert into job_inbox (job_id, kind, text) values ($1, 'instruction', $2)`, id, text); err != nil {
		return err
	}
	s.event(ctx, id, nil, "instruction", "지시: "+text)
	return nil
}

// Control is pause, resume, run (now) or cancel.
func (s *Store) Control(ctx context.Context, id int64, action string) error {
	var q, note string
	switch action {
	case "pause":
		q, note = `update jobs set state = 'paused', updated_at = now() where id = $1 and state in ('active', 'waiting')`, "멈췄습니다"
	case "resume":
		q, note = `update jobs set state = 'active', next_at = now(), next_reason = '다시 시작', attempts = 0, updated_at = now()
			where id = $1 and state in ('paused', 'failed')`, "다시 시작했습니다"
	case "run":
		q, note = `update jobs set state = 'active', next_at = now(), next_reason = '지금 실행', updated_at = now()
			where id = $1 and state in ('active', 'paused')`, "지금 실행합니다"
	case "cancel":
		q, note = `update jobs set state = 'cancelled', next_at = null, finished_at = now(), updated_at = now()
			where id = $1 and state not in ('done', 'cancelled')`, "그만뒀습니다"
	default:
		return fmt.Errorf("모르는 조작입니다: %s", action)
	}
	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("지금 상태에서는 할 수 없습니다.")
	}
	s.event(ctx, id, nil, "control", note)
	return nil
}

// Answer records the operator's decision on a card a job raised and wakes
// the job if it was waiting on that card.
func (s *Store) Answer(ctx context.Context, id int64, card, text string) error {
	if _, err := s.pool.Exec(ctx, `insert into job_inbox (job_id, kind, text) values ($1, 'answer', $2)`, id, text); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		update jobs set state = 'active', waiting_card = '', next_at = now(), next_reason = '카드 결정', updated_at = now()
		 where id = $1 and state = 'waiting' and waiting_card = $2`, id, card); err != nil {
		return err
	}
	s.event(ctx, id, nil, "answer", text)
	return nil
}

// --- the scheduler -----------------------------------------------------------

// Due is the job most overdue whose sites are all logged in and which is not
// already running, or nil.
func (s *Store) Due(ctx context.Context) (*Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `select `+jobCols+` from jobs j
		where j.state = 'active' and j.next_at <= now()
		  and not exists (select 1 from job_sites s where s.login_required and s.site = any(j.sites))
		  and not exists (select 1 from job_runs r where r.job_id = j.id and r.ended_at is null)
		order by j.next_at limit 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// StartRun opens a run. Any run of this job left open by a crash is closed
// first, so a job can never be stuck "running".
func (s *Store) StartRun(ctx context.Context, jobID int64) (int64, error) {
	if _, err := s.pool.Exec(ctx, `update job_runs set ended_at = now(), outcome = 'error', note = '중단됨'
		where job_id = $1 and ended_at is null`, jobID); err != nil {
		return 0, err
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `insert into job_runs (job_id) values ($1) returning id`, jobID).Scan(&id); err != nil {
		return 0, err
	}
	_, err := s.pool.Exec(ctx, `update jobs set last_run_at = now() where id = $1`, jobID)
	s.event(ctx, jobID, &id, "run_start", "실행을 시작했습니다")
	return id, err
}

// CloseOrphans ends runs a previous agent process left open.
func (s *Store) CloseOrphans(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `update job_runs set ended_at = now(), outcome = 'yielded', note = '에이전트가 재시작됨'
		where ended_at is null`)
	return err
}

func (s *Store) runJob(ctx context.Context, runID int64) (int64, bool, error) {
	var jobID int64
	var open bool
	err := s.pool.QueryRow(ctx, `select job_id, ended_at is null from job_runs where id = $1`, runID).Scan(&jobID, &open)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, errors.New("그런 실행이 없습니다.")
	}
	return jobID, open, err
}

// Step is one line of progress from a run in flight.
func (s *Store) Step(ctx context.Context, runID int64, kind, text string) error {
	jobID, _, err := s.runJob(ctx, runID)
	if err != nil {
		return err
	}
	if kind == "" {
		kind = "step"
	}
	s.event(ctx, jobID, &runID, kind, text)
	return nil
}

// Ended is what the agent needs to know after a run is closed.
type Ended struct {
	JobID  int64  `json:"jobId"`
	Title  string `json:"title"`
	Failed bool   `json:"failed"`
	Note   string `json:"note,omitempty"`
}

// EndRun closes a run and applies the rules. On a good run the instructions
// and answers it was given are marked as read.
func (s *Store) EndRun(ctx context.Context, runID int64, outcome, note string) (Ended, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Ended{}, err
	}
	defer tx.Rollback(ctx)

	var jobID int64
	var decided bool
	var started time.Time
	err = tx.QueryRow(ctx, `update job_runs set ended_at = now(), outcome = $2, note = $3
		where id = $1 and ended_at is null returning job_id, decided, started_at`, runID, outcome, note).
		Scan(&jobID, &decided, &started)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ended{}, errors.New("이미 끝난 실행입니다.")
	}
	if err != nil {
		return Ended{}, err
	}
	var attempts int
	var state, title string
	if err := tx.QueryRow(ctx, `select attempts, state, title from jobs where id = $1 for update`, jobID).
		Scan(&attempts, &state, &title); err != nil {
		return Ended{}, err
	}
	out := Ended{JobID: jobID, Title: title}

	if outcome == rules.OK {
		if _, err := tx.Exec(ctx, `update job_inbox set consumed_run = $2
			where job_id = $1 and consumed_run is null and at <= $3`, jobID, runID, started); err != nil {
			return Ended{}, err
		}
	}

	// A person paused or cancelled it during the run: their call stands.
	if state == rules.Paused || state == rules.Cancelled {
		return out, tx.Commit(ctx)
	}

	end := rules.AfterRun(outcome, decided, attempts, note)
	switch {
	case end.Fail:
		_, err = tx.Exec(ctx, `update jobs set state = 'failed', attempts = $2, next_at = null,
			summary = $3, updated_at = now() where id = $1`, jobID, end.Attempts, "실패: "+end.Note)
		out.Failed, out.Note = true, end.Note
	case end.Retry:
		_, err = tx.Exec(ctx, `update jobs set attempts = $2, next_at = $3,
			next_reason = $4, updated_at = now() where id = $1`,
			jobID, end.Attempts, time.Now().Add(end.After), retryReason(outcome, end.Note))
	default:
		_, err = tx.Exec(ctx, `update jobs set attempts = 0, updated_at = now() where id = $1`, jobID)
	}
	if err != nil {
		return Ended{}, err
	}
	kind, text := "run_end", "실행을 마쳤습니다"
	switch {
	case outcome == rules.Yielded:
		text = "대화가 시작되어 잠시 멈췄습니다. 곧 다시 합니다"
	case end.Fail:
		kind, text = "error", "세 번 연달아 실패해 멈췄습니다: "+end.Note
	case end.Retry:
		kind, text = "error", "잘 끝나지 않았습니다(15분 뒤 다시): "+end.Note
	}
	if _, err := tx.Exec(ctx, `insert into job_events (job_id, run_id, kind, text) values ($1, $2, $3, $4)`,
		jobID, runID, kind, clamp(text, 2000)); err != nil {
		return Ended{}, err
	}
	return out, tx.Commit(ctx)
}

func retryReason(outcome, note string) string {
	if outcome == rules.Yielded {
		return "대화 뒤에 이어서"
	}
	return "다시 시도: " + clamp(note, 100)
}

// --- what a run decides (through its tools) ---------------------------------

func (s *Store) decide(ctx context.Context, runID int64, q string, args ...any) (int64, error) {
	jobID, open, err := s.runJob(ctx, runID)
	if err != nil {
		return 0, err
	}
	if !open {
		return 0, errors.New("이미 끝난 실행입니다.")
	}
	if _, err := s.pool.Exec(ctx, q, append([]any{jobID}, args...)...); err != nil {
		return 0, err
	}
	_, err = s.pool.Exec(ctx, `update job_runs set decided = true where id = $1`, runID)
	return jobID, err
}

// Schedule sets when this job runs next.
func (s *Store) Schedule(ctx context.Context, runID int64, at time.Time, reason string) (time.Time, error) {
	at = rules.ClampNext(time.Now(), at)
	jobID, err := s.decide(ctx, runID, `update jobs set state = 'active', next_at = $2, next_reason = $3,
		waiting_card = '', updated_at = now() where id = $1 and state in ('active', 'waiting')`, at, reason)
	if err == nil {
		s.event(ctx, jobID, &runID, "schedule", fmt.Sprintf("다음 확인 %s: %s", at.Local().Format("1/2 15:04"), reason))
	}
	return at, err
}

// Wait parks the job on a card; the card's decision wakes it (Answer).
func (s *Store) Wait(ctx context.Context, runID int64, card, what string) error {
	jobID, err := s.decide(ctx, runID, `update jobs set state = 'waiting', waiting_card = $2, next_at = null,
		next_reason = $3, updated_at = now() where id = $1 and state in ('active', 'waiting')`, card, what)
	if err == nil {
		s.event(ctx, jobID, &runID, "card", what)
	}
	return err
}

// Finish ends the job.
func (s *Store) Finish(ctx context.Context, runID int64, summary string) error {
	jobID, err := s.decide(ctx, runID, `update jobs set state = 'done', next_at = null, summary = $2,
		finished_at = now(), updated_at = now() where id = $1 and state in ('active', 'waiting')`, summary)
	if err == nil {
		s.event(ctx, jobID, &runID, "finish", "끝냈습니다: "+summary)
	}
	return err
}

// Report updates the job's one-line status and logs it.
func (s *Store) Report(ctx context.Context, runID int64, summary string) error {
	jobID, _, err := s.runJob(ctx, runID)
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `update jobs set summary = $2, updated_at = now() where id = $1`, jobID, summary); err != nil {
		return err
	}
	s.event(ctx, jobID, &runID, "report", summary)
	return nil
}

func (s *Store) Remember(ctx context.Context, runID int64, key, value string) error {
	jobID, _, err := s.runJob(ctx, runID)
	if err != nil {
		return err
	}
	if value == "" {
		_, err = s.pool.Exec(ctx, `delete from job_memory where job_id = $1 and key = $2`, jobID, key)
		return err
	}
	_, err = s.pool.Exec(ctx, `insert into job_memory (job_id, key, value) values ($1, $2, $3)
		on conflict (job_id, key) do update set value = excluded.value, updated_at = now()`, jobID, key, value)
	return err
}

type RecordInput struct {
	Key     string           `json:"key"`
	Title   string           `json:"title"`
	Status  string           `json:"status"`
	Amount  *int64           `json:"amount"`
	Metrics map[string]int64 `json:"metrics"`
	Image   string           `json:"image"`
	URL     string           `json:"url"`
	Note    string           `json:"note"`
	Remove  bool             `json:"remove"`
}

func (s *Store) Record(ctx context.Context, runID int64, r RecordInput) error {
	jobID, _, err := s.runJob(ctx, runID)
	if err != nil {
		return err
	}
	if r.Remove {
		_, err = s.pool.Exec(ctx, `delete from job_records where job_id = $1 and key = $2`, jobID, r.Key)
		return err
	}
	metrics, _ := json.Marshal(r.Metrics)
	if r.Metrics == nil {
		metrics = []byte("{}")
	}
	_, err = s.pool.Exec(ctx, `insert into job_records (job_id, key, title, status, amount, metrics, image, url, note)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (job_id, key) do update set title = excluded.title, status = excluded.status,
		       amount = excluded.amount, metrics = excluded.metrics,
		       image = case when excluded.image = '' then job_records.image else excluded.image end,
		       url = case when excluded.url = '' then job_records.url else excluded.url end,
		       note = excluded.note, updated_at = now()`,
		jobID, r.Key, r.Title, r.Status, r.Amount, metrics, r.Image, r.URL, r.Note)
	return err
}

// --- sites ---------------------------------------------------------------------

type Site struct {
	Site          string    `json:"site"`
	LoginRequired bool      `json:"loginRequired"`
	Note          string    `json:"note,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (s *Store) SetLogin(ctx context.Context, site string, required bool, note string) error {
	site = rules.Host(site)
	if site == "" {
		return errors.New("사이트가 비어 있습니다.")
	}
	_, err := s.pool.Exec(ctx, `insert into job_sites (site, login_required, note) values ($1, $2, $3)
		on conflict (site) do update set login_required = excluded.login_required, note = excluded.note, updated_at = now()`,
		site, required, note)
	return err
}

func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.pool.Query(ctx, `select site, login_required, note, updated_at from job_sites where login_required order by site`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Site{}
	for rows.Next() {
		var x Site
		if err := rows.Scan(&x.Site, &x.LoginRequired, &x.Note, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// --- photos --------------------------------------------------------------------

type Purge struct {
	ID     int64    `json:"id"`
	Photos []string `json:"photos"`
}

// PurgeDue lists jobs that ended more than after ago and still have photos.
func (s *Store) PurgeDue(ctx context.Context, after time.Duration) ([]Purge, error) {
	rows, err := s.pool.Query(ctx, `select id, photos from jobs
		where photos_purged_at is null and cardinality(photos) > 0
		  and state in ('done', 'cancelled', 'failed')
		  and coalesce(finished_at, updated_at) < $1`, time.Now().Add(-after))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Purge{}
	for rows.Next() {
		var p Purge
		if err := rows.Scan(&p.ID, &p.Photos); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) MarkPurged(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `update jobs set photos_purged_at = now() where id = $1`, id)
	return err
}

func clamp(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
