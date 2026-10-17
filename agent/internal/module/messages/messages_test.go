package messages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The tools ask mind with the services' token and answer in a few lines: who,
// when, where, the stretch around the word - and the picked message marked
// among its neighbours.
func TestSearchAndContext(t *testing.T) {
	var asked []string
	mind := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		asked = append(asked, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/messages/search":
			_, _ = w.Write([]byte(`{"messages":[{"ref":"kakao:3","source":"kakao","roomName":"대학 동기방","sender":"지수","sentAt":"2026-10-12T21:04:00+09:00","text":"…갓생은 부지런히 사는 거…"}]}`))
		case "/messages/context":
			_, _ = w.Write([]byte(`{"messages":[{"ref":"kakao:2","source":"kakao","roomName":"대학 동기방","sender":"민지","sentAt":"2026-10-12T21:03:00+09:00","text":"갓생이 뭐야?"},{"ref":"kakao:3","source":"kakao","roomName":"대학 동기방","sender":"지수","sentAt":"2026-10-12T21:04:00+09:00","text":"갓생은 부지런히 사는 거"},{"ref":"kakao:4","source":"kakao","roomName":"대학 동기방","isMine":true,"sentAt":"2026-10-12T21:05:00+09:00","text":"아하"}]}`))
		}
	}))
	defer mind.Close()
	m := New(mind.URL+"/", "tok")
	ctx := context.Background()

	ms, err := m.get(ctx, "/messages/search", map[string][]string{"q": {"갓생"}})
	if err != nil || len(ms) != 1 {
		t.Fatalf("%v %v", ms, err)
	}
	got := renderSearch(ms)
	for _, want := range []string{"[kakao:3]", "카카오톡 대학 동기방", "지수:", "갓생은 부지런히"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, got)
		}
	}
	around, _ := m.get(ctx, "/messages/context", map[string][]string{"ref": {"kakao:3"}})
	got = renderContext(around, "kakao:3")
	for _, want := range []string{"▶ 2026-10-12 21:04 지수: 갓생은", "- 2026-10-12 21:03 민지:", "나: 아하"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, got)
		}
	}
	if renderSearch(nil) == "" || !strings.Contains(renderSearch(nil), "없습니다") {
		t.Fatal("못 찾으면 그렇게 말합니다")
	}

	bad := New(mind.URL, "wrong")
	if _, err := bad.get(ctx, "/messages/search", map[string][]string{"q": {"x"}}); err == nil {
		t.Fatal("토큰이 틀리면 오류")
	}
	if count(0) != "5" || count(3) != "3" {
		t.Fatal("앞뒤 수")
	}
}

func init() { time.Local = time.FixedZone("KST", 9*3600) }
