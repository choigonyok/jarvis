package find

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/search-svc/internal/naver"
)

type call struct {
	kind    naver.Kind
	display int
	sort    string
}

type fake struct {
	mu    sync.Mutex
	calls []call
	items map[naver.Kind][]naver.Item
	fail  map[naver.Kind]error
}

func (f *fake) Search(_ context.Context, kind naver.Kind, _ string, display, _ int, sort string) (naver.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{kind, display, sort})
	f.mu.Unlock()
	if err := f.fail[kind]; err != nil {
		return naver.Result{}, err
	}
	return naver.Result{Total: 100, Items: f.items[kind]}, nil
}

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func TestSinceDropsOlderPostsAndAsksNewestFirst(t *testing.T) {
	f := &fake{items: map[naver.Kind][]naver.Item{
		naver.Blog: {
			{Title: "새 글", Link: "https://blog.naver.com/a/2", Date: day(25)},
			{Title: "옛 글", Link: "https://blog.naver.com/a/1", Date: day(1)},
		},
		naver.Cafe: {{Title: "카페 글", Link: "https://cafe.naver.com/c/1"}},
	}}
	hits, covs, err := Search(context.Background(), f, Query{Text: "롤토체스 20판", Sources: []string{"blog", "cafe"}, Since: day(19), Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Title != "새 글" || hits[1].Source != "cafe" {
		t.Fatalf("hits = %+v", hits)
	}
	if covs[0].Older != 1 || covs[0].Shown != 1 || covs[0].Total != 100 {
		t.Errorf("blog coverage = %+v", covs[0])
	}
	// The blog (dated) is asked twice - best matches and newest - for three
	// times the limit; the cafe (undated) once, by match.
	var blogSorts []string
	for _, c := range f.calls {
		switch c.kind {
		case naver.Blog:
			blogSorts = append(blogSorts, c.sort)
			if c.display != 15 {
				t.Errorf("blog asked for %d, want three times the limit", c.display)
			}
		case naver.Cafe:
			if c.sort != "" {
				t.Errorf("the cafe was asked sort %q: it has no dates to cut by", c.sort)
			}
		}
	}
	if len(blogSorts) != 2 {
		t.Errorf("blog asked %v", blogSorts)
	}
}

func TestDefaultSourcesAndAFailingOne(t *testing.T) {
	f := &fake{
		items: map[naver.Kind][]naver.Item{naver.Web: {{Title: "공식", Link: "https://example.com/"}}, naver.News: {{Title: "같은 곳", Link: "http://example.com"}}},
		fail:  map[naver.Kind]error{naver.Cafe: errors.New("네이버 검색 호출 한도를 넘었습니다(429)")},
	}
	hits, covs, err := Search(context.Background(), f, Query{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 4 {
		t.Errorf("asked %d sources, want the four defaults", len(f.calls))
	}
	if len(hits) != 1 {
		t.Errorf("hits = %+v (the same page twice is shown once)", hits)
	}
	var cafe Coverage
	for _, c := range covs {
		if c.Source == "cafe" {
			cafe = c
		}
	}
	if !strings.Contains(cafe.Error, "429") {
		t.Errorf("cafe coverage = %+v", cafe)
	}
}

func TestUnknownSourceIsRefused(t *testing.T) {
	if _, _, err := Search(context.Background(), &fake{}, Query{Text: "x", Sources: []string{"instagram"}}); err == nil {
		t.Error("want an error")
	}
	if _, _, err := Search(context.Background(), &fake{}, Query{Text: "  "}); err == nil {
		t.Error("an empty query should fail")
	}
}
