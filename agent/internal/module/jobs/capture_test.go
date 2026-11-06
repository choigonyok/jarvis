package jobs

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/shot"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

// newsLog keeps the job news put in the conversation, with its photos.
type newsLog struct {
	mu     sync.Mutex
	images [][]string
}

func (n *newsLog) JobNews(_ thread.Origin, _, images []string) *thread.Turn {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.images = append(n.images, images)
	return nil
}
func (n *newsLog) AttachProposal(string) {}

// screen is the browser bridge's screenshot, a small PNG.
func screen(t *testing.T) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(2, 2, color.RGBA{G: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-browser", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: shot.Tool, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: b.Bytes(), MIMEType: "image/png"}}}, nil
	})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(srv.Close)
	return srv
}

// A run keeps the browser's screen as a photo for the operator - its line in
// the run's record, and on the conversation's job card with the report that
// names it (2026-11-06: "작업카드에서 사진 볼 수 있게"). A photo that is not
// the job's is not shown as its own.
func TestARunShowsTheScreenItTook(t *testing.T) {
	svc := &jobsSvc{}
	up, err := uploads.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	news := &newsLog{}
	m := New(NewStore(svc.serve(t).URL, ""), up, proposal.NewStore(bus.New()), news, quietLog())
	m.SetCamera(shot.New(screen(t).URL, ""))
	m.current.add(5, Job{ID: 16, Title: "책 주문", Sites: []string{"www.aladin.co.kr"}, Photos: []string{"given.jpg"}}, true)
	s := runSession(t, m, "5")

	text, isErr := callRun(t, s, "capture", nil)
	if isErr || !strings.HasPrefix(text, "찍었습니다: ") {
		t.Fatalf("화면을 찍어야 합니다: %q", text)
	}
	name := strings.Fields(strings.TrimPrefix(text, "찍었습니다: "))[0]
	if _, err := up.Path(name); err != nil {
		t.Fatalf("사진으로 남아야 합니다: %s %v", name, err)
	}
	if !slices.ContainsFunc(svc.got(), func(c string) bool { return strings.Contains(c, "/runs/5/") }) {
		t.Fatalf("실행 기록에 한 줄: %v", svc.got())
	}

	text, isErr = callRun(t, s, "report", map[string]any{"summary": "주문 완료", "notify": true, "images": []string{name, "given.jpg", "other.jpg"}})
	if isErr || !strings.Contains(text, "보이지 않은 것: other.jpg") {
		t.Fatalf("이 작업의 사진이 아닌 것은 빼고: %q", text)
	}
	if len(news.images) != 1 || !slices.Equal(news.images[0], []string{name, "given.jpg"}) {
		t.Fatalf("작업 카드에 찍은 화면과 작업 사진: %+v", news.images)
	}

	// No sites, no browser of its own: nothing to take.
	m.current.add(6, Job{ID: 17, Title: "조사"}, false)
	if text, isErr := callRun(t, runSession(t, m, "6"), "capture", nil); !isErr || !strings.Contains(text, "찍을 수 없습니다") {
		t.Fatalf("브라우저 없는 작업: %q", text)
	}
}
