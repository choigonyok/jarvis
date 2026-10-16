package sensor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// The past before the earliest message mind holds is brought in without
// touching the live reading, read once for facts, and not again.
func TestHistoryBringsInThePastOnce(t *testing.T) {
	ctx := context.Background()
	line := time.Now().Add(-time.Hour).Truncate(time.Second)
	old := line.Add(-72 * time.Hour)
	rows := []collected{
		{RowID: 1, ChatID: 7, ChatName: "지수", Author: "지수", Text: "나 부산으로 이사했어", SentAt: old.Unix()},
		{RowID: 2, ChatID: 9, ChatName: "신한카드", Text: "승인 12,000원", SentAt: old.Unix()},
		{RowID: 3, ChatID: 8, ChatName: "민지", Author: "민지", Text: "나 고수 못 먹어", SentAt: old.Add(time.Hour).Unix()},
		{RowID: 4, ChatID: 7, ChatName: "지수", Author: "지수", Text: "오늘 뭐 해?", SentAt: line.Add(time.Minute).Unix()},
	}
	kakao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("rowid"), 10, 64)
		var out []collected
		for _, m := range rows {
			if m.RowID > after {
				out = append(out, m)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": out})
	}))
	defer kakao.Close()
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"turns": []turn{
			{ID: "u-1", Role: "user", AtISO: old.Format(time.RFC3339Nano), Text: "나는 빨간색 안 좋아해"},
		}})
	}))
	defer chat.Close()

	st := store.NewMemory()
	// What the live reading already has: the line is its earliest message.
	if err := st.PutMessage(ctx, model.Message{Ref: "kakao:4", Source: "kakao", Room: "kakao:7", RoomName: "지수", Sender: "지수", SentAt: line, Text: "오늘 뭐 해?"}); err != nil {
		t.Fatal(err)
	}
	_ = st.MarkRoom(ctx, "kakao:7", line)

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	script := &llm.Script{Answer: func(_, user string) (string, error) {
		return `{"changes":[{"op":"create","kind":"plan","set":{"title":"약속","status":"confirmed"},"evidence":[{"line":1,"quote":"나"}],"confidence":0.9}]}`, nil
	}}
	now := time.Now
	conv := interpret.NewConversation(interpret.Config{Owner: "윤"}, st, world.New(st, now), script, time.Local, now, quiet)
	noise := regexp.MustCompile(`카드`)
	h := &History{
		Messengers: []*Messenger{{Name: "kakao", URL: kakao.URL, Exclude: noise, HTTP: kakao.Client()}},
		Chat:       &Chat{URL: chat.URL, HTTP: chat.Client()},
		Store:      st, Reader: conv, Noise: noise, Batch: 40, Out: quiet, Wait: time.Millisecond,
	}
	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"kakao:1", "kakao:3", "chat:u-1"} {
		if _, err := st.Message(ctx, ref); err != nil {
			t.Fatalf("%s 를 들여와야 합니다: %v", ref, err)
		}
	}
	if _, err := st.Message(ctx, "kakao:2"); err == nil {
		t.Fatal("알림 방은 들여오지 않습니다")
	}
	rooms, _ := st.Rooms(ctx)
	for _, r := range rooms {
		if r.Uninterpreted > 0 && r.Room != "kakao:7" {
			t.Fatalf("지난 대화가 새 메시지로 읽히면 안 됩니다: %+v", r)
		}
	}
	if script.Calls() != 3 {
		t.Fatalf("지수·민지·jarvis 방을 한 번씩 읽어야 합니다: %d", script.Calls())
	}
	if es, _ := st.Entities(ctx, "", "", false); len(es) != 0 {
		t.Fatalf("일정은 만들지 않습니다: %+v", es)
	}

	if err := h.Run(ctx); err != nil || script.Calls() != 3 {
		t.Fatalf("한 번 끝나면 다시 읽지 않습니다: calls=%d err=%v", script.Calls(), err)
	}
}
