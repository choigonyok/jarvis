package observe

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

func memoryLog() *obs.MemoryLog {
	return obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// A message's text is kept, and its observation goes under a key of its own:
// the collector's record of it already holds the message's ref.
func TestAMessageIsKeptAndSeenUnderItsOwnKey(t *testing.T) {
	ctx := context.Background()
	st, log := store.NewMemory(), memoryLog()
	at := time.Date(2026, 10, 14, 18, 30, 0, 0, time.FixedZone("KST", 9*60*60))
	msg := model.Message{Ref: "kakao:2", Source: "kakao", Room: "kakao:7", RoomName: "지수", Sender: "지수", SentAt: at, Text: "이번 주말에 바다 갈까?"}
	if err := Message(ctx, st, log, msg); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Message(ctx, "kakao:2"); err != nil || got.Text != msg.Text {
		t.Fatalf("메시지가 저장돼야 합니다: %+v %v", got, err)
	}
	seen, _ := log.Read(ctx, 0, 10)
	var ref obs.MessageRef
	if len(seen) != 1 || seen[0].Type != model.KindMessageObserved || seen[0].Key != "observed:kakao:2" || seen[0].Key == msg.Ref ||
		seen[0].Source != "kakao" || seen[0].Subject != "kakao:7" || !seen[0].OccurredAt.Equal(at) ||
		json.Unmarshal(seen[0].Data, &ref) != nil || ref != (obs.MessageRef{Ref: "kakao:2", Room: "kakao:7"}) {
		t.Fatalf("관측 한 건이 제 키로: %+v", seen)
	}
}

// An occurrence is seen by its source, under its id and its time (UTC): the
// same one seen again is the same record.
func TestAnOccurrenceIsSeenByItsSourceIDAndTime(t *testing.T) {
	ctx := context.Background()
	st, log := store.NewMemory(), memoryLog()
	at := time.Date(2026, 10, 14, 18, 30, 0, 0, time.FixedZone("KST", 9*60*60))
	o := model.Occurrence{ID: "spending:812", Kind: "purchase", At: at, Title: "동네 분식", Amount: 9000, Ref: "812"}
	for range 2 {
		if err := Occurrence(ctx, st, log, o); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.Occurrence(ctx, "spending:812"); err != nil || got.Title != o.Title {
		t.Fatalf("발생이 저장돼야 합니다: %+v %v", got, err)
	}
	seen, _ := log.Read(ctx, 0, 10)
	var ref obs.OccurrenceRef
	if len(seen) != 1 || seen[0].Type != model.KindOccurrenceObserved || seen[0].Source != "spending" ||
		seen[0].Key != "occurrence:spending:812:2026-10-14T09:30:00Z" || seen[0].Subject != "spending:812" ||
		json.Unmarshal(seen[0].Data, &ref) != nil || ref != (obs.OccurrenceRef{ID: "spending:812", Kind: "purchase"}) {
		t.Fatalf("같은 발생은 한 번: %+v", seen)
	}
}
