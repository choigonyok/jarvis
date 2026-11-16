package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/calendar-svc/internal/uniple"
)

// Kept here, the guest changes the space as themselves: their wish is theirs,
// and what the API reads back to them is from their side without swapping.
func TestTheGuestWritesTheLocalSpace(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.Exec(ctx, `truncate couple.notifications, couple.news_seen, couple.bucket_items, couple.calendar_events, couple.blocks, couple.sides, couple.profiles, couple.spaces cascade`)
	admin.Close()
	if err != nil {
		t.Fatal(err)
	}
	c, err := uniple.OpenLocal(ctx, dsn, uniple.Config{}, uniple.LocalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	h := New(c, "owner-token", "guest-token", slog.New(slog.NewTextHandler(io.Discard, nil)), nil).WithCouple(c).Handler()
	call := func(token, method, path, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, b := call("owner-token", http.MethodPost, "/couple/blocks", `{"type":"bucket"}`)
	if code != http.StatusOK {
		t.Fatalf("블록 만들기: %d %v", code, b)
	}
	block := b["id"].(string)
	if code, out := call("guest-token", http.MethodPost, "/couple/blocks/"+block+"/bucket", `{"title":"제주 한 달"}`); code != http.StatusOK {
		t.Fatalf("손님의 버킷: %d %v", code, out)
	}
	_, space := call("guest-token", http.MethodGet, "/couple", "")
	if space["edit"] != true || space["me"].(map[string]any)["name"] != "상대" {
		t.Fatalf("손님이 본 공간: %v", space)
	}
	_, news := call("owner-token", http.MethodGet, "/couple/news", "")
	list := news["news"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["who"] != "partner" || news["seen"] != "" {
		t.Fatalf("운영자가 본 소식: %v", news)
	}
	at := list[0].(map[string]any)["at"].(string)
	if code, out := call("owner-token", http.MethodPost, "/couple/news/seen", `{"at":"`+at+`"}`); code != http.StatusOK {
		t.Fatalf("읽음: %d %v", code, out)
	}
	if _, news = call("owner-token", http.MethodGet, "/couple/news", ""); news["seen"] == "" {
		t.Fatalf("읽은 데까지: %v", news)
	}
	_, ev := call("guest-token", http.MethodPut, "/events", `{"date":"2026-12-01","title":"휴가","owner":"me"}`)
	if ev["owner"] != "me" || ev["source"] != "me" {
		t.Fatalf("손님에게 손님의 일정은 내 것: %v", ev)
	}
	if _, mine := call("owner-token", http.MethodGet, "/events/"+ev["id"].(string), ""); mine["owner"] != "partner" || mine["source"] != "partner" {
		t.Fatalf("운영자에게는 상대 것: %v", mine)
	}
}
