package sensor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// A message a collector handed on, sealed as it came.
func sealed(t *testing.T, sealer *seal.Sealer, source string, m collected) queue.Message {
	t.Helper()
	raw, _ := json.Marshal(m)
	body, err := sealer.Seal(raw)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"sealed": base64.StdEncoding.EncodeToString(body)})
	return queue.Message{Kind: KindMessage, Source: source, SourceKey: fmt.Sprintf("%s:%d", source, m.RowID), Payload: payload}
}

// Every message comes; one worth reading is observed once, text in the store
// and a reference in the log; alert rooms and empty messages are not observed.
func TestMessagesTakesWhatIsWorthReading(t *testing.T) {
	recent := time.Now().Add(-time.Minute).Unix()
	sealer, err := seal.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	log := obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var seen []obs.Observation
	go log.Run(context.Background(), func(_ context.Context, o obs.Observation) error { seen = append(seen, o); return nil })
	s := &Messages{Store: st, Log: log, Seal: sealer, Exclude: regexp.MustCompile(`카드`)}
	ctx := context.Background()
	for _, m := range []collected{
		{RowID: 2, ChatID: 7, ChatName: "지수", Author: "지수", Text: "이번 주말에 강릉 갈까?", SentAt: recent},
		{RowID: 3, ChatID: 9, ChatName: "신한카드", Text: "승인 12,000원", SentAt: recent},
		{RowID: 4, ChatID: 7, ChatName: "지수", IsMine: true, Text: " ", SentAt: recent},
	} {
		if err := s.Take(ctx, sealed(t, sealer, "kakao", m)); err != nil {
			t.Fatal(err)
		}
	}
	log.Drain()
	if len(seen) != 1 || seen[0].Key != "kakao:2" {
		t.Fatalf("최근 대화 한 건만 관측해야 합니다: %+v", seen)
	}
	msg, err := st.Message(ctx, "kakao:2")
	if err != nil || msg.Text != "이번 주말에 강릉 갈까?" || msg.Room != "kakao:7" || msg.Source != "kakao" || msg.RoomName != "지수" {
		t.Fatalf("원문은 저장소에: %+v %v", msg, err)
	}
	if strings.Contains(string(seen[0].Data), "강릉") {
		t.Fatalf("관측 로그에는 원문이 없어야 합니다: %s", seen[0].Data)
	}
}

// A payment app's room is not read as conversation, but money received in it
// is a transfer.
func TestMessagesFindsATransfer(t *testing.T) {
	sealer, _ := seal.New(make([]byte, 32))
	st := store.NewMemory()
	log := obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	s := &Messages{Store: st, Log: log, Seal: sealer, Exclude: regexp.MustCompile(`페이`), Payments: regexp.MustCompile(`^(카카오페이|토스)$`), Occur: observer{st}}
	ctx := context.Background()
	m := collected{RowID: 8, ChatID: 3, ChatName: "카카오페이", Text: "김민수님이 32,000원을 보냈어요", SentAt: time.Now().Unix()}
	if err := s.Take(ctx, sealed(t, sealer, "kakao", m)); err != nil {
		t.Fatal(err)
	}
	o, err := st.Occurrence(ctx, "kakao-pay:8")
	if err != nil || o.Kind != "transfer" || o.Amount != 32000 {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := st.Message(ctx, "kakao:8"); err == nil {
		t.Fatal("결제 앱 방은 대화로 읽지 않습니다")
	}
}

// What cannot be opened will not open next time.
func TestMessagesRefusesWhatItCannotOpen(t *testing.T) {
	sealer, _ := seal.New(make([]byte, 32))
	other, _ := seal.New([]byte("0123456789abcdef0123456789abcdef"))
	s := &Messages{Seal: sealer}
	err := s.Take(context.Background(), sealed(t, other, "kakao", collected{RowID: 1, Text: "x"}))
	if !errors.Is(err, queue.ErrRefused) {
		t.Fatalf("%v", err)
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
