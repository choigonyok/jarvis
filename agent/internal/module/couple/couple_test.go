package couple

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

// fake is calendar-svc's couple routes: the space, its blocks, and every
// write it was sent.
type fake struct {
	mu     sync.Mutex
	writes []string // "METHOD path body"
}

const spaceJSON = `{
  "me": {"id": "u-me", "name": "하늘"},
  "partner": {"id": "u-you", "name": "바다"},
  "anniversary": "2026-08-15",
  "weekStart": "mon",
  "blocks": [
    {"id": "b-d", "type": "dday", "title": "♥", "dday": [
      {"id": "d0", "title": "처음 만난 날", "date": "2026-08-15", "start": true},
      {"id": "d1", "title": "바다 생일", "date": "2000-11-21", "repeat": "yearly"},
      {"id": "d2", "title": "지난 여행", "date": "2026-10-01"}]},
    {"id": "b-c", "type": "calendar", "title": "캘린더"},
    {"id": "b-w", "type": "workout", "title": "운동"},
    {"id": "b-b", "type": "bucket", "title": "버킷", "bucket": [
      {"id": "w1", "title": "제주", "done": true}, {"id": "w2", "title": "캠핑", "done": false}]},
    {"id": "b-m", "type": "map", "title": "지도", "visits": [
      {"id": "v1", "region": "21090", "asset": "a1", "rep": true}]},
    {"id": "b-f", "type": "free", "title": "자유", "free": [{"id": "n1", "content": "우산 챙기기", "by": "partner"}]},
    {"id": "b-a", "type": "album", "title": "앨범"}
  ]
}`

func (f *fake) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("%s %s: 토큰이 없습니다", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			raw, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.writes = append(f.writes, r.Method+" "+r.URL.Path+" "+string(raw))
			f.mu.Unlock()
			switch {
			case strings.HasSuffix(r.URL.Path, "/bucket"):
				_, _ = io.WriteString(w, `{"id":"w9","title":"바다 보러 가기","done":false}`)
			case strings.HasSuffix(r.URL.Path, "/dday"):
				_, _ = io.WriteString(w, `{"id":"d9","title":"200일","date":"2027-03-02"}`)
			case strings.HasSuffix(r.URL.Path, "/records"):
				_, _ = io.WriteString(w, `{"id":"r9","region":"21090","title":"해운대 산책"}`)
			case strings.HasSuffix(r.URL.Path, "/checks") && r.Method == http.MethodPost:
				_, _ = io.WriteString(w, `{"id":"c9","date":"2026-11-14","who":"me"}`)
			default:
				_, _ = io.WriteString(w, `{"ok":true}`)
			}
			return
		}
		switch {
		case r.URL.Path == "/couple":
			_, _ = io.WriteString(w, spaceJSON)
		case r.URL.Path == "/couple/news":
			_, _ = io.WriteString(w, `{"news":[{"who":"partner","blockType":"map","blockTitle":"지도","kind":"","at":"2026-11-13T05:00:00Z","item":{"region":"21090"}}]}`)
		case strings.HasSuffix(r.URL.Path, "/checks"):
			_, _ = io.WriteString(w, `{"checks":[{"id":"c1","date":"2026-11-10","who":"me","memo":"5km"},{"id":"c2","date":"2026-11-11","who":"partner"},{"id":"c3","date":"2026-11-14","who":"me"}]}`)
		case strings.HasPrefix(r.URL.Path, "/couple/blocks/"):
			var s space
			_ = json.Unmarshal([]byte(spaceJSON), &s)
			id := strings.TrimPrefix(r.URL.Path, "/couple/blocks/")
			for _, b := range s.Blocks {
				if b.ID == id {
					if b.Type == "map" {
						b.Records = []record{{ID: "r1", Region: "11010", Title: "궁궐 산책", From: "2026-10-03"}}
					}
					_ = json.NewEncoder(w).Encode(b)
					return
				}
			}
			http.Error(w, `{"error":"그런 블록이 없어요"}`, http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func module(t *testing.T) (*Module, *fake, *tool.Board, *tool.Board) {
	f := &fake{}
	m := New(f.server(t).URL, "tok")
	// 2026-11-14 (토) 정오, 서울.
	m.now = func() time.Time { return time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC) }
	look, change := tool.New(tool.Look, tool.LookAbout), tool.New(tool.Change, tool.ChangeAbout)
	m.Look(look)
	m.Change(change)
	return m, f, look, change
}

func TestTheSpaceInOneRead(t *testing.T) {
	_, _, look, _ := module(t)
	out, err := look.Do(context.Background(), "couple", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"만난 날 2026-08-15(D+92)",
		"♥ (디데이, id b-d): 다음 기념일 바다 생일 2026-11-21(D-7)",
		"운동 (습관 체크, id b-w): 이번 주(11-09~11-15) 운영자 2일 · 바다 1일",
		"버킷 (버킷리스트, id b-b): 1/2 이룸",
		"지도 (데이트 지도, id b-m): 사진으로 채운 지역 1곳",
		"앨범 (공유 앨범, id b-a): 이 콘솔에서 열지 않는 블록",
		"바다 · 지도 · 추가 · 부산 해운대구",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("요약에 %q 가 없습니다:\n%s", want, out)
		}
	}
}

func TestABlockWithItsItems(t *testing.T) {
	_, _, look, _ := module(t)
	for block, wants := range map[string][]string{
		"b-d": {"처음 만난 날 2026-08-15 · 만난 날(D+92) (id d0)", "바다 생일 2000-11-21 · 매년, 다음 2026-11-21(D-7) (id d1)", "지난 여행 2026-10-01 · D+44 (id d2)"},
		"b-b": {"[이룸] 제주 (id w1)", "[할 것] 캠핑 (id w2)"},
		"b-f": {"우산 챙기기 · 바다 (id n1)"},
		"b-w": {"2026-11-09 ~ 2026-11-15 체크:", "2026-11-10 운영자 · 5km", "2026-11-11 바다"},
		"b-m": {"사진으로 채운 지역 1곳: 부산 해운대구", "서울 종로구 · 궁궐 산책 · 2026-10-03 (id r1)"},
	} {
		out, err := look.Do(context.Background(), "couple", map[string]any{"block": block})
		if err != nil {
			t.Fatal(block, err)
		}
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s 에 %q 가 없습니다:\n%s", block, want, out)
			}
		}
	}
	if _, err := look.Do(context.Background(), "couple", map[string]any{"block": "b-x"}); err == nil {
		t.Fatal("없는 블록은 거절")
	}
}

func TestAddsGoWhereTheBlockKeepsThem(t *testing.T) {
	_, f, _, change := module(t)
	ctx := context.Background()
	if out, err := change.Do(ctx, "couple.add", map[string]any{"block": "b-b", "text": "바다 보러 가기"}); err != nil || !strings.Contains(out, "상대의 uniple 앱") {
		t.Fatalf("버킷: %q %v", out, err)
	}
	if _, err := change.Do(ctx, "couple.add", map[string]any{"block": "b-d", "text": "200일"}); err == nil {
		t.Fatal("기념일에는 날짜가 있어야 합니다")
	}
	if _, err := change.Do(ctx, "couple.add", map[string]any{"block": "b-d", "text": "엄마 생신", "date": "1970-03-02", "yearly": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := change.Do(ctx, "couple.add", map[string]any{"block": "b-m", "text": "해운대 산책", "region": "해운대", "date": "2026-11-08"}); err != nil {
		t.Fatal(err)
	}
	if _, err := change.Do(ctx, "couple.add", map[string]any{"block": "b-m", "text": "산책", "region": "중구"}); err == nil || !strings.Contains(err.Error(), "여럿") {
		t.Fatalf("이름이 같은 구는 어느 것인지 물어야 합니다: %v", err)
	}
	if _, err := change.Do(ctx, "couple.add", map[string]any{"block": "b-w", "text": "운동"}); err == nil {
		t.Fatal("습관 블록에는 더하지 않고 체크합니다")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	want := []string{
		`POST /couple/blocks/b-b/bucket {"title":"바다 보러 가기"}`,
		`POST /couple/blocks/b-d/dday {"date":"1970-03-02","repeat":"yearly","title":"엄마 생신"}`,
		`POST /couple/blocks/b-m/records {"from":"2026-11-08","region":"21090","title":"해운대 산책","to":"2026-11-08"}`,
	}
	if strings.Join(f.writes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("쓴 것:\n%s\n기대:\n%s", strings.Join(f.writes, "\n"), strings.Join(want, "\n"))
	}
}

func TestChangesRemovesAndChecks(t *testing.T) {
	_, f, _, change := module(t)
	ctx := context.Background()
	for _, c := range []struct {
		what string
		args map[string]any
	}{
		{"couple.change", map[string]any{"block": "b-b", "item": "w2", "done": true}},
		{"couple.change", map[string]any{"block": "b-d", "item": "d1", "yearly": false}},
		{"couple.change", map[string]any{"block": "b-f", "item": "n1", "text": "우산 꼭"}},
		{"couple.remove", map[string]any{"block": "b-m", "item": "r1"}},
		{"couple.check", map[string]any{"block": "b-w", "memo": "3km"}},
		{"couple.check", map[string]any{"block": "b-w", "day": "2026-11-10", "undo": true}},
	} {
		if _, err := change.Do(ctx, c.what, c.args); err != nil {
			t.Fatalf("%s %v: %v", c.what, c.args, err)
		}
	}
	if _, err := change.Do(ctx, "couple.change", map[string]any{"block": "b-b", "item": "w2"}); err == nil {
		t.Fatal("바꿀 것이 없으면 거절")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	want := []string{
		`PATCH /couple/blocks/b-b/bucket/w2 {"done":true}`,
		`PATCH /couple/blocks/b-d/dday/d1 {"repeat":""}`,
		`PATCH /couple/blocks/b-f/free/n1 {"content":"우산 꼭"}`,
		`DELETE /couple/blocks/b-m/records/r1 `,
		`POST /couple/blocks/b-w/checks {"date":"2026-11-14","memo":"3km"}`,
		`DELETE /couple/blocks/b-w/checks/c1 `,
	}
	if strings.Join(f.writes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("쓴 것:\n%s\n기대:\n%s", strings.Join(f.writes, "\n"), strings.Join(want, "\n"))
	}
}

func TestRegionsBySaidName(t *testing.T) {
	for said, want := range map[string]string{"해운대구": "21090", "부산 해운대": "21090", "종로": "11010", "11010": "11010", "서울 중구": "11020"} {
		if got, err := regions.find(said); err != nil || got != want {
			t.Errorf("%s: %s %v", said, got, err)
		}
	}
	for _, said := range []string{"", "아틀란티스", "99999"} {
		if _, err := regions.find(said); err == nil {
			t.Errorf("%q 는 거절", said)
		}
	}
	if got := regions.name("21090"); got != "부산 해운대구" {
		t.Errorf("이름: %s", got)
	}
}
