// Package convo tests the conversation as the operator meets it: a message
// sent to the real CLI on the real model, with the instructions, the skills,
// the tools as they read and the lines in front of the message all built by
// the agent's own code - and what came of it graded: the tools called, the
// cards raised, what must not have been done, what the answer says.
//
// What stands in is only what the tools give back: every backend answers from
// the scenario, so nothing here reaches the operator's data, and what is
// tested is the model's reading of the same words the operator's conversation
// gets. mind's interpreter has its own scenarios (mind-svc internal/eval);
// these are the conversation's. A change to the instructions, a skill or a
// tool's words is measured here before it reaches anyone (scripts/convo).
package convo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Scenario is one message and what must come of it.
type Scenario struct {
	File string `json:"-"`
	Name string `json:"name"`
	// Why is what it holds the conversation to: the incident, or the rule.
	Why string `json:"why"`
	// Say is the operator's message. Prompt, instead, is a turn nobody typed
	// (a card's decision, as the gate words it).
	Say    string `json:"say,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	// Before is the conversation so far, oldest first: what a new session is
	// handed as its recap.
	Before []Line `json:"before,omitempty"`
	// Situation is mind's "right now" ([지금 상황 · HH:MM] and its lines);
	// Rules the operator's rules; Waiting the cards still waiting, a line
	// each; Memory the facts long-term memory holds for the message.
	Situation string   `json:"situation,omitempty"`
	Rules     []string `json:"rules,omitempty"`
	Waiting   []string `json:"waiting,omitempty"`
	Memory    []string `json:"memory,omitempty"`
	// Answers are what the tools give back: by row of look or change
	// ("look:calendar", "change:reminder.set") or by tool ("create_job",
	// "search"). What is not here gets a plain stand-in.
	Answers map[string]string `json:"answers,omitempty"`
	Expect  Expect            `json:"expect"`
}

// Line is one turn of the conversation before the message.
type Line struct {
	Who  string `json:"who"` // 운영자 | Jarvis
	Text string `json:"text"`
	// Ago is how long before the message it was said ("12m"; default 10m).
	Ago string `json:"ago,omitempty"`
}

// Expect is what must and must not come of the message.
type Expect struct {
	// Calls must each be made; AnyOf, at least one of them; Never, none.
	Calls []Call `json:"calls,omitempty"`
	AnyOf []Call `json:"anyOf,omitempty"`
	Never []Call `json:"never,omitempty"`
	// NoTools: no tool at all, not even a lookup.
	NoTools bool `json:"noTools,omitempty"`
	// Says: the answer holds each - one of its "|" alternatives. SaysNot:
	// none of these.
	Says    []string `json:"says,omitempty"`
	SaysNot []string `json:"saysNot,omitempty"`
}

// Call is a tool call to look for. Tool is the tool's own name - look,
// change, create_event, create_job, search, Read - or "*" for any; What is
// look's or change's row. Args are what arguments must hold, by name ("*":
// any of them): "|" separates alternatives, and @today, @tomorrow,
// @yesterday, @next:<요일>, @nextweek:<요일> stand for dates counted from when
// the scenario ran.
type Call struct {
	Tool string            `json:"tool"`
	What string            `json:"what,omitempty"`
	Args map[string]string `json:"args,omitempty"`
}

func (c Call) String() string {
	s := c.Tool
	if c.What != "" {
		s += " " + c.What
	}
	if len(c.Args) > 0 {
		keys := make([]string, 0, len(c.Args))
		for k := range c.Args {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+"~"+c.Args[k])
		}
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	return s
}

// Load is every scenario in dir, by file name.
func Load(dir string) ([]Scenario, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	var out []Scenario
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var sc Scenario
		if err := json.Unmarshal(raw, &sc); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		sc.File = filepath.Base(f)
		if sc.Name == "" || (sc.Say == "") == (sc.Prompt == "") {
			return nil, fmt.Errorf("%s: name 과, say 와 prompt 중 하나가 있어야 합니다", sc.File)
		}
		out = append(out, sc)
	}
	return out, nil
}

// Recorded is one tool call the model made, as the stream said it.
type Recorded struct {
	// Name is the tool as the CLI names it (mcp__look__look, Read).
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Short is the tool's own name: what follows the server in an MCP tool's.
func (r Recorded) Short() string {
	if i := strings.LastIndex(r.Name, "__"); strings.HasPrefix(r.Name, "mcp__") && i >= 0 {
		return r.Name[i+2:]
	}
	return r.Name
}

// row is look's or change's row, and that row's arguments; for any other
// tool, "" and its input.
func (r Recorded) row() (string, map[string]any) {
	var in map[string]any
	_ = json.Unmarshal(r.Input, &in)
	if s := r.Short(); s == "look" || s == "change" {
		what, _ := in["what"].(string)
		args, _ := in["args"].(map[string]any)
		return what, args
	}
	return "", in
}

func (r Recorded) String() string {
	what, args := r.row()
	s := r.Short()
	if what != "" {
		s += " " + what
	}
	if len(args) > 0 {
		b, _ := json.Marshal(args)
		s += " " + string(b)
	}
	return s
}

var weekday = map[string]time.Weekday{"일": time.Sunday, "월": time.Monday, "화": time.Tuesday, "수": time.Wednesday, "목": time.Thursday, "금": time.Friday, "토": time.Saturday}

// resolve turns the date words of an expectation into dates, from now.
func resolve(want string, now time.Time) string {
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	switch {
	case want == "@today":
		return day(now)
	case want == "@tomorrow":
		return day(now.AddDate(0, 0, 1))
	case want == "@yesterday":
		return day(now.AddDate(0, 0, -1))
	case strings.HasPrefix(want, "@next:"):
		wd, ok := weekday[strings.TrimPrefix(want, "@next:")]
		if !ok {
			return want
		}
		d := now.AddDate(0, 0, 1)
		for d.Weekday() != wd {
			d = d.AddDate(0, 0, 1)
		}
		return day(d)
	case strings.HasPrefix(want, "@nextweek:"):
		wd, ok := weekday[strings.TrimPrefix(want, "@nextweek:")]
		if !ok {
			return want
		}
		// Weeks run Monday to Sunday: the day of the week after this one.
		monday := now.AddDate(0, 0, -((int(now.Weekday())+6)%7)+7)
		return day(monday.AddDate(0, 0, (int(wd)+6)%7))
	}
	return want
}

// holds is whether text holds one of want's alternatives.
func holds(text, want string, now time.Time) bool {
	for _, alt := range strings.Split(want, "|") {
		if alt = strings.TrimSpace(resolve(strings.TrimSpace(alt), now)); alt != "" && strings.Contains(text, alt) {
			return true
		}
	}
	return false
}

// Matches is whether a call made is one c looks for.
func (c Call) Matches(r Recorded, now time.Time) bool {
	if c.Tool != "*" && c.Tool != r.Short() && c.Tool != r.Name {
		return false
	}
	what, args := r.row()
	if c.What != "" && c.What != what {
		return false
	}
	for k, want := range c.Args {
		if k == "*" {
			// Any argument: the call as it was made.
			if !holds(string(r.Input), want, now) {
				return false
			}
			continue
		}
		v, ok := args[k]
		if !ok {
			return false
		}
		s, isString := v.(string)
		if !isString {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		if !holds(s, want, now) {
			return false
		}
	}
	return true
}

// Grade is what went wrong with a run: an expected call not made, a call
// that must not be made, the answer saying what it must not or not saying
// what it must. None is a pass.
func Grade(sc Scenario, calls []Recorded, answer string, now time.Time) []string {
	var out []string
	e := sc.Expect
	made := func(c Call) bool {
		return slices.ContainsFunc(calls, func(r Recorded) bool { return c.Matches(r, now) })
	}
	for _, c := range e.Calls {
		if !made(c) {
			out = append(out, "하지 않음: "+c.String())
		}
	}
	if len(e.AnyOf) > 0 && !slices.ContainsFunc(e.AnyOf, made) {
		var names []string
		for _, c := range e.AnyOf {
			names = append(names, c.String())
		}
		out = append(out, "어느 것도 하지 않음: "+strings.Join(names, " / "))
	}
	for _, c := range e.Never {
		for _, r := range calls {
			if c.Matches(r, now) {
				out = append(out, "하면 안 되는 것을 함: "+r.String())
			}
		}
	}
	if e.NoTools && len(calls) > 0 {
		var names []string
		for _, r := range calls {
			names = append(names, r.String())
		}
		out = append(out, "도구를 쓰지 않아야 함: "+strings.Join(names, ", "))
	}
	for _, s := range e.Says {
		if !holds(answer, s, now) {
			out = append(out, "답에 없음: "+s)
		}
	}
	for _, s := range e.SaysNot {
		if holds(answer, s, now) {
			out = append(out, "답에 있으면 안 됨: "+s)
		}
	}
	return out
}
