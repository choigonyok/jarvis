package remind

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/module/place"
	"github.com/choigonyok/jarvis/agent/internal/tool"
)

type call struct {
	method, path string
	body         map[string]any
}

// fakeMind answers /api/reminders as mind does and keeps what it was asked.
func fakeMind(t *testing.T, calls *[]call) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c := call{method: r.Method, path: r.URL.RequestURI()}
		_ = json.NewDecoder(r.Body).Decode(&c.body)
		*calls = append(*calls, c)
		line := `{"reminder":{"id":"rem_1","text":"베개 챙기세요","source":"chat","status":"%s","when":"10/24(토) 08:00","line":"10/24(토) 08:00 · 베개 챙기세요%s (id rem_1)"}}`
		switch {
		case r.Method == http.MethodPost && c.body["at"] == "어제":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"시각을 읽지 못했습니다"}`))
		case r.Method == http.MethodPost, r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(strings.ReplaceAll(strings.Replace(line, "%s", "waiting", 1), "%s", "")))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(strings.Replace(strings.Replace(line, "%s", "cancelled", 1), "%s", " (취소함)", 1)))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"reminders":[
				{"id":"rem_1","source":"chat","status":"waiting","line":"10/24(토) 08:00 · 베개 챙기세요 (id rem_1)"},
				{"id":"rem_2","source":"rebalance","status":"waiting","line":"10/20(화) 17:00부터 집에 있거나 도착할 때 · 늦어도 22:00 · 자산 비중이 목표 범위를 벗어났어요 (id rem_2)"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func session(t *testing.T, m *Module) *tool.Board {
	t.Helper()
	b := tool.New("t", "")
	m.Look(b)
	m.Change(b)
	return b
}

func callTool(t *testing.T, b *tool.Board, name string, args map[string]any) (string, bool) {
	t.Helper()
	out, err := b.Do(context.Background(), name, args)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}

// The tools hand mind only what was given, and answer with what mind stored -
// or, when it stored nothing, say so plainly.
func TestTools(t *testing.T) {
	var calls []call
	s := session(t, New(fakeMind(t, &calls).URL+"/", "tok", nil))

	got, isErr := callTool(t, s, "reminder.set", map[string]any{"text": "베개 챙기세요", "at": "2026-10-24T08:00"})
	if isErr || !strings.Contains(got, "운영자에게 말할 때와 문구: 10/24(토) 08:00 · 베개 챙기세요\n") || strings.Contains(got, "[rem_1]") {
		t.Fatalf("저장된 때와 문구를 돌려줘야 합니다: %q", got)
	}
	// The id apart from the words to say, named for what it is: one leading the
	// line is copied into an answer.
	if !strings.Contains(got, "(고칠 때 도구에 넣는 id: rem_1 - 답에는 쓰지 않습니다)") {
		t.Fatalf("id 는 따로: %q", got)
	}
	if c := calls[0]; c.method != http.MethodPost || c.path != "/api/reminders" || c.body["source"] != "chat" || c.body["at"] != "2026-10-24T08:00" || c.body["place"] != nil {
		t.Fatalf("준 칸만 보내야 합니다: %+v", c)
	}

	got, isErr = callTool(t, s, "reminder.set", map[string]any{"text": "베개", "at": "어제"})
	if !isErr || !strings.Contains(got, "시각을 읽지 못했습니다") || !strings.Contains(got, "됐다고 말하지 마세요") {
		t.Fatalf("못 했으면 그렇게: %q %v", got, isErr)
	}

	got, isErr = callTool(t, s, "reminder.change", map[string]any{"id": "rem_1", "at": "2026-10-24T07:30", "clear": []string{"place"}})
	if c := calls[len(calls)-1]; isErr || c.method != http.MethodPatch || c.path != "/api/reminders/rem_1" || c.body["at"] != "2026-10-24T07:30" || c.body["text"] != nil || c.body["clear"] == nil {
		t.Fatalf("고칠 칸만: %+v %q", c, got)
	}
	if !strings.HasPrefix(got, "고쳤습니다.\n운영자에게 말할 때와 문구: 10/24(토) 08:00 · 베개 챙기세요") {
		t.Fatalf("%q", got)
	}

	got, _ = callTool(t, s, "reminders", map[string]any{"all": true})
	if calls[len(calls)-1].path != "/api/reminders?all=1" || !strings.Contains(got, "10/24(토) 08:00 · 베개 챙기세요 (id rem_1)") || !strings.Contains(got, "자비스가 그때로 미뤄 둔 알림") {
		t.Fatalf("목록: %q", got)
	}

	got, isErr = callTool(t, s, "reminder.cancel", map[string]any{"id": "rem_1"})
	if isErr || calls[len(calls)-1].method != http.MethodDelete || got != "취소했습니다: 10/24(토) 08:00 · 베개 챙기세요" {
		t.Fatalf("취소: %q", got)
	}
	if renderList(nil) != "맡아 둔 리마인드가 없습니다." {
		t.Fatal("빈 목록")
	}
}

// atlas is the map as the tools see it: one region of the operator's, one
// city with an outline, nothing else.
type atlas struct{ asked []string }

func (a *atlas) IsRegion(_ context.Context, name string) (bool, error) {
	a.asked = append(a.asked, "region:"+name)
	return name == "헬스장", nil
}

func (a *atlas) FindArea(_ context.Context, name string) (place.Area, error) {
	a.asked = append(a.asked, "map:"+name)
	if name != "가상시" {
		return place.Area{}, errors.New("지도에서 '" + name + "'를 찾지 못했습니다")
	}
	return place.Area{Name: "가상시", Where: "가상시, 가상도", Rings: [][][2]float64{{{128.0, 36.0}, {128.2, 36.0}, {128.2, 36.15}}}}, nil
}

// A place that is none of the operator's regions is found on the map and
// waited for by its outline; one the map does not know is not kept as a
// region that never comes.
func TestPlaceOnTheMap(t *testing.T) {
	var calls []call
	maps := &atlas{}
	m := New(fakeMind(t, &calls).URL, "tok", nil)
	m.UseMap(maps)
	s := session(t, m)

	got, isErr := callTool(t, s, "reminder.set", map[string]any{"text": "빵 사 가세요", "place": "가상시", "after": "2026-11-06T19:30"})
	area, _ := calls[len(calls)-1].body["area"].(map[string]any)
	if isErr || area["name"] != "가상시" || area["rings"] == nil || !strings.Contains(got, "지도에서 찾은 곳: 가상시, 가상도") {
		t.Fatalf("지도의 지역은 경계와 함께: %q %+v", got, calls[len(calls)-1].body)
	}

	n := len(calls)
	for _, p := range []string{"헬스장", "home", "회사"} {
		if _, isErr := callTool(t, s, "reminder.set", map[string]any{"text": "물", "place": p}); isErr || calls[len(calls)-1].body["area"] != nil {
			t.Fatalf("%s 는 영역이라 지도에 묻지 않는다: %+v", p, calls[len(calls)-1].body)
		}
	}
	if len(calls) != n+3 {
		t.Fatalf("세 번 저장: %d", len(calls)-n)
	}

	n = len(calls)
	got, isErr = callTool(t, s, "reminder.set", map[string]any{"text": "빵", "place": "수원"})
	if !isErr || len(calls) != n || !strings.Contains(got, "찾지 못했습니다") || !strings.Contains(got, "바뀐 것이 없습니다") {
		t.Fatalf("모르는 곳은 저장하지 않고 그렇게 말한다: %q", got)
	}

	if _, isErr := callTool(t, s, "reminder.change", map[string]any{"id": "rem_1", "place": "가상시"}); isErr || calls[len(calls)-1].body["area"] == nil {
		t.Fatalf("고칠 때도 지도의 지역은 경계와 함께: %+v", calls[len(calls)-1].body)
	}
}
