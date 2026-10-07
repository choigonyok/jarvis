// Package spending lets the model read the household book: a month's total,
// where it went, how it is pacing against the budget, and what repeats.
//
// Read-only for the same reason as assets: no Actuator, so nothing for the
// gate to approve and no tool that writes. Recategorising and budgets are
// done on the 가계부 screen, where the person doing it sees what changes.
package spending

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

var _ module.ContextSource = (*Module)(nil)

const (
	Name       = "spending"
	ServerName = "spending"
)

func QualifiedToolName(tool string) string {
	return fmt.Sprintf("mcp__%s__%s", ServerName, tool)
}

// Tools is every tool this server has; all of them read.
var Tools = []string{QualifiedToolName("get_spending")}

// --- wire shape (spending-svc GET /spending) --------------------------------

type Tx struct {
	Merchant      string    `json:"merchant"`
	Category      string    `json:"category"`
	AmountKrw     int64     `json:"amountKrw"`
	Currency      string    `json:"currency"`
	ForeignAmount *float64  `json:"foreignAmount"`
	Estimated     bool      `json:"estimated"`
	Installment   int       `json:"installment"`
	Issuer        string    `json:"issuer"`
	ApprovedAt    time.Time `json:"approvedAt"`
	Memo          string    `json:"memo"`
	Status        string    `json:"status"`
	SignedKrw     int64     `json:"signedKrw"`
	Items         []struct {
		Name      string `json:"name"`
		Quantity  int    `json:"quantity"`
		AmountKrw int64  `json:"amountKrw"`
		Category  string `json:"category"`
	} `json:"items"`
	EnrichStatus *string `json:"enrichStatus"`
}

type Category struct {
	Name      string `json:"name"`
	TotalKrw  int64  `json:"totalKrw"`
	Count     int    `json:"count"`
	BudgetKrw *int64 `json:"budgetKrw"`
	PrevKrw   int64  `json:"prevKrw"`
}

type Recurring struct {
	Merchant  string `json:"merchant"`
	AmountKrw int64  `json:"amountKrw"`
	NextAt    string `json:"nextAt"`
	Months    int    `json:"months"`
}

type Book struct {
	Month       string `json:"month"`
	DaysInMonth int    `json:"daysInMonth"`
	ElapsedDays int    `json:"elapsedDays"`
	TotalKrw    int64  `json:"totalKrw"`
	Count       int    `json:"count"`
	Prev        struct {
		Month      string `json:"month"`
		TotalKrw   int64  `json:"totalKrw"`
		SameDayKrw int64  `json:"sameDayKrw"`
	} `json:"prev"`
	BudgetKrw    *int64     `json:"budgetKrw"`
	Categories   []Category `json:"categories"`
	TopMerchants []struct {
		Name     string `json:"name"`
		TotalKrw int64  `json:"totalKrw"`
		Count    int    `json:"count"`
	} `json:"topMerchants"`
	Transactions []Tx        `json:"transactions"`
	Recurring    []Recurring `json:"recurring"`
	Unparsed     []struct {
		ChatName string `json:"chatName"`
	} `json:"unparsed"`
	Collector struct {
		LatestMessageAt *time.Time `json:"latestMessageAt"`
		LastAlertAt     *time.Time `json:"lastAlertAt"`
		Stale           bool       `json:"stale"`
		LastError       string     `json:"lastError"`
	} `json:"collector"`
}

// --- client ---------------------------------------------------------------

type Store struct {
	base   string
	token  string
	client *http.Client
}

func NewStore(baseURL, token string) *Store {
	return &Store{base: strings.TrimRight(baseURL, "/"), token: token,
		client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *Store) Month(ctx context.Context, month string) (Book, error) {
	path := "/spending"
	if month != "" {
		path += "?month=" + url.QueryEscape(month)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return Book{}, err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return Book{}, fmt.Errorf("가계부 서비스에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
			return Book{}, errors.New(payload.Error)
		}
		return Book{}, fmt.Errorf("가계부 서비스가 거부했습니다: %s", res.Status)
	}
	var b Book
	return b, json.NewDecoder(res.Body).Decode(&b)
}

// --- module ---------------------------------------------------------------

type Module struct{ store *Store }

func New(store *Store) *Module { return &Module{store: store} }

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) Facts(ctx context.Context, _ string) ([]module.Fact, error) {
	b, err := m.store.Month(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []module.Fact
	for _, line := range strings.Split(strings.TrimSpace(renderHead(b)), "\n") {
		out = append(out, module.Fact{Source: Name, Text: line})
	}
	return out, nil
}

type MonthInput struct {
	Month    string `json:"month,omitempty" jsonschema:"조회할 달. YYYY-MM. 비우면 이번 달."`
	Category string `json:"category,omitempty" jsonschema:"이 분류의 내역만 나열한다(예: 식비). 비우면 전체에서 최근 순."`
}

func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_spending",
		Description: "가계부(카드 결제 알림으로 모은 지출)를 조회한다: 월 총액, 지난달 같은 날까지와 비교, 예산 대비 속도, " +
			"분류별 합계, 많이 쓴 곳, 정기 결제, 건별 내역(날짜·사용처·금액·분류). 읽기 전용.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MonthInput) (*mcp.CallToolResult, any, error) {
		b, err := m.store.Month(ctx, in.Month)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: render(b, in.Category)}}}, b, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

// --- rendering ------------------------------------------------------------

var weekdays = [...]string{"일", "월", "화", "수", "목", "금", "토"}

func renderHead(b Book) string {
	var s strings.Builder
	fmt.Fprintf(&s, "%s 지출 %s (%d건, %d일 중 %d일 경과)\n", b.Month, krw(b.TotalKrw), b.Count, b.DaysInMonth, b.ElapsedDays)
	if b.ElapsedDays < b.DaysInMonth {
		diff := b.TotalKrw - b.Prev.SameDayKrw
		fmt.Fprintf(&s, "지난달(%s) 같은 날까지 %s → %s\n", b.Prev.Month, krw(b.Prev.SameDayKrw), more(diff))
	} else {
		fmt.Fprintf(&s, "지난달(%s) 전체 %s → %s\n", b.Prev.Month, krw(b.Prev.TotalKrw), more(b.TotalKrw-b.Prev.TotalKrw))
	}
	if b.BudgetKrw != nil && *b.BudgetKrw > 0 {
		budget := *b.BudgetKrw
		pace := int64(math.Round(float64(budget) * float64(b.ElapsedDays) / float64(b.DaysInMonth)))
		fmt.Fprintf(&s, "예산 %s 중 %.0f%% 사용, 오늘까지 적정 %s → %s\n",
			krw(budget), 100*float64(b.TotalKrw)/float64(budget), krw(pace), more(b.TotalKrw-pace))
		if b.ElapsedDays > 0 && b.ElapsedDays < b.DaysInMonth {
			projected := b.TotalKrw * int64(b.DaysInMonth) / int64(b.ElapsedDays)
			fmt.Fprintf(&s, "이 속도면 월말 %s (단순 추정)\n", krw(projected))
		}
	}
	c := b.Collector
	switch {
	case c.LastError != "":
		s.WriteString("주의: 카톡 수집기에 연결되지 않아 최근 결제가 빠졌을 수 있음 (" + c.LastError + ")\n")
	case c.Stale:
		s.WriteString("주의: 카톡 메시지가 한동안 들어오지 않음. 맥의 카카오톡이 꺼져 있으면 그동안의 결제가 빠짐\n")
	}
	if n := len(b.Unparsed); n > 0 {
		fmt.Fprintf(&s, "읽지 못한 카드 알림 %d건이 확인 대기 중 (합계에 없음)\n", n)
	}
	return s.String()
}

func render(b Book, only string) string {
	var s strings.Builder
	s.WriteString(renderHead(b))

	s.WriteString("\n분류별\n")
	for _, c := range b.Categories {
		line := fmt.Sprintf("- %s %s (%d건, 지난달 %s)", c.Name, krw(c.TotalKrw), c.Count, krw(c.PrevKrw))
		if c.BudgetKrw != nil {
			line += fmt.Sprintf(" · 예산 %s 중 %s 남음", krw(*c.BudgetKrw), krw(*c.BudgetKrw-c.TotalKrw))
		}
		s.WriteString(line + "\n")
	}
	if len(b.TopMerchants) > 0 {
		s.WriteString("\n많이 쓴 곳\n")
		for _, m := range b.TopMerchants {
			fmt.Fprintf(&s, "- %s %s (%d건)\n", m.Name, krw(m.TotalKrw), m.Count)
		}
	}
	if len(b.Recurring) > 0 {
		s.WriteString("\n정기 결제\n")
		for _, r := range b.Recurring {
			fmt.Fprintf(&s, "- %s %s, 다음 예상 %s (%d개월째)\n", r.Merchant, krw(r.AmountKrw), r.NextAt, r.Months)
		}
	}

	title, limit := "\n최근 내역 (최대 30건)\n", 30
	if only != "" {
		title, limit = "\n"+only+" 내역\n", 200
	}
	s.WriteString(title)
	n := 0
	for _, t := range b.Transactions {
		if only != "" && t.Category != only {
			continue
		}
		if n >= limit {
			break
		}
		n++
		s.WriteString("- " + line(t) + "\n")
		for _, it := range t.Items {
			fmt.Fprintf(&s, "    · %s ×%d · %s · %s\n", it.Name, it.Quantity, it.Category, krw(it.AmountKrw))
		}
	}
	if n == 0 {
		s.WriteString("- 없음\n")
	}
	return s.String()
}

func line(t Tx) string {
	d := t.ApprovedAt.Local()
	out := fmt.Sprintf("%d/%d(%s) %s %s · %s · %s", int(d.Month()), d.Day(), weekdays[d.Weekday()],
		d.Format("15:04"), t.Merchant, t.Category, krw(t.AmountKrw))
	if t.ForeignAmount != nil {
		out += fmt.Sprintf(" ($%.2f", *t.ForeignAmount)
		if t.Estimated {
			out += ", 원화는 추정"
		}
		out += ")"
	}
	if t.Installment > 0 {
		out += fmt.Sprintf(" · %d개월 할부", t.Installment)
	}
	switch t.Status {
	case "cancelled":
		out += " · 취소됨(합계 제외)"
	case "cancel":
		out += " · 취소 알림"
	case "refund":
		out += " · 부분 취소(환급)"
	case "excluded":
		out += " · 합계에서 뺌"
	}
	if t.EnrichStatus != nil {
		switch *t.EnrichStatus {
		case "pending":
			out += " · 주문 상품 조회 대기"
		case "login_required":
			out += " · 주문 상품 조회 멈춤(사이트 로그인 필요)"
		case "not_found":
			out += " · 주문을 찾지 못함"
		}
	}
	if t.Memo != "" {
		out += " · 메모: " + t.Memo
	}
	return out
}

func krw(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d", v)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "−₩" + b.String()
	}
	return "₩" + b.String()
}

func more(diff int64) string {
	switch {
	case diff > 0:
		return krw(diff) + " 더 씀"
	case diff < 0:
		return krw(-diff) + " 덜 씀"
	}
	return "같음"
}
