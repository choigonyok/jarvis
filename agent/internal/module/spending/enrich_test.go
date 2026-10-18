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
	"time"

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

func (b *fakeBG) CanStart(bool) bool { return true }
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

// A lookup the conversation keeps cutting short waits longer each time, up
// to an hour; one that ran resets it.
func TestYieldWait(t *testing.T) {
	cases := []struct {
		yields int
		want   time.Duration
	}{{0, 10 * time.Minute}, {1, 20 * time.Minute}, {2, 40 * time.Minute}, {3, time.Hour}, {9, time.Hour}}
	for _, c := range cases {
		if got := yieldWait(10*time.Minute, c.yields); got != c.want {
			t.Errorf("%d번 연달아 미룸: %v, 기대 %v", c.yields, got, c.want)
		}
	}
	if got := yieldWait(2*time.Hour, 3); got != 2*time.Hour {
		t.Errorf("쿨다운이 한 시간보다 길면 그대로: %v", got)
	}
}

// The lookup reads an order's date and amount from a screenshot: the element
// list leaves plain text out, and the extraction tool needs an LLM key the
// browser does not have (2026-10-18, every lookup ended without them).
func TestTheLookupCanSeeThePage(t *testing.T) {
	has := map[string]bool{}
	for _, tool := range enrichTools {
		has[tool] = true
	}
	if !has["mcp__"+BrowserServer+"__browser_screenshot"] || has["mcp__"+BrowserServer+"__browser_extract_content"] {
		t.Fatalf("도구: %v", enrichTools)
	}
	if !strings.Contains(enrichSystem, "browser_screenshot") {
		t.Fatal("지시문이 스크린샷으로 읽으라고 해야 합니다")
	}
}
