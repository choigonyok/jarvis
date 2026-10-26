// Package find asks Naver's search in several categories at once and puts
// the answers side by side: blogs and cafes for what people saw and wrote
// down, news for what was announced, web documents for the sites
// themselves.
//
// A category is a source. Adding one (a YouTube search, a site's own notice
// list) is a name here and a function that returns hits; the rest - since,
// limits, what was checked - is the same for all of them.
package find

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/search-svc/internal/naver"
)

// Sources are the names a search may ask by.
var Sources = map[string]naver.Kind{
	"blog":  naver.Blog,
	"news":  naver.News,
	"cafe":  naver.Cafe,
	"kin":   naver.Kin,
	"web":   naver.Web,
	"encyc": naver.Encyc,
	"local": naver.Local,
}

// Default is where a search looks when it is not told.
var Default = []string{"blog", "news", "cafe", "web"}

// Query is one search.
type Query struct {
	Text    string
	Sources []string
	// Since drops what was written before it. Blogs and news say when they
	// were written; the other categories do not, and are asked newest-first
	// instead where they can be.
	Since time.Time
	// Sort is "date" (newest first) or "" (best match).
	Sort string
	// Limit is per source.
	Limit int
}

// Hit is one result.
type Hit struct {
	Source   string     `json:"source"`
	Title    string     `json:"title"`
	URL      string     `json:"url"`
	Snippet  string     `json:"snippet,omitempty"`
	Date     *time.Time `json:"date,omitempty"`
	Author   string     `json:"author,omitempty"`
	Category string     `json:"category,omitempty"`
	Address  string     `json:"address,omitempty"`
}

// Coverage is what one source was asked and what it gave: how a "nothing
// found" can say where it looked.
type Coverage struct {
	Source string `json:"source"`
	Total  int    `json:"total"`
	Shown  int    `json:"shown"`
	Older  int    `json:"older,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Searcher is the HUB client; an interface so tests can stand in for it.
type Searcher interface {
	Search(ctx context.Context, kind naver.Kind, query string, display, start int, sort string) (naver.Result, error)
}

const (
	defaultLimit = 6
	maxLimit     = 30
	snippetRunes = 200
)

// Search asks every source of q at once.
func Search(ctx context.Context, s Searcher, q Query) ([]Hit, []Coverage, error) {
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" {
		return nil, nil, fmt.Errorf("검색어가 없습니다")
	}
	sources := q.Sources
	if len(sources) == 0 {
		sources = Default
	}
	var names []string
	seenName := map[string]bool{}
	for _, s := range sources {
		s = strings.ToLower(strings.TrimSpace(s))
		if _, ok := Sources[s]; !ok {
			return nil, nil, fmt.Errorf("모르는 출처 %q - %s 중에서 고르세요", s, strings.Join(sourceNames(), ", "))
		}
		if !seenName[s] {
			seenName[s] = true
			names = append(names, s)
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)

	type answer struct {
		hits []Hit
		cov  Coverage
	}
	answers := make([]answer, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			kind := Sources[name]
			cov := Coverage{Source: name}
			var items []naver.Item
			if !q.Since.IsZero() && dated(kind) {
				// Since, where there are dates: the best matches of the
				// period first, then the newest - newest alone is mostly
				// posts that only brush the words.
				display := min(limit*3, kind.MaxDisplay())
				best, err := s.Search(ctx, kind, q.Text, display, 1, "")
				if err != nil {
					cov.Error = err.Error()
					answers[i] = answer{cov: cov}
					return
				}
				newest, err := s.Search(ctx, kind, q.Text, display, 1, "date")
				if err == nil {
					best.Items = append(best.Items, newest.Items...)
				}
				cov.Total, items = best.Total, best.Items
			} else {
				// A category without dates cannot be cut by since; asking it
				// newest-first then gives the newest posts that barely match.
				sort := q.Sort
				if !dated(kind) && !q.Since.IsZero() {
					sort = ""
				}
				res, err := s.Search(ctx, kind, q.Text, limit, 1, sort)
				if err != nil {
					cov.Error = err.Error()
					answers[i] = answer{cov: cov}
					return
				}
				cov.Total, items = res.Total, res.Items
			}
			var hits []Hit
			taken := map[string]bool{}
			for _, it := range items {
				if taken[it.Link] {
					continue
				}
				taken[it.Link] = true
				if !q.Since.IsZero() && dated(kind) && !it.Date.IsZero() && it.Date.Before(q.Since) {
					cov.Older++
					continue
				}
				if len(hits) >= limit {
					break
				}
				h := Hit{Source: name, Title: it.Title, URL: it.Link, Snippet: clip(it.Description, snippetRunes),
					Author: it.Author, Category: it.Category, Address: firstOf(it.RoadAddress, it.Address)}
				if !it.Date.IsZero() {
					d := it.Date
					h.Date = &d
				}
				hits = append(hits, h)
			}
			answers[i] = answer{hits: hits, cov: cov}
		}(i, name)
	}
	wg.Wait()

	var hits []Hit
	var covs []Coverage
	seen := map[string]bool{}
	for _, a := range answers {
		shown := 0
		for _, h := range a.hits {
			key := strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(h.URL, "https://"), "http://"), "/")
			if h.URL != "" && seen[key] {
				continue
			}
			seen[key] = true
			hits = append(hits, h)
			shown++
		}
		a.cov.Shown = shown
		covs = append(covs, a.cov)
	}
	return hits, covs, nil
}

// dated is a category whose results say when they were written.
func dated(k naver.Kind) bool { return k == naver.Blog || k == naver.News }

func sourceNames() []string {
	return []string{"blog", "news", "cafe", "kin", "web", "encyc", "local"}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
