// Package memory is long-term memory as mind sees it: memory-svc, where
// Graphiti keeps people, places and things as a graph. mind writes the facts
// it has checked against a conversation (Put) and reads what is known (Search,
// Find, Profile). Graphiti's judgement comes back with each fact: which node
// each end went on, whether it repeated something already known, which facts
// it retired.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

type Client interface {
	// Put sends facts; each comes back with memory's judgement, or with
	// Later when it could not go in now (the day's budget, an error).
	Put(ctx context.Context, facts []model.Fact) ([]Result, error)
	// Search is facts for a question, ranked; center (a node id) adds what
	// is around that person or place.
	Search(ctx context.Context, q, center string, limit int) ([]Hit, error)
	// Find is nodes by name.
	Find(ctx context.Context, name string, limit int) ([]Node, error)
	// Profile is one node with its summary and latest facts.
	Profile(ctx context.Context, id string) (Profile, error)
}

// Result is memory-svc's answer for one fact (POST /facts).
type Result struct {
	Ref   string `json:"ref"`
	Later string `json:"later,omitempty"`
	Edge  struct {
		ID   string `json:"id"`
		Fact string `json:"fact"`
	} `json:"edge"`
	Duplicate bool             `json:"duplicate"`
	Expired   bool             `json:"expired"`
	Source    model.MemoryNode `json:"source"`
	Target    model.MemoryNode `json:"target"`
	Ended     []model.Ended    `json:"ended"`
}

func (r Result) Stored() *model.Stored {
	return &model.Stored{
		Edge: r.Edge.ID, EdgeFact: r.Edge.Fact, Duplicate: r.Duplicate, Expired: r.Expired,
		Source: r.Source, Target: r.Target, Ended: r.Ended,
	}
}

// Hit is one fact a search found.
type Hit struct {
	ID        string           `json:"id"`
	Fact      string           `json:"fact"`
	Relation  string           `json:"relation"`
	ValidAt   string           `json:"validAt,omitempty"`
	InvalidAt string           `json:"invalidAt,omitempty"`
	Source    model.MemoryNode `json:"source"`
	Target    model.MemoryNode `json:"target"`
}

// Ended is whether memory holds the fact as no longer true.
func (h Hit) Ended() bool { return h.InvalidAt != "" }

type Node struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Kinds   []string `json:"kinds"`
	Summary string   `json:"summary"`
}

type Profile struct {
	Node
	Facts []ProfileFact `json:"facts"`
}

// ProfileFact is one of a node's facts; Other is the node at its far end.
type ProfileFact struct {
	ID       string `json:"id"`
	Fact     string `json:"fact"`
	Relation string `json:"relation"`
	Other    string `json:"other"`
	ValidAt  string `json:"validAt,omitempty"`
	Ended    bool   `json:"ended"`
}

// HTTP is memory-svc. Group is the graph mind reads and writes: the
// operator's, "owner".
type HTTP struct {
	URL    string
	Token  string
	Group  string
	Client *http.Client
}

func NewHTTP(base, token string) *HTTP {
	return &HTTP{URL: strings.TrimRight(base, "/"), Token: token, Group: "owner", Client: &http.Client{Timeout: 3 * time.Minute}}
}

type party struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"`
	Self bool   `json:"self,omitempty"`
}

func partyOf(p model.Party) party {
	if p.Self {
		return party{Self: true}
	}
	return party{ID: p.Node, Name: p.Name, Kind: p.Kind}
}

func (c *HTTP) Put(ctx context.Context, facts []model.Fact) ([]Result, error) {
	type in struct {
		Ref      string   `json:"ref"`
		Source   party    `json:"source"`
		Target   party    `json:"target"`
		Relation string   `json:"relation"`
		Fact     string   `json:"fact"`
		ValidAt  string   `json:"validAt,omitempty"`
		Ends     []string `json:"ends,omitempty"`
	}
	body := struct {
		Group string `json:"group"`
		Facts []in   `json:"facts"`
	}{Group: c.Group}
	for _, f := range facts {
		var ends []string
		for _, e := range f.Ends {
			ends = append(ends, e.ID)
		}
		body.Facts = append(body.Facts, in{f.ID, partyOf(f.Subject), partyOf(f.Object), f.Relation, f.Text, f.ValidAt, ends})
	}
	var out struct {
		Results []Result `json:"results"`
	}
	return out.Results, c.do(ctx, http.MethodPost, "/facts", body, &out)
}

// Records sends structured records (POST /records): written straight to
// the graph, no model asked.
func (c *HTTP) Records(ctx context.Context, recs []model.Record) error {
	body := struct {
		Group   string         `json:"group"`
		Records []model.Record `json:"records"`
	}{c.Group, recs}
	var out struct {
		Written int `json:"written"`
	}
	return c.do(ctx, http.MethodPost, "/records", body, &out)
}

func (c *HTTP) Search(ctx context.Context, q, center string, limit int) ([]Hit, error) {
	v := url.Values{"q": {q}, "group": {c.Group}, "limit": {fmt.Sprint(limit)}}
	if center != "" {
		v.Set("center", center)
	}
	var out struct {
		Facts []Hit `json:"facts"`
	}
	return out.Facts, c.do(ctx, http.MethodGet, "/search?"+v.Encode(), nil, &out)
}

func (c *HTTP) Find(ctx context.Context, name string, limit int) ([]Node, error) {
	v := url.Values{"q": {name}, "group": {c.Group}, "limit": {fmt.Sprint(limit)}}
	var out struct {
		Nodes []Node `json:"nodes"`
	}
	return out.Nodes, c.do(ctx, http.MethodGet, "/nodes?"+v.Encode(), nil, &out)
}

func (c *HTTP) Profile(ctx context.Context, id string) (Profile, error) {
	var out Profile
	return out, c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(id)+"?group="+url.QueryEscape(c.Group), nil, &out)
}

func (c *HTTP) do(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return model.ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("memory-svc %s %s: %d", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
