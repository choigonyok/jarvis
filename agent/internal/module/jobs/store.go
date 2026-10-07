package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Store is the client to jobs-svc.
type Store struct {
	base   string
	token  string
	client *http.Client
}

func NewStore(baseURL, token string) *Store {
	return &Store{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

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
	CreatedAt    time.Time  `json:"createdAt"`
	Running      bool       `json:"running"`
	BlockedSites []string   `json:"blockedSites,omitempty"`
	Records      int        `json:"records"`
}

type Event struct {
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type Record struct {
	Key     string           `json:"key"`
	Title   string           `json:"title"`
	Status  string           `json:"status"`
	Amount  *int64           `json:"amount,omitempty"`
	Metrics map[string]int64 `json:"metrics"`
	Image   string           `json:"image,omitempty"`
	URL     string           `json:"url,omitempty"`
	Note    string           `json:"note,omitempty"`
}

type Inbox struct {
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

type NewJob struct {
	Title        string   `json:"title"`
	Goal         string   `json:"goal"`
	Instructions string   `json:"instructions"`
	Sites        []string `json:"sites"`
	Photos       []string `json:"photos"`
}

type Ended struct {
	JobID  int64  `json:"jobId"`
	Title  string `json:"title"`
	Failed bool   `json:"failed"`
	Note   string `json:"note,omitempty"`
}

type RecordInput struct {
	Key     string           `json:"key"`
	Title   string           `json:"title"`
	Status  string           `json:"status"`
	Amount  *int64           `json:"amount,omitempty"`
	Metrics map[string]int64 `json:"metrics,omitempty"`
	Image   string           `json:"image,omitempty"`
	URL     string           `json:"url,omitempty"`
	Note    string           `json:"note,omitempty"`
	Remove  bool             `json:"remove,omitempty"`
}

type Purge struct {
	ID     int64    `json:"id"`
	Photos []string `json:"photos"`
}

func (s *Store) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("작업 서비스에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
			return errors.New(payload.Error)
		}
		return fmt.Errorf("작업 서비스가 거부했습니다: %s", res.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (s *Store) Create(ctx context.Context, n NewJob) (Job, error) {
	var j Job
	err := s.call(ctx, http.MethodPost, "/jobs", n, &j)
	return j, err
}

func (s *Store) List(ctx context.Context) ([]Job, error) {
	var payload struct {
		Jobs []Job `json:"jobs"`
	}
	err := s.call(ctx, http.MethodGet, "/jobs", nil, &payload)
	return payload.Jobs, err
}

// Detail without per-tool steps: what a run or the chat needs, not the
// moment-by-moment log.
func (s *Store) Detail(ctx context.Context, id int64) (Detail, error) {
	var d Detail
	err := s.call(ctx, http.MethodGet, fmt.Sprintf("/jobs/%d?events=40&steps=0", id), nil, &d)
	return d, err
}

func (s *Store) Instruct(ctx context.Context, id int64, text string) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/jobs/%d/instruct", id), map[string]string{"text": text}, nil)
}

func (s *Store) Answer(ctx context.Context, id int64, card, text string) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/jobs/%d/answer", id), map[string]string{"card": card, "text": text}, nil)
}

func (s *Store) Due(ctx context.Context) (*Job, error) {
	var payload struct {
		Job *Job `json:"job"`
	}
	err := s.call(ctx, http.MethodGet, "/due", nil, &payload)
	return payload.Job, err
}

func (s *Store) StartRun(ctx context.Context, jobID int64) (int64, error) {
	var payload struct {
		RunID int64 `json:"runId"`
	}
	err := s.call(ctx, http.MethodPost, fmt.Sprintf("/jobs/%d/runs", jobID), map[string]any{}, &payload)
	return payload.RunID, err
}

func (s *Store) CloseOrphans(ctx context.Context) error {
	return s.call(ctx, http.MethodPost, "/runs/orphans", map[string]any{}, nil)
}

func (s *Store) EndRun(ctx context.Context, runID int64, outcome, note string) (Ended, error) {
	var out Ended
	err := s.call(ctx, http.MethodPost, fmt.Sprintf("/runs/%d/end", runID),
		map[string]string{"outcome": outcome, "note": note}, &out)
	return out, err
}

func (s *Store) Step(ctx context.Context, runID int64, kind, text string) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/runs/%d/step", runID), map[string]string{"kind": kind, "text": text}, nil)
}

func (s *Store) Schedule(ctx context.Context, runID int64, at time.Time, reason string) (time.Time, error) {
	var payload struct {
		At time.Time `json:"at"`
	}
	err := s.call(ctx, http.MethodPost, fmt.Sprintf("/runs/%d/schedule", runID),
		map[string]any{"at": at, "reason": reason}, &payload)
	return payload.At, err
}

func (s *Store) Wait(ctx context.Context, runID int64, card, what string) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/runs/%d/wait", runID), map[string]string{"card": card, "what": what}, nil)
}

func (s *Store) Finish(ctx context.Context, runID int64, summary string) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/runs/%d/finish", runID), map[string]string{"summary": summary}, nil)
}

func (s *Store) Report(ctx context.Context, runID int64, summary string) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/runs/%d/report", runID), map[string]string{"summary": summary}, nil)
}

func (s *Store) Remember(ctx context.Context, runID int64, key, value string) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/runs/%d/memory", runID), map[string]string{"key": key, "value": value}, nil)
}

func (s *Store) Record(ctx context.Context, runID int64, r RecordInput) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/runs/%d/record", runID), r, nil)
}

func (s *Store) SetLogin(ctx context.Context, site string, required bool, note string) error {
	return s.call(ctx, http.MethodPost, "/sites/login", map[string]any{"site": site, "required": required, "note": note}, nil)
}

func (s *Store) PurgeDue(ctx context.Context) ([]Purge, error) {
	var payload struct {
		Jobs []Purge `json:"jobs"`
	}
	err := s.call(ctx, http.MethodGet, "/purge/due", nil, &payload)
	return payload.Jobs, err
}

func (s *Store) MarkPurged(ctx context.Context, id int64) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/jobs/%d/purged", id), map[string]any{}, nil)
}
