package permission

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

// The cards that showed their arguments in braces (2026-10-20): a job made and
// a job instructed, as the model sent them. A card reads as words.
func TestCardsReadAsWordsNotJSON(t *testing.T) {
	g := &Gate{modules: module.NewRegistry()}
	cases := []struct {
		tool, input   string
		title         string
		has           []string
		consequenceIn string
	}{
		{
			tool:  "mcp__jobs__instruct_job",
			input: `{"id":27,"text":"배송 조건 변경(운영자 지시): 캠핑 전에 꼭 와야 한다. 10/23(금)까지 도착이 확실한 판매처만 고른다."}`,
			title: "작업 27에 지시를 더합니다",
			has:   []string{"배송 조건 변경(운영자 지시): 캠핑 전에 꼭 와야 한다."},
		},
		{
			tool: "mcp__jobs__create_job",
			input: `{"title":"카즈미 레노칸 나이프 최저가 구매","goal":"배송비 포함 최저가로 주문을 마친다.",` +
				`"instructions":"- 상품: 카즈미 필드 레노칸 나이프\n- 수량 1개","sites":["search.shopping.naver.com","www.coupang.com"],"photos":[]}`,
			title: "작업을 맡깁니다: 카즈미 레노칸 나이프 최저가 구매",
			has:   []string{"목표: 배송비 포함 최저가로 주문을 마친다.", "지시:\n  - 상품: 카즈미 필드 레노칸 나이프\n  - 수량 1개", "쓰는 사이트: search.shopping.naver.com, www.coupang.com"},
		},
		{
			tool:  "mcp__browser__browser_click",
			input: `{"index":12,"element":"구매하기 버튼","ref":"e12","new_tab":false}`,
			title: "브라우저에서 누릅니다",
			has:   []string{"대상: 구매하기 버튼", "번호: 12", "새 탭: 아니요"},
		},
		{
			tool:  "mcp__browser__browser_navigate",
			input: `{"url":"https://www.coupang.com/vp/products/1"}`,
			title: "브라우저로 주소를 엽니다",
			has:   []string{"https://www.coupang.com/vp/products/1"},
		},
		{
			tool:  "mcp__mind__remember_thing",
			input: `{"what":{"name":"장작","amount":10.5},"tags":["캠핑","겨울"],"items":[{"name":"참나무","kg":10}]}`,
			title: "판단 도구를 씁니다: remember thing",
			has:   []string{"what:\n  이름: 장작\n  금액: 10.5", "tags: 캠핑, 겨울", "items:\n  - 이름: 참나무\n    kg: 10"},
		},
	}
	for _, c := range cases {
		var in map[string]any
		if err := json.Unmarshal([]byte(c.input), &in); err != nil {
			t.Fatal(err)
		}
		_, card, err := g.describe(c.tool, in, in)
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		if card.Title != c.title {
			t.Errorf("%s title %q, want %q", c.tool, card.Title, c.title)
		}
		if strings.ContainsAny(card.Body, "{}") || strings.Contains(card.Body, `"`) {
			t.Errorf("%s: JSON on the card:\n%s", c.tool, card.Body)
		}
		for _, h := range c.has {
			if !strings.Contains(card.Body, h) {
				t.Errorf("%s: body lacks %q:\n%s", c.tool, h, card.Body)
			}
		}
		if strings.Contains(card.Body, "e12") || strings.Contains(card.Consequence, "Claude Code") {
			t.Errorf("%s: a handle or the CLI's name on the card: %q / %q", c.tool, card.Body, card.Consequence)
		}
	}
}
