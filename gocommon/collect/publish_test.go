package collect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/seal"
)

type fixture struct {
	p    *Publisher
	rows []Item
	sent []record.Record
	fail int
}

func setup(t *testing.T) *fixture {
	t.Helper()
	s, err := seal.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{}
	f.p = &Publisher{
		Source: "kakao", CursorPath: filepath.Join(t.TempDir(), "published"), Seal: s,
		Loops: health.NewLoops(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		After: func(rowid int64, limit int) ([]Item, error) {
			var out []Item
			for _, r := range f.rows {
				if r.RowID > rowid && len(out) < limit {
					out = append(out, r)
				}
			}
			return out, nil
		},
		Latest: func() (int64, error) {
			if len(f.rows) == 0 {
				return 0, nil
			}
			return f.rows[len(f.rows)-1].RowID, nil
		},
		Send: func(_ context.Context, r record.Record) error {
			if f.fail > 0 {
				f.fail--
				return errors.New("입구가 꺼져 있습니다")
			}
			f.sent = append(f.sent, r)
			return nil
		},
	}
	return f
}

func msg(id int64) Item {
	return Item{RowID: id, ChatID: 9, SentAt: 1000 + id, Body: map[string]any{"rowid": id, "text": "비밀 이야기", "author": "민수"}}
}

// A first start begins from now; what comes after goes out sealed, in order.
func TestAFirstStartBeginsFromNow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.rows = []Item{msg(1), msg(2)}
	if err := f.p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 0 {
		t.Fatalf("지난 것을 보냈습니다: %d", len(f.sent))
	}
	f.rows = append(f.rows, msg(3), msg(4))
	if err := f.p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 2 || f.sent[0].SourceKey != "kakao:3" || f.sent[1].SourceKey != "kakao:4" {
		t.Fatalf("%+v", f.sent)
	}
	r := f.sent[0]
	if r.Kind != KindMessage || r.Source != "kakao" || r.Subject != "kakao:9" || r.Tier != record.Raw || r.OccurredAt.Unix() != 1003 {
		t.Fatalf("%+v", r)
	}
	raw, _ := json.Marshal(r.Payload)
	if string(raw) == "" || containsPlain(raw) {
		t.Fatalf("평문이 보입니다: %s", raw)
	}
	var p struct {
		Sealed string `json:"sealed"`
	}
	_ = json.Unmarshal(raw, &p)
	body, err := base64.StdEncoding.DecodeString(p.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := f.p.Seal.Open(body)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(plain, &got); err != nil || got.Text != "비밀 이야기" {
		t.Fatalf("%s %v", plain, err)
	}
}

func containsPlain(raw []byte) bool {
	s := string(raw)
	return len(s) > 0 && (contains(s, "비밀") || contains(s, "민수"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The cursor moves only past what was taken: an outage costs a delay.
func TestAnOutageLosesNothing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if err := f.p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	f.rows = []Item{msg(1), msg(2), msg(3)}
	f.fail = 1
	if err := f.p.Round(ctx); err == nil {
		t.Fatal("실패를 말해야 합니다")
	}
	if len(f.sent) != 0 {
		t.Fatalf("%d", len(f.sent))
	}
	if err := f.p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 3 {
		t.Fatalf("%d", len(f.sent))
	}
}

// The cursor outlives a restart, and more than a batch goes through.
func TestTheCursorSurvivesAndBatchesContinue(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if err := os.WriteFile(f.p.CursorPath, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= publishBatch+5; i++ {
		f.rows = append(f.rows, msg(i))
	}
	if err := f.p.Round(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != publishBatch+5 {
		t.Fatalf("%d", len(f.sent))
	}
	raw, _ := os.ReadFile(f.p.CursorPath)
	if string(raw) != "205" {
		t.Fatalf("%s", raw)
	}
}
