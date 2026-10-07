// Package market sells things on Joongna (중고나라) for the operator.
//
// In conversation the model looks at the attached photos, checks prices, and
// raises a draft with post_listing. That draft is a card the operator edits
// and approves; approving stores it in market-svc. Everything that touches
// Joongna itself - posting, price changes, status, bumping, deleting, and the
// hourly look at how each listing is doing - is done later by the background
// worker (worker.go) with the shared browser, one task at a time.
package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

var (
	_ module.Editor        = (*Module)(nil)
	_ module.ContextSource = (*Module)(nil)
)

const (
	Name       = "market"
	ServerName = "market"
	KindPost   = "market.post_listing"
)

// QualifiedToolName is how Claude Code refers to one of these tools.
func QualifiedToolName(tool string) string {
	return fmt.Sprintf("mcp__%s__%s", ServerName, tool)
}

// Conditions are the choices on the card. Joongna's form words them its own
// way; the worker picks the closest one there.
var Conditions = []string{"새상품(미개봉)", "거의 새것", "사용감 적음", "사용감 많음", "고장·부품용"}

var shippingLabel = map[string]string{"included": "택배비 포함", "separate": "택배비 별도"}

type Module struct {
	store   *Store
	uploads *uploads.Store
	// kick wakes the worker when a draft is approved, so posting starts now
	// rather than at the next tick.
	kick chan struct{}
	// proposals is where the login card goes (Notify); nil in tests.
	proposals *proposal.Store
}

func New(store *Store, up *uploads.Store) *Module {
	return &Module{store: store, uploads: up, kick: make(chan struct{}, 1)}
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) Specs() []action.Spec {
	return []action.Spec{{
		Kind:    KindPost,
		Summary: "중고나라 판매 글 초안을 승인 카드로 올립니다. 승인되면 백그라운드에서 등록합니다.",
		// It goes out to a public marketplace under the operator's name.
		Reversible: false,
	}}
}

type PostInput struct {
	Photos      []string `json:"photos" jsonschema:"올릴 사진. 대화에 첨부된 사진 경로의 파일 이름(예: 3f2a...c1.jpg). 대표 사진을 맨 앞에."`
	Title       string   `json:"title" jsonschema:"글 제목. 40자 이내. 브랜드·모델·핵심 사양 위주. 예: '에어팟 프로 2세대 C타입 (풀박스)'"`
	PriceKrw    int64    `json:"priceKrw" jsonschema:"판매 가격(원). 운영자가 말한 가격만. 말하지 않았으면 0 - 운영자가 카드에서 적는다."`
	Description string   `json:"description" jsonschema:"본문. 상품 정보, 구성품, 상태(하자 포함), 구입 시기(아는 경우), 거래 방식(택배) 순으로 짧은 문단. 과장하지 않는다."`
	Category    string   `json:"category" jsonschema:"중고나라 카테고리 추정. 큰 분류 > 작은 분류. 예: '디지털기기 > 음향기기'"`
	Condition   string   `json:"condition" jsonschema:"상품 상태. 새상품(미개봉), 거의 새것, 사용감 적음, 사용감 많음, 고장·부품용 중 하나"`
	Shipping    string   `json:"shipping" jsonschema:"택배비. included(포함) 또는 separate(별도)"`
	Reference   string   `json:"reference,omitempty" jsonschema:"search_prices 로 본 시세 요약 한두 줄. 운영자가 가격을 정할 때 참고한다."`
}

func decodePost(raw json.RawMessage) (PostInput, error) {
	var in PostInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, err
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	if in.Shipping == "" {
		in.Shipping = "included"
	}
	// The model is told file names, but a full path is the natural thing to
	// copy from the prompt. Accept it and keep only the name.
	for i, p := range in.Photos {
		if j := strings.LastIndex(p, "/"); j >= 0 {
			in.Photos[i] = p[j+1:]
		}
	}
	return in, nil
}

// check is what a draft must satisfy to be shown. Price may still be 0 - the
// operator fills it in on the card - but must be set by the time it runs.
func (m *Module) check(in PostInput, final bool) error {
	switch {
	case len(in.Photos) == 0:
		return errors.New("사진이 한 장은 있어야 합니다. 대화에 첨부된 사진 이름을 photos 에 넣으세요.")
	case len(in.Photos) > 12:
		return errors.New("사진은 12장까지입니다.")
	case in.Title == "":
		return errors.New("제목이 비어 있습니다.")
	case len([]rune(in.Title)) > 40:
		return errors.New("제목은 40자까지입니다.")
	case in.Shipping != "included" && in.Shipping != "separate":
		return errors.New("shipping 은 included 또는 separate 입니다.")
	case in.PriceKrw < 0:
		return errors.New("가격이 음수입니다.")
	case final && in.PriceKrw == 0:
		return errors.New("가격을 적어 주세요.")
	}
	for _, p := range in.Photos {
		if _, err := m.uploads.Path(p); err != nil {
			return fmt.Errorf("사진 %s 를 찾을 수 없습니다. 대화에 첨부된 사진만 쓸 수 있습니다.", p)
		}
	}
	return nil
}

func (m *Module) Preview(_ context.Context, a action.Action) (action.Card, error) {
	if a.Kind != KindPost {
		return action.Card{}, fmt.Errorf("모르는 동작입니다: %s", a.Kind)
	}
	in, err := decodePost(a.Input)
	if err != nil {
		return action.Card{}, err
	}
	if err := m.check(in, false); err != nil {
		return action.Card{}, err
	}
	return card(in), nil
}

func card(in PostInput) action.Card {
	body := in.Reference
	if strings.TrimSpace(body) == "" {
		body = "시세 정보 없음"
	}
	price := ""
	if in.PriceKrw > 0 {
		price = strconv.FormatInt(in.PriceKrw, 10)
	}
	return action.Card{
		Title:       "중고나라에 판매 글을 올립니다",
		Body:        "시세 참고\n" + body,
		Consequence: "승인하면 백그라운드에서 내 계정으로 공개 등록합니다. 진행 상황은 중고나라 탭에서 볼 수 있습니다.",
		Images:      in.Photos,
		Fields: []action.Field{
			{Key: "title", Label: "제목", Kind: "text", Value: in.Title},
			{Key: "priceKrw", Label: "가격(원)", Kind: "number", Value: price},
			{Key: "condition", Label: "상태", Kind: "select", Value: in.Condition, Options: Conditions},
			{Key: "shipping", Label: "택배비", Kind: "select", Value: shippingLabel[in.Shipping],
				Options: []string{shippingLabel["included"], shippingLabel["separate"]}},
			{Key: "category", Label: "카테고리", Kind: "text", Value: in.Category},
			{Key: "description", Label: "설명", Kind: "textarea", Value: in.Description},
		},
	}
}

// Revise applies the card's edits. Every approval of a draft comes through
// here, edited or not, because this is where an empty price is caught.
func (m *Module) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	in, err := decodePost(a.Input)
	if err != nil {
		return a, action.Card{}, err
	}
	for k, v := range edits {
		v = strings.TrimSpace(v)
		switch k {
		case "title":
			in.Title = v
		case "priceKrw":
			digits := strings.NewReplacer(",", "", "원", "", " ", "").Replace(v)
			if digits == "" {
				in.PriceKrw = 0
				break
			}
			n, err := strconv.ParseInt(digits, 10, 64)
			if err != nil {
				return a, action.Card{}, errors.New("가격은 숫자로 적어 주세요.")
			}
			in.PriceKrw = n
		case "condition":
			in.Condition = v
		case "shipping":
			switch v {
			case shippingLabel["included"], "included":
				in.Shipping = "included"
			case shippingLabel["separate"], "separate":
				in.Shipping = "separate"
			default:
				return a, action.Card{}, errors.New("택배비는 포함 또는 별도입니다.")
			}
		case "category":
			in.Category = v
		case "description":
			in.Description = v
		default:
			return a, action.Card{}, fmt.Errorf("고칠 수 없는 항목입니다: %s", k)
		}
	}
	if err := m.check(in, true); err != nil {
		return a, action.Card{}, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return a, action.Card{}, err
	}
	return action.Action{Kind: a.Kind, Input: raw}, card(in), nil
}

func (m *Module) Execute(ctx context.Context, a action.Action) (action.Result, error) {
	in, err := decodePost(a.Input)
	if err != nil {
		return action.Result{}, err
	}
	if err := m.check(in, true); err != nil {
		return action.Result{}, err
	}
	l, err := m.store.Create(ctx, NewListing{
		Title:       in.Title,
		PriceKrw:    in.PriceKrw,
		Description: in.Description,
		Category:    in.Category,
		Condition:   in.Condition,
		Shipping:    in.Shipping,
		Photos:      in.Photos,
	})
	if err != nil {
		return action.Result{}, err
	}
	select {
	case m.kick <- struct{}{}:
	default:
	}
	return action.Result{Note: fmt.Sprintf("등록 대기 · %s %s", l.Title, krw(l.PriceKrw))}, nil
}

func (m *Module) Facts(ctx context.Context, _ string) ([]module.Fact, error) {
	ls, _, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []module.Fact
	for _, l := range ls {
		if l.Status == "active" || l.Status == "reserved" || l.Status == "queued" {
			out = append(out, module.Fact{Source: Name, Text: line(l)})
		}
	}
	return out, nil
}

// --- 대화 도구 ---------------------------------------------------------------

type SearchInput struct {
	Query string `json:"query" jsonschema:"검색어. 모델명 위주로 짧게. 예: '에어팟 프로 2 C타입'"`
}

type ListInput struct{}

// Handler serves the chat's tools. post_listing reaches here only after its
// card was approved - and is then run by the gate through Execute, not here.
func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "search_prices",
		Description: "중고나라에서 판매 중인 글의 시세를 본다: 평균·최저·최고 가격과 최근 글 몇 개. " +
			"부품·액세서리 글이 섞이므로 같은 물건만 골라 판단한다. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
		p, err := SearchPrices(ctx, in.Query, 12)
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(p.render(), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_listings",
		Description: "내가 중고나라에 올렸거나 올리려는 글의 현황: 상태, 가격, 조회·찜·채팅 수, 처리 중인 변경. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, any, error) {
		ls, st, err := m.store.List(ctx)
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(renderList(ls, st), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "post_listing",
		Description: "중고나라 판매 글 초안을 승인 카드로 올린다. 운영자가 카드에서 고쳐 승인하면 백그라운드에서 등록한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PostInput) (*mcp.CallToolResult, any, error) {
		// The gate runs an approved post through Execute and tells the model
		// so; a call that lands here was never approved.
		return toolText("이 도구는 승인 카드를 거쳐서만 실행됩니다. 승인 결과 메시지를 기다리세요.", true), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

var statusLabel = map[string]string{
	"queued": "등록 대기", "active": "판매중", "reserved": "예약중",
	"sold": "판매완료", "deleted": "삭제됨", "failed": "등록 실패",
}

var taskLabel = map[string]string{
	"post": "등록", "price": "가격 변경", "status": "상태 변경", "bump": "끌어올리기", "delete": "삭제",
}

func line(l Listing) string {
	s := fmt.Sprintf("[%s] %s · %s", statusLabel[l.Status], l.Title, krw(l.PriceKrw))
	if l.JoongnaID != "" {
		s += fmt.Sprintf(" · 조회 %d 찜 %d 채팅 %d", l.Views, l.Likes, l.Chats)
	}
	return s
}

func renderList(ls []Listing, st State) string {
	if len(ls) == 0 {
		return "중고나라에 올린 글이 없습니다."
	}
	var b strings.Builder
	if st.LoginRequired {
		b.WriteString("주의: 중고나라 로그인이 풀려 모든 작업이 멈춰 있습니다. 화면 탭에서 다시 로그인해야 합니다.\n\n")
	}
	for _, l := range ls {
		b.WriteString("- " + line(l))
		for _, t := range l.Tasks {
			if t.State == "pending" {
				fmt.Fprintf(&b, " · %s 대기", taskLabel[t.Kind])
			} else if t.State == "failed" {
				fmt.Fprintf(&b, " · %s 실패(%s)", taskLabel[t.Kind], t.Note)
			}
		}
		b.WriteString("\n")
	}
	if st.LastSyncAt != nil {
		fmt.Fprintf(&b, "\n마지막으로 중고나라에서 확인한 때: %s", st.LastSyncAt.Local().Format("1/2 15:04"))
	}
	return b.String()
}

func krw(v int64) string {
	s := strconv.FormatInt(v, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String() + "원"
}

func toolText(s string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
