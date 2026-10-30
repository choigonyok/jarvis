package probes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

func row(t *testing.T, rows []check.Probe, id string) check.Verdict {
	t.Helper()
	for _, p := range rows {
		if p.ID == id {
			return p.Run(context.Background())
		}
	}
	t.Fatalf("%s 줄이 없습니다", id)
	return check.Verdict{}
}

// A source is as fresh as its last news against what the registry expects of
// it, and a check says what its service said: this list names neither.
func TestRowsComeFromWhatTheStackSays(t *testing.T) {
	now := time.Now()
	registry := `{"rows":[
		{"section":"kind","key":"a","body":{"lane":"relay"}},
		{"section":"source","key":"new.ok","body":{"kind":"k1","from":"s","warn":"1h","fail":"2h","group":"수집","name":"새 것","ok":"들어와요","late":"%s째 없어요","gone":"%s째 멈췄어요","label":"마지막","deps":["svc.x"]}},
		{"section":"source","key":"new.late","body":{"kind":"k2","from":"s","warn":"1h","fail":"2h","name":"늦은 것","ok":"들어와요","late":"%s째 없어요","gone":"%s째 멈췄어요"}},
		{"section":"source","key":"new.gone","body":{"kind":"k3","from":"s","warn":"1h","fail":"2h","name":"끊긴 것","ok":"들어와요","late":"%s째 없어요","gone":"%s째 멈췄어요"}},
		{"section":"source","key":"new.none","body":{"kind":"k4","from":"s","warn":"1h","fail":"2h","name":"처음","ok":"들어와요","late":"l","gone":"g"}}]}`
	mindUp := true
	last := map[string]any{
		"sources": []map[string]any{
			{"kind": "k1", "source": "s", "at": now.Add(-10 * time.Minute)},
			{"kind": "k2", "source": "s", "at": now.Add(-90 * time.Minute)},
			{"kind": "k3", "source": "s", "at": now.Add(-3 * time.Hour)},
			{"kind": "k4", "source": "other", "at": now},
		},
		"checks": []map[string]any{
			{"id": "host.disk", "at": now.Add(-time.Minute), "body": map[string]any{"id": "host.disk", "group": "기반", "name": "저장 공간", "state": "warn", "summary": "슬슬 정리할 때예요.", "figure": "143 / 245GB", "meter": 90}},
			{"id": "old.one", "at": now.Add(-4 * time.Hour), "body": map[string]any{"id": "old.one", "name": "오래된 것", "state": "ok", "summary": "좋아요."}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/last":
			_ = json.NewEncoder(w).Encode(last)
		case r.URL.Path == "/api/registry" && mindUp:
			_, _ = w.Write([]byte(registry))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	s := NewStack(Config{InputURL: srv.URL, MindURL: srv.URL})

	rows, ok := s.Rows(context.Background())
	if !ok {
		t.Fatal("읽지 못했습니다")
	}
	if v := row(t, rows, "new.ok"); v.State != check.OK || v.Summary != "들어와요" || v.ObservedLabel != "마지막" {
		t.Fatalf("%+v", v)
	}
	if v := row(t, rows, "new.late"); v.State != check.Warn || !strings.HasSuffix(v.Summary, "째 없어요") {
		t.Fatalf("%+v", v)
	}
	if v := row(t, rows, "new.gone"); v.State != check.Fail || !strings.HasPrefix(v.Summary, "3시간") {
		t.Fatalf("%+v", v)
	}
	if v := row(t, rows, "new.none"); v.State != check.Unknown {
		t.Fatalf("다른 소스의 기록은 세지 않습니다: %+v", v)
	}
	if v := row(t, rows, "host.disk"); v.State != check.Warn || v.Figure != "143 / 245GB" || v.Meter == nil || *v.Meter != 90 {
		t.Fatalf("%+v", v)
	}
	if v := row(t, rows, "old.one"); v.State != check.Unknown || !strings.Contains(v.Summary, "소식이 없어요") {
		t.Fatalf("말이 끊긴 점검은 모름입니다: %+v", v)
	}

	// mind away: what arrives still matters, and it is what the registry said last.
	mindUp = false
	rows, ok = s.Rows(context.Background())
	if !ok {
		t.Fatal("입구는 읽혔습니다")
	}
	if v := row(t, rows, "new.gone"); v.State != check.Fail {
		t.Fatalf("%+v", v)
	}
	// The way in away: nothing is known, and the screen keeps what it has.
	srv.Close()
	if _, ok := s.Rows(context.Background()); ok {
		t.Fatal("입구가 없으면 줄을 바꾸지 않습니다")
	}
}
