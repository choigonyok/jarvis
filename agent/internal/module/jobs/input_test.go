package jobs

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/shot"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

// captchaScreen is the browser bridge showing a 보안문자 page: white, with
// the box that asks in the middle.
func captchaScreen(t *testing.T) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1440, 900))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(482, 118, 942, 696), image.NewUniform(color.RGBA{60, 60, 60, 255}), image.Point{}, draw.Src)
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

// A 보안문자 the operator can answer in words comes to the conversation as a
// card: the part that asks cut out large and the whole screen, one field to
// type the answer in, and answers for having done it on the 화면 tab or not
// at all. The run ends waiting on it, like any question.
func TestInputCardCarriesTheScreen(t *testing.T) {
	svc := &jobsSvc{}
	up, err := uploads.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), up, props, &transcript{}, quietLog())
	m.SetCamera(shot.New(captchaScreen(t).URL, ""))
	m.current.add(9, Job{ID: 177, Title: "벨락 수저 최저가", Sites: []string{"search.shopping.naver.com"}}, true)
	s := runSession(t, m, "9")

	text, isErr := callRun(t, s, "request_input", map[string]any{
		"ask": "영수증 보안문자 - 가게 위치는 Domaan St. [?] (빈칸 채우기)", "label": "보안문자 답", "site": "https://search.shopping.naver.com/search/all",
	})
	if isErr || !strings.Contains(text, "화면 사진 2장") || !strings.Contains(text, "browser_switch_tab") {
		t.Fatalf("카드를 올리고 탭을 둔 채 마치라고 해야 합니다: %q", text)
	}
	cards := props.List()
	if len(cards) != 1 || cards[0].Action.Kind != KindAsk {
		t.Fatalf("질문 카드 하나: %+v", cards)
	}
	c := cards[0].Card
	if !c.Screen || len(c.Images) != 2 {
		t.Fatalf("잘라 낸 부분과 전체 화면, 화면 카드로: screen=%v images=%v", c.Screen, c.Images)
	}
	for _, name := range c.Images {
		if _, err := up.Path(name); err != nil {
			t.Fatalf("사진이 남아 있어야 합니다: %s", name)
		}
	}
	if !strings.Contains(c.Title, "보안문자 답") || !strings.Contains(c.Body, "search.shopping.naver.com") || !strings.Contains(c.Body, "Domaan St.") {
		t.Fatalf("무엇을 묻는지: %q / %q", c.Title, c.Body)
	}
	if len(c.Fields) != 1 || c.Fields[0].Key != "answer" || !c.Fields[0].Required || c.Fields[0].Kind != "text" {
		t.Fatalf("답 칸 하나, 필수: %+v", c.Fields)
	}
	if err := c.CheckChoices(); err != nil || len(c.Choices) != 3 ||
		c.Choices[1].Values["answer"] != doneOnScreen || c.Choices[1].Tone != action.ToneNeutral ||
		c.Choices[2].Decision != action.ChoiceReject {
		t.Fatalf("넣기 · 화면 탭에서 직접 · 하지 않기: %+v %v", c.Choices, err)
	}
	got := svc.got()
	shots := 0
	for _, call := range got {
		if call == "/runs/9/step" {
			shots++
		}
	}
	if shots != 2 || !contains(got, "/runs/9/wait "+cards[0].ID) {
		t.Fatalf("찍은 화면 둘을 실행 기록에, 실행은 카드에 걸려야 합니다: %v", got)
	}

	// Done on the 화면 tab: the job hears that, not something to type.
	in := AskInput{Question: c.Title, Fields: []action.Field{{Key: "answer", Label: "보안문자 답", Value: doneOnScreen}}}
	if a := answerOf(in, proposal.Approve, "", "화면 탭에서 직접 했어요"); !strings.Contains(a, "- 보안문자 답: "+doneOnScreen) {
		t.Fatalf("작업이 듣는 답: %q", a)
	}
}

// Without a browser to take, or without saying what is asked, no card.
func TestInputCardNeedsTheScreenAndTheQuestion(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, &transcript{}, quietLog())
	m.current.add(3, Job{ID: 5, Title: "조회", Sites: []string{"www.coupang.com"}}, true)
	s := runSession(t, m, "3")
	if text, isErr := callRun(t, s, "request_input", map[string]any{"ask": "인증번호", "label": " "}); !isErr || !strings.Contains(text, "label") {
		t.Fatalf("칸 이름이 없으면 돌려보냅니다: %q", text)
	}
	if text, isErr := callRun(t, s, "request_input", map[string]any{"ask": "인증번호 6자리", "label": "인증번호"}); !isErr || !strings.Contains(text, "request_captcha") {
		t.Fatalf("화면을 찍을 수 없으면 화면 탭으로: %q", text)
	}
	if n := len(props.List()); n != 0 {
		t.Fatalf("카드 %d 장", n)
	}
}
