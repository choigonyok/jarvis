package spending

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
)

// EnrichServerName is the MCP server only the background lookup is given.
// It is mounted on this agent but never put in the chat's MCP config, so the
// conversation cannot see or call these tools.
const EnrichServerName = "spending-enrich"

// BrowserServer is the JARVIS_EXTRA_MCP entry for the shared browser.
const BrowserServer = "mcp-browser"

// enrichTools is everything the background lookup may call. Reading and
// clicking through order pages - no typing, so no logging in, no search
// boxes, no checkout forms - and the two tools that write its answer.
var enrichTools = []string{
	"mcp__" + BrowserServer + "__browser_navigate",
	"mcp__" + BrowserServer + "__browser_get_state",
	"mcp__" + BrowserServer + "__browser_extract_content",
	"mcp__" + BrowserServer + "__browser_scroll",
	"mcp__" + BrowserServer + "__browser_click",
	"mcp__" + BrowserServer + "__browser_go_back",
	"mcp__" + BrowserServer + "__browser_list_tabs",
	"mcp__" + BrowserServer + "__browser_switch_tab",
	"mcp__" + BrowserServer + "__browser_close_tab",
	"mcp__" + EnrichServerName + "__save_order_items",
	"mcp__" + EnrichServerName + "__report_lookup",
}

// --- spending-svc calls ----------------------------------------------------

type Pending struct {
	ID          int64     `json:"id"`
	Source      string    `json:"source"`
	SourceLabel string    `json:"sourceLabel"`
	Merchant    string    `json:"merchant"`
	AmountKrw   int64     `json:"amountKrw"`
	ApprovedAt  time.Time `json:"approvedAt"`
	Issuer      string    `json:"issuer"`
	CardTail    string    `json:"cardTail"`
	Note        string    `json:"note"`
}

func (s *Store) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("가계부 서비스에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
			return errors.New(payload.Error)
		}
		return fmt.Errorf("가계부 서비스가 거부했습니다: %s", res.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (s *Store) Pending(ctx context.Context) ([]Pending, error) {
	var payload struct {
		Transactions []Pending `json:"transactions"`
	}
	err := s.call(ctx, http.MethodGet, "/enrich/pending?limit=5", nil, &payload)
	return payload.Transactions, err
}

func (s *Store) report(ctx context.Context, id int64, outcome, note string) error {
	return s.call(ctx, http.MethodPost, fmt.Sprintf("/transactions/%d/enrich-report", id),
		map[string]string{"outcome": outcome, "note": note}, nil)
}

// --- the background lookup's own tools ------------------------------------

type ItemInput struct {
	Name     string `json:"name" jsonschema:"상품 이름. 옵션·용량까지 짧게. 예: '마이프로틴 임팩트 웨이 초코 1kg'"`
	Quantity int    `json:"quantity" jsonschema:"수량"`
	PriceKrw int64  `json:"priceKrw" jsonschema:"주문서에 적힌 이 줄의 금액(단가×수량). 할인 전이든 후든 주문서에 보이는 그대로."`
	Category string `json:"category" jsonschema:"분류. 식비, 카페·간식, 편의점·마트, 교통, 쇼핑, 구독, 생활·통신, 의료·건강, 미용·뷰티, 운동, 문화·여가, 여행, 기타 중 하나"`
}

type SaveInput struct {
	TransactionID int64       `json:"transactionId" jsonschema:"작업 목록의 결제 id"`
	Items         []ItemInput `json:"items"`
	Note          string      `json:"note,omitempty" jsonschema:"주문번호나 주문일처럼 나중에 확인할 단서. 짧게."`
}

type ReportInput struct {
	TransactionID int64  `json:"transactionId"`
	Outcome       string `json:"outcome" jsonschema:"not_found(주문을 찾지 못함) | login_required(로그인 화면이 나옴) | failed(그 밖의 이유로 못 읽음)"`
	Note          string `json:"note" jsonschema:"무엇을 봤는지 한 줄"`
}

// EnrichHandler serves the two tools that write the lookup's answer.
func (m *Module) EnrichHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: EnrichServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "save_order_items",
		Description: "결제 한 건이 어떤 상품들이었는지 기록한다. 카드 결제 금액과 상품 금액이 다르면(쿠폰·캐시·배송비) 서버가 맞춰 나눈다. " +
			"금액 차이가 너무 크면 다른 주문이라고 거절하니, 그때는 다른 주문을 찾는다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SaveInput) (*mcp.CallToolResult, any, error) {
		body := map[string]any{"items": in.Items, "note": in.Note}
		if err := m.store.call(ctx, http.MethodPost, fmt.Sprintf("/transactions/%d/items", in.TransactionID), body, nil); err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(fmt.Sprintf("결제 %d 의 상품 %d개를 기록했습니다.", in.TransactionID, len(in.Items)), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "report_lookup",
		Description: "상품을 기록하지 못한 결제의 결과를 남긴다. 모든 결제는 save_order_items 나 이것 중 하나로 끝나야 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReportInput) (*mcp.CallToolResult, any, error) {
		if err := m.store.report(ctx, in.TransactionID, in.Outcome, in.Note); err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText("기록했습니다.", false), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

func toolText(s string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// --- the loop --------------------------------------------------------------

type Background interface {
	Idle() bool
	Background(ctx context.Context, t claudecode.Task) (string, error)
}

type EnrichConfig struct {
	// EnrichURL is this agent's own spending-enrich endpoint.
	EnrichURL string
	// Interval is how often the queue is checked. Checking is one HTTP call;
	// the model only runs when there is something in it.
	Interval time.Duration
	// Cooldown is the least time between two lookups. Marketplaces block
	// clients that open order pages too often.
	Cooldown time.Duration
	Timeout  time.Duration
}

// RunEnricher wakes on new marketplace charges and nothing else.
func (m *Module) RunEnricher(ctx context.Context, bg Background, cfg EnrichConfig, log *slog.Logger) {
	var last time.Time
	tick := time.NewTicker(cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if time.Since(last) < cfg.Cooldown || !bg.Idle() {
			continue
		}
		pending, err := m.store.Pending(ctx)
		if err != nil || len(pending) == 0 {
			continue
		}
		last = time.Now()
		m.enrichOnce(ctx, bg, cfg, pending, log)
	}
}

func (m *Module) enrichOnce(ctx context.Context, bg Background, cfg EnrichConfig, pending []Pending, log *slog.Logger) {
	ids := make([]string, len(pending))
	for i, p := range pending {
		ids[i] = fmt.Sprint(p.ID)
	}
	log.Info("묶음 결제의 주문을 조회합니다", "count", len(pending), "ids", strings.Join(ids, ","))

	result, err := bg.Background(ctx, claudecode.Task{
		Name:    "주문 조회",
		Prompt:  enrichPrompt(pending),
		System:  enrichSystem,
		Servers: map[string]string{EnrichServerName: cfg.EnrichURL},
		Extra:   []string{BrowserServer},
		Allowed: enrichTools,
		Timeout: cfg.Timeout,
	})
	switch {
	case errors.Is(err, claudecode.ErrYielded), errors.Is(err, claudecode.ErrBusy):
		// The person needed the assistant. Nothing is recorded; the same
		// charges come up again after the cooldown.
		log.Info("주문 조회를 미뤘습니다", "reason", err)
		return
	case err != nil:
		log.Warn("주문 조회가 실패했습니다", "err", err)
	default:
		log.Info("주문 조회를 마쳤습니다", "result", clampText(result, 300))
	}

	// Every charge in the batch must end somewhere. One the model never
	// answered for would come straight back on the next tick and be retried
	// forever; reporting it counts as an attempt, and three end it.
	// Ask again rather than trusting the batch: one the model answered
	// (items, or not_found which moves it later) is no longer due, and
	// counting it a second time would end its retries early.
	bctx := context.WithoutCancel(ctx)
	still, qerr := m.store.Pending(bctx)
	if qerr != nil {
		return
	}
	asked := map[int64]bool{}
	for _, p := range pending {
		asked[p.ID] = true
	}
	note := "조회가 끝났지만 결과가 남지 않았습니다"
	if err != nil {
		note = clampText(err.Error(), 150)
	}
	for _, p := range still {
		if asked[p.ID] {
			_ = m.store.report(bctx, p.ID, "failed", note)
		}
	}
}

const enrichSystem = `당신은 Jarvis의 백그라운드 가계부 작업입니다. 대화 상대는 없고, 결과는 도구로만 남깁니다.

할 일: 카드 결제 알림에 "쿠팡 8,900원"처럼 가맹점만 찍힌 결제가 실제로 어떤 상품이었는지, 이미 로그인된 브라우저로 그 사이트의 주문 내역을 읽어 기록합니다.

반드시 지킬 것
- 주문 목록·주문 상세 페이지만 봅니다. 장바구니, 구매, 결제, 취소, 반품, 리뷰, 광고 버튼은 절대 누르지 마세요.
- 글자를 입력할 수 없습니다. 로그인 화면이 나오면:
  - 네이버 로그인 화면이고 아이디·비밀번호 칸이 브라우저에 저장된 값으로 이미 채워져 있으면, 다른 것은 아무것도 누르지 말고 곧바로 로그인 버튼을 한 번 누르세요. "로그인 상태 유지"나 다른 체크박스·탭을 먼저 누르면 채워진 칸이 지워집니다. 그 뒤 원래 보려던 내역 페이지로 돌아가 이어서 하세요.
  - 칸이 비어 있거나, 보안문자·새 기기 인증·추가 확인이 나오거나, 한 번 눌러서 로그인되지 않으면 더 시도하지 말고 report_lookup(outcome=login_required)으로 끝내세요.
  - 쿠팡 로그인 화면이면 누르지 말고 바로 login_required 입니다.
  - 한 건만 보고하면 같은 사이트의 나머지 결제도 함께 멈추므로, 그 사이트의 다른 결제는 따로 보고하지 않아도 됩니다.
- 같은 페이지를 반복해서 새로 고치지 마세요. 필요한 만큼만 스크롤하세요.
- 작업이 끝나면 직접 연 탭은 닫으세요. 원래 있던 탭은 건드리지 마세요.

주문 찾기
- 결제 시각과 같은 날, 결제 시각 몇 분 이내에 주문된 건을 찾습니다. 금액은 쿠폰·쿠팡캐시·적립금 때문에 카드 금액보다 클 수 있고, 배송비 때문에 작을 수 있습니다.
- 쿠팡 주문목록: https://mc.coupang.com/ssr/desktop/order/list
- 네이버페이 결제내역: https://pay.naver.com/pc/history (또는 https://pay.naver.com/pcpay)
- 네이버페이 쪽은 위 내역 페이지만 열 수 있습니다. 그 밖의 pay.naver.com 페이지와 버튼은 결제 안전장치에 막힙니다. 막혀서 열리지 않으면 우회하지 말고, 무엇이 막혔는지 적어 report_lookup(outcome=failed)으로 끝내세요.
- 맞는 주문이 확실하지 않으면 추측해서 기록하지 말고 not_found로 남기세요.

기록하기
- 찾았으면 save_order_items로 상품마다 이름·수량·주문서에 보이는 금액·분류를 남깁니다.
- 분류는 상품이 무엇인지로 고릅니다: 먹는 것(식품·음료·간편식)은 식비, 간식·과자·커피는 카페·간식, 휴지·세제·주방·욕실용품은 생활·통신, 의약품·영양제·위생의료용품은 의료·건강, 화장품·헤어·스킨케어·미용도구는 미용·뷰티, 운동용품·보충제(프로틴 등)는 운동, 책·음반·게임은 문화·여가, 옷·전자기기·가구·잡화는 쇼핑. 애매하면 쇼핑.
- 결제 한 건마다 save_order_items 또는 report_lookup 중 하나는 꼭 호출하세요.`

func enrichPrompt(pending []Pending) string {
	var b strings.Builder
	b.WriteString("아래 결제들의 주문 상품을 찾아 기록하세요.\n\n")
	for _, p := range pending {
		at := p.ApprovedAt.Local()
		fmt.Fprintf(&b, "- 결제 id %d: %s(%s) %s %s, 카드 결제 %s",
			p.ID, p.SourceLabel, p.Merchant, at.Format("2006-01-02"), at.Format("15:04"), krw(p.AmountKrw))
		if p.Note != "" {
			fmt.Fprintf(&b, " (지난번: %s)", p.Note)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func clampText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
