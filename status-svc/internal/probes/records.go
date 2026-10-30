package probes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

// The rows that come from what the stack says rather than from this list: a
// source that should be heard from (mind.registry, section source, against when
// it last had news) and a check a service said on the stream.

// source is a row of the registry's source section.
type source struct {
	ID   string   `json:"-"`
	Kind string   `json:"kind"`
	From string   `json:"from"`
	Warn duration `json:"warn"`
	Fail duration `json:"fail"`

	Group string `json:"group"`
	Name  string `json:"name"`
	OK    string `json:"ok"`
	Late  string `json:"late"`
	Gone  string `json:"gone"`
	Label string `json:"label"`

	Rule string   `json:"rule"`
	Fix  string   `json:"fix"`
	Deps []string `json:"deps"`
}

type duration time.Duration

func (d *duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = duration(v)
	return err
}

// Stack reads what the stack says.
type Stack struct {
	env *env

	mu sync.Mutex
	// sources is the last the registry said: a mind that is down cannot say,
	// and whether things are arriving does not stop mattering then.
	sources []source
}

// NewStack reads what the stack says with cfg.
func NewStack(cfg Config) *Stack {
	return &Stack{env: &env{Config: cfg, client: &http.Client{Timeout: 15 * time.Second}}}
}

func (s *Stack) readSources(ctx context.Context) []source {
	var out struct {
		Rows []struct {
			Section string          `json:"section"`
			Key     string          `json:"key"`
			Body    json.RawMessage `json:"body"`
		} `json:"rows"`
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.env.get(ctx, s.env.client, s.env.MindURL+"/api/registry", &out); err != nil {
		return s.sources
	}
	var list []source
	for _, r := range out.Rows {
		if r.Section != "source" {
			continue
		}
		var v source
		if err := json.Unmarshal(r.Body, &v); err != nil {
			continue
		}
		v.ID = r.Key
		list = append(list, v)
	}
	s.sources = list
	return list
}

// Rows is every row the stack says there is now; false when the way in could
// not be read, so that a restart of it does not empty the screen.
func (s *Stack) Rows(ctx context.Context) ([]check.Probe, bool) {
	var last struct {
		Sources []struct {
			Kind   string    `json:"kind"`
			Source string    `json:"source"`
			At     time.Time `json:"at"`
		} `json:"sources"`
		Checks []struct {
			ID   string          `json:"id"`
			At   time.Time       `json:"at"`
			Body json.RawMessage `json:"body"`
		} `json:"checks"`
	}
	if err := s.env.get(ctx, s.env.client, s.env.InputURL+"/last", &last); err != nil {
		return nil, false
	}
	seen := map[string]time.Time{}
	for _, l := range last.Sources {
		seen[l.Kind+"/"+l.Source] = l.At
	}
	var rows []check.Probe
	for _, v := range s.readSources(ctx) {
		rows = append(rows, sourceRow(v, seen))
	}
	for _, c := range last.Checks {
		var r checks.Result
		if json.Unmarshal(c.Body, &r) == nil && r.ID != "" {
			rows = append(rows, checkRow(r, c.At))
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, true
}

// sourceRow judges a source by how long it has been silent.
func sourceRow(v source, seen map[string]time.Time) check.Probe {
	at, heard := seen[v.Kind+"/"+v.From]
	return check.Probe{ID: v.ID, Group: v.Group, Name: v.Name, Rule: v.Rule, Fix: v.Fix, Deps: v.Deps, Every: 30 * time.Second,
		Run: func(context.Context) check.Verdict {
			if !heard {
				return check.Verdict{State: check.Unknown, Summary: "아직 받은 기록이 없어요."}
			}
			age := time.Since(at)
			when := at
			out := check.Verdict{ObservedAt: &when, ObservedLabel: v.Label}
			switch {
			case age > time.Duration(v.Fail):
				out.State, out.Summary = check.Fail, sentence(v.Gone, age)
			case age > time.Duration(v.Warn):
				out.State, out.Summary = check.Warn, sentence(v.Late, age)
			default:
				out.State, out.Summary = check.OK, v.OK
			}
			return out
		}}
}

func sentence(s string, age time.Duration) string {
	if strings.Contains(s, "%s") {
		return fmt.Sprintf(s, span(age))
	}
	return s
}

// checkRow shows what a service said. A service says it again on the beat, so a
// long silence means the one saying it stopped, and the last word is no longer
// known to be true.
func checkRow(r checks.Result, at time.Time) check.Probe {
	return check.Probe{ID: r.ID, Group: r.Group, Name: r.Name, Rule: r.Rule, Fix: r.Fix, Deps: r.Deps, Every: 30 * time.Second,
		Run: func(context.Context) check.Verdict {
			out := check.Verdict{State: check.State(r.State), Summary: r.Summary, ObservedAt: r.ObservedAt, ObservedLabel: r.ObservedLabel,
				Facts: r.Facts, Figure: r.Figure, Meter: r.Meter}
			if age := time.Since(at); age > 3*checks.Beat {
				out.State, out.Summary = check.Unknown, fmt.Sprintf("%s째 이 점검의 소식이 없어요. 마지막으로 들은 말: %s", span(age), r.Summary)
			}
			return out
		}}
}
