package sensor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/ingest"
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
		{RowID: 2, ChatID: 7, Author: "지수", Message: ingest.Message{ChatName: "지수", Text: "이번 주말에 강릉 갈까?", SentAt: recent}},
		{RowID: 3, ChatID: 9, Message: ingest.Message{ChatName: "신한카드", Text: "승인 12,000원", SentAt: recent}},
		{RowID: 4, ChatID: 7, Message: ingest.Message{ChatName: "지수", IsMine: true, Text: " ", SentAt: recent}},
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

// The book gets every KakaoTalk message, and only those: what is worth reading
// as conversation is another question.
func TestMessagesGoToTheBook(t *testing.T) {
	sealer, _ := seal.New(make([]byte, 32))
	log := obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	var got []string
	s := &Messages{Store: store.NewMemory(), Log: log, Seal: sealer, Exclude: regexp.MustCompile(`카드`),
		Cards: func(_ context.Context, m ingest.Message) error {
			got = append(got, m.MessageID+" "+m.ChatType+" "+m.ChatName)
			return nil
		}}
	ctx := context.Background()
	m := collected{RowID: 3, ChatID: 9, Message: ingest.Message{MessageID: "m-3", ChatType: "direct", ChatName: "신한카드", Text: "승인 12,000원", SentAt: time.Now().Unix()}}
	if err := s.Take(ctx, sealed(t, sealer, "kakao", m)); err != nil {
		t.Fatal(err)
	}
	m.MessageID, m.RowID = "m-4", 4
	if err := s.Take(ctx, sealed(t, sealer, "imessage", m)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "m-3 direct 신한카드" {
		t.Fatalf("%v", got)
	}
}

// A book that cannot take a message holds the message: the stream retries it.
func TestABookThatCannotTakeItHoldsTheMessage(t *testing.T) {
	sealer, _ := seal.New(make([]byte, 32))
	s := &Messages{Seal: sealer, Cards: func(context.Context, ingest.Message) error { return errors.New("안 됩니다") }}
	m := collected{RowID: 3, Message: ingest.Message{ChatName: "신한카드", Text: "승인", SentAt: time.Now().Unix()}}
	if err := s.Take(context.Background(), sealed(t, sealer, "kakao", m)); err == nil {
		t.Fatal("다시 시도하도록 오류여야 합니다")
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
	m := collected{RowID: 8, ChatID: 3, Message: ingest.Message{ChatName: "카카오페이", Text: "김민수님이 32,000원을 보냈어요", SentAt: time.Now().Unix()}}
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
	err := s.Take(context.Background(), sealed(t, other, "kakao", collected{RowID: 1, Message: ingest.Message{Text: "x"}}))
	if !errors.Is(err, queue.ErrRefused) {
		t.Fatalf("%v", err)
	}
}

// sealedTurn is a turn as input-svc leaves it in the stream.
func sealedTurn(t *testing.T, sealer *seal.Sealer, thread string, tn turn) queue.Message {
	t.Helper()
	raw, _ := json.Marshal(tn)
	closed, err := sealer.Seal(raw)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"thread": thread, "sealed": base64.StdEncoding.EncodeToString(closed)})
	return queue.Message{Kind: KindTurn, Source: "chat", SourceKey: "turn:" + tn.ID, Payload: payload}
}

// A turn with an origin is mind's own output come back - a job's news, a
// reminder rung, a checklist shown. It is kept as a notice, under its own
// name, never as something the assistant said in the conversation. Times come
// as input-svc writes them: RFC 3339, a colon in the offset, microseconds.
func TestTurnsAreReadAsARoom(t *testing.T) {
	at := time.Now().Add(-time.Hour).Truncate(time.Second).Format("2006-01-02T15:04:05.000000-07:00")
	sealer, _ := seal.New(make([]byte, 32))
	st := store.NewMemory()
	log := obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	s := &Messages{Store: st, Log: log, Seal: sealer}
	ctx := context.Background()
	job := func(kind string) *struct {
		Kind string `json:"kind"`
	} {
		return &struct {
			Kind string `json:"kind"`
		}{kind}
	}
	for _, tn := range []turn{
		{ID: "u-1", Role: "user", AtISO: at, Text: "그거 취소됐어"},
		{ID: "a-2", Role: "agent", AtISO: at, Text: "알겠어요."},
		{ID: "j-3", Role: "agent", AtISO: at, Paragraphs: []string{"탁상시계 주문을 마쳤어요"}, Origin: job("job")},
		{ID: "n-4", Role: "agent", AtISO: at, Text: "치과 예약 확인하기", Origin: job("notice")},
	} {
		if err := s.TakeTurn(ctx, sealedTurn(t, sealer, "", tn)); err != nil {
			t.Fatal(err)
		}
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
		m, err := st.Message(ctx, ref)
		if err != nil || m.Notice != want.notice || m.Sender != want.sender || m.IsMine != want.mine {
			t.Fatalf("%s: %+v %v", ref, m, err)
		}
	}
}

// Another thread is not the operator's conversation, and what cannot be read
// will not be read next time either.
func TestTurnsOfAnotherThreadAndBadOnes(t *testing.T) {
	at := time.Now().Format(time.RFC3339Nano)
	sealer, _ := seal.New(make([]byte, 32))
	other, _ := seal.New([]byte("0123456789abcdef0123456789abcdef"))
	st := store.NewMemory()
	log := obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	s := &Messages{Store: st, Log: log, Seal: sealer}
	ctx := context.Background()
	if err := s.TakeTurn(ctx, sealedTurn(t, sealer, "other", turn{ID: "u-9", Role: "user", AtISO: at, Text: "다른 대화"})); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Message(ctx, "chat:u-9"); err == nil {
		t.Fatal("운영자 대화만 읽습니다")
	}
	if err := s.TakeTurn(ctx, sealedTurn(t, other, "", turn{ID: "u-8", Role: "user", AtISO: at, Text: "x"})); !errors.Is(err, queue.ErrRefused) {
		t.Fatalf("%v", err)
	}
	if err := s.TakeTurn(ctx, sealedTurn(t, sealer, "", turn{ID: "u-7", Role: "user", AtISO: "어제", Text: "x"})); !errors.Is(err, queue.ErrRefused) {
		t.Fatalf("%v", err)
	}
}
