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
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// A first start skips the backlog; after that every new message is observed
// once, text in the store and a reference in the log; alert rooms and empty
// messages are not observed at all.
func TestMessengerReadsWhatIsNew(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour).Unix()
	recent := time.Now().Add(-time.Minute).Unix()
	rows := []collected{
		{RowID: 1, ChatID: 7, ChatName: "지수", Author: "지수", Text: "지난주 이야기", SentAt: old},
		{RowID: 2, ChatID: 7, ChatName: "지수", Author: "지수", Text: "이번 주말에 강릉 갈까?", SentAt: recent},
		{RowID: 3, ChatID: 9, ChatName: "신한카드", Text: "승인 12,000원", SentAt: recent},
		{RowID: 4, ChatID: 7, ChatName: "지수", IsMine: true, Text: " ", SentAt: recent},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("rowid"), 10, 64)
		var out []collected
		for _, m := range rows {
			if m.RowID > after {
				out = append(out, m)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": out})
	}))
	defer srv.Close()

	st := store.NewMemory()
	log := obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var seen []obs.Observation
	go log.Run(context.Background(), func(_ context.Context, o obs.Observation) error { seen = append(seen, o); return nil })
	s := &Messenger{Name: "kakao", URL: srv.URL, Store: st, Log: log, Exclude: regexp.MustCompile(`카드`), Backfill: 6 * time.Hour, Every: time.Hour, Out: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTP: srv.Client()}

	if err := s.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	log.Drain()
	if len(seen) != 1 || seen[0].Key != "kakao:2" {
		t.Fatalf("최근 대화 한 건만 관측해야 합니다: %+v", seen)
	}
	msg, err := st.Message(context.Background(), "kakao:2")
	if err != nil || msg.Text != "이번 주말에 강릉 갈까?" || msg.Room != "kakao:7" {
		t.Fatalf("원문은 저장소에: %+v %v", msg, err)
	}
	if strings.Contains(string(seen[0].Data), "강릉") {
		t.Fatalf("관측 로그에는 원문이 없어야 합니다: %s", seen[0].Data)
	}

	rows = append(rows, collected{RowID: 5, ChatID: 7, ChatName: "지수", IsMine: true, Text: "그러자!", SentAt: recent})
	_ = s.Poll(context.Background())
	_ = s.Poll(context.Background())
	log.Drain()
	if len(seen) != 2 || seen[1].Key != "kakao:5" {
		t.Fatalf("새 메시지만 한 번: %+v", seen)
	}
}
