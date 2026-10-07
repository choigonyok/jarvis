package spending

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
)

type fakeSvc struct {
	mu       sync.Mutex
	pending  []Pending
	reported map[int64]string
}

func (f *fakeSvc) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /enrich/pending", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"transactions": f.pending})
	})
	mux.HandleFunc("POST /transactions/{id}/enrich-report", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Outcome string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		defer f.mu.Unlock()
		var id int64
		_ = json.Unmarshal([]byte(r.PathValue("id")), &id)
		f.reported[id] = b.Outcome
		w.WriteHeader(http.StatusOK)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

type fakeBG struct {
	calls  int
	prompt string
	task   claudecode.Task
	run    func()
	err    error
}

func (b *fakeBG) Idle() bool { return true }
func (b *fakeBG) Background(_ context.Context, t claudecode.Task) (string, error) {
	b.calls++
	b.prompt, b.task = t.Prompt, t
	if b.run != nil {
		b.run()
	}
	return "ok", b.err
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestEnrichOnceReportsOnlyTheUnanswered(t *testing.T) {
	f := &fakeSvc{reported: map[int64]string{},
		pending: []Pending{{ID: 1, SourceLabel: "쿠팡", Merchant: "쿠팡", AmountKrw: 8900}, {ID: 2, SourceLabel: "쿠팡", Merchant: "쿠팡(쿠페이)", AmountKrw: 2970}}}
	m := New(NewStore(f.server(t).URL, ""))
	bg := &fakeBG{run: func() {
		// The model answered #1 (it leaves the due list) and said nothing about #2.
		f.mu.Lock()
		f.pending = f.pending[1:]
		f.mu.Unlock()
	}}
	m.enrichOnce(context.Background(), bg, EnrichConfig{EnrichURL: "http://x/mcp/spending-enrich"}, f.pending, quiet)

	if bg.calls != 1 || !strings.Contains(bg.prompt, "결제 id 1") || !strings.Contains(bg.prompt, "₩8,900") {
		t.Fatalf("calls %d prompt %q", bg.calls, bg.prompt)
	}
	if len(f.reported) != 1 || f.reported[2] != "failed" {
		t.Fatalf("reported %v", f.reported)
	}
	// 백그라운드 작업에는 입력 도구도, 셸도 없다.
	for _, tool := range bg.task.Allowed {
		if strings.Contains(tool, "browser_type") || strings.Contains(tool, "Bash") {
			t.Fatalf("허용하면 안 되는 도구: %s", tool)
		}
	}
	if bg.task.Servers[EnrichServerName] == "" || len(bg.task.Extra) != 1 {
		t.Fatalf("servers %v extra %v", bg.task.Servers, bg.task.Extra)
	}
}

// When the person starts talking, the lookup gives way and records nothing:
// the same charges come up again later, with no attempt counted.
func TestYieldRecordsNothing(t *testing.T) {
	f := &fakeSvc{reported: map[int64]string{}, pending: []Pending{{ID: 1, Merchant: "쿠팡", AmountKrw: 8900}}}
	m := New(NewStore(f.server(t).URL, ""))
	m.enrichOnce(context.Background(), &fakeBG{err: claudecode.ErrYielded}, EnrichConfig{}, f.pending, quiet)
	if len(f.reported) != 0 {
		t.Fatalf("양보했는데 기록됨: %v", f.reported)
	}
}
