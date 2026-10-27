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

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
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

// B-1, B-11: turns come as chat-svc writes them (RFC 3339, offset with a
// colon, microseconds), only what is newer is asked for, and a turn in the
// same second as the last one read is not taken for it.
func TestChatReadsNewTurnsBySince(t *testing.T) {
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	at := func(us int) string { return base.Add(time.Duration(us) * time.Microsecond).Format("2006-01-02T15:04:05.000000-07:00") }
	turns := []turn{
		{ID: "u-1", Role: "user", AtISO: at(100), Text: "하나"},
		{ID: "a-2", Role: "agent", AtISO: at(900000), Text: "둘"},
	}
	var sinces []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		sinces = append(sinces, since)
		cut, _ := time.Parse(time.RFC3339Nano, since)
		var out []turn
		for _, tt := range turns {
			if a, _ := time.Parse(time.RFC3339Nano, tt.AtISO); a.After(cut) {
				out = append(out, tt)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"turns": out})
	}))
	defer srv.Close()

	st := store.NewMemory()
	log := obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	s := &Chat{URL: srv.URL, Store: st, Log: log, Backfill: 6 * time.Hour, Out: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTP: srv.Client()}

	turns = turns[:1]
	if err := s.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	turns = append(turns, turn{ID: "a-2", Role: "agent", AtISO: at(900000), Text: "둘"}) // same second, later
	if err := s.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Message(context.Background(), "chat:a-2"); err != nil {
		t.Fatalf("같은 초에 온 다음 턴도 읽어야 합니다: %v", err)
	}
	if len(sinces) != 2 || sinces[1] == "" || !strings.Contains(sinces[1], ".") {
		t.Fatalf("since 를 마이크로초까지 넘겨야 합니다: %q", sinces)
	}
}

// A turn with an origin is mind's own output come back - a job's news, a
// reminder rung, a checklist shown. It is kept as a notice, under its own
// name, never as something the assistant said in the conversation.
func TestChatKeepsTheAssistantsOwnCardsAsNotices(t *testing.T) {
	at := time.Now().Add(-time.Hour).Truncate(time.Second).Format("2006-01-02T15:04:05.000000-07:00")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"turns":[
			{"id":"u-1","role":"user","atIso":"` + at + `","text":"그거 취소됐어"},
			{"id":"a-2","role":"agent","atIso":"` + at + `","text":"알겠어요."},
			{"id":"j-3","role":"agent","atIso":"` + at + `","paragraphs":["탁상시계 주문을 마쳤어요"],"origin":{"kind":"job","job":16,"run":40}},
			{"id":"n-4","role":"agent","atIso":"` + at + `","text":"치과 예약 확인하기","origin":{"kind":"notice","ref":"moment:7"}}
		]}`))
	}))
	defer srv.Close()
	st := store.NewMemory()
	log := obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	s := &Chat{URL: srv.URL, Store: st, Log: log, Backfill: 6 * time.Hour, Out: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTP: srv.Client()}
	if err := s.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]struct {
		notice bool
		sender string
		mine   bool
	}{
		"chat:u-1": {false, "", true},
		"chat:a-2": {false, "Jarvis(비서)", false},
		"chat:j-3": {true, model.NoticeSender, false},
		"chat:n-4": {true, model.NoticeSender, false},
	} {
		m, err := st.Message(context.Background(), ref)
		if err != nil || m.Notice != want.notice || m.Sender != want.sender || m.IsMine != want.mine {
			t.Fatalf("%s: %+v %v", ref, m, err)
		}
	}
}
