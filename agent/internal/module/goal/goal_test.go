package goal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

type sent struct {
	method, path string
	body         map[string]any
}

// fakeSender answers what would go through the stream.
type fakeSender struct {
	calls []sent
	err   error
}

func (f *fakeSender) Do(_ context.Context, method, path string, in, out any) error {
	raw, _ := json.Marshal(in)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.calls = append(f.calls, sent{method, path, body})
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(`{"goal":{"id":"goal_1","line":"[goal_1] 운동 (주 3회) 이번 주 1/3"}}`), out)
}

func TestChangesGoThroughTheStream(t *testing.T) {
	send := &fakeSender{}
	m := New("http://unused", "", send)
	b := tool.New("change", "")
	m.Change(b)
	ctx := context.Background()
	out, err := b.Do(ctx, "goal.make", map[string]any{"title": "운동", "cadence": "weekly", "times": 3})
	if err != nil || !strings.Contains(out, "이번 주 1/3") {
		t.Fatalf("%q %v", out, err)
	}
	if c := send.calls[0]; c.method != "POST" || c.path != "/api/goals" || c.body["cadence"] != "weekly" {
		t.Fatalf("%+v", c)
	}
	if _, err := b.Do(ctx, "goal.check", map[string]any{"id": "goal_1", "mark": "done", "day": "2026-11-02"}); err != nil {
		t.Fatal(err)
	}
	if c := send.calls[1]; c.path != "/api/goals/goal_1/checks" || c.body["by"] != "chat" || c.body["day"] != "2026-11-02" {
		t.Fatalf("%+v", c)
	}
	if _, err := b.Do(ctx, "goal.change", map[string]any{"id": "goal_1", "pause": true}); err != nil {
		t.Fatal(err)
	}
	if c := send.calls[2]; c.method != "PATCH" || c.body["pause"] != true {
		t.Fatalf("%+v", c)
	}
}

// Of a goal of more than one a day, a time is one more and a number is the
// day's count; a mark under a day said with nothing in it is not made.
func TestTimesOfADay(t *testing.T) {
	send := &fakeSender{}
	b := tool.New("change", "")
	New("http://unused", "", send).Change(b)
	ctx := context.Background()
	if _, err := b.Do(ctx, "goal.make", map[string]any{"title": "물 마시기", "cadence": "daily", "perDay": 4}); err != nil {
		t.Fatal(err)
	}
	if c := send.calls[0]; c.body["perDay"] != float64(4) {
		t.Fatalf("%+v", c)
	}
	if _, err := b.Do(ctx, "goal.check", map[string]any{"id": "goal_1", "add": 1}); err != nil {
		t.Fatal(err)
	}
	if c := send.calls[1]; c.body["add"] != float64(1) || c.body["mark"] != nil || c.body["count"] != nil {
		t.Fatalf("한 번 더: %+v", c)
	}
	if _, err := b.Do(ctx, "goal.check", map[string]any{"id": "goal_1", "count": 3, "day": "2026-11-04"}); err != nil {
		t.Fatal(err)
	}
	if c := send.calls[2]; c.body["count"] != float64(3) || c.body["mark"] != nil {
		t.Fatalf("세 번: %+v", c)
	}
	if _, err := b.Do(ctx, "goal.check", map[string]any{"id": "goal_1", "mark": "missed", "count": 2}); err != nil {
		t.Fatal(err)
	}
	if c := send.calls[3]; c.body["mark"] != "missed" || c.body["count"] != float64(2) {
		t.Fatalf("두 번 하고 그만: %+v", c)
	}
	if _, err := b.Do(ctx, "goal.check", map[string]any{"id": "goal_1", "mark": "clear"}); err != nil {
		t.Fatal(err)
	}
	if c := send.calls[4]; c.body["mark"] != "" {
		t.Fatalf("지우기: %+v", c)
	}
	if _, err := b.Do(ctx, "goal.check", map[string]any{"id": "goal_1"}); err == nil || len(send.calls) != 5 {
		t.Fatalf("아무것도 없는 기록은 보내지 않습니다: %v %d", err, len(send.calls))
	}
}

func TestLookReadsMind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/api/goals" {
			t.Errorf("%s %s", r.URL, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"goals":[{"id":"goal_1","line":"[goal_1] 물 2L (매일) 3일째"},{"id":"goal_2","line":"[goal_2] 운동 (주 3회)"}]}`))
	}))
	defer srv.Close()
	b := tool.New("look", "")
	New(srv.URL, "tok", nil).Look(b)
	out, err := b.Do(context.Background(), "goals", nil)
	if err != nil || !strings.Contains(out, "물 2L") || !strings.Contains(out, "운동") {
		t.Fatalf("%q %v", out, err)
	}
}
