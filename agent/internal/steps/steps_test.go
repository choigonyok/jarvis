package steps

import (
	"encoding/json"
	"testing"
)

func TestLine(t *testing.T) {
	cases := []struct {
		tool, input, want string
	}{
		// A job's browser, as its run log has always said it.
		{"mcp__mcp-browser__browser_navigate", `{"url":"https://web.joongna.com/product/form"}`, "열기: https://web.joongna.com/product/form"},
		{"mcp__mcp-browser__browser_upload_files", `{"paths":["a","b"]}`, "사진 2장 넣기"},
		{"mcp__mcp-browser__browser_fill_login", `{"site":"coupang.com"}`, "로그인 정보 넣기"},
		{"mcp__job__schedule_next", `{}`, ""},
		{"mcp__job__progress", `{"text":"글을 올립니다"}`, ""},
		{"mcp__web__search", `{"query":"양평 캠핑장 장작"}`, "검색: 양평 캠핑장 장작"},

		// The conversation's boards: what is looked at, and what is left.
		{"mcp__look__look", `{"what":"calendar","args":{"from":"2026-11-07"}}`, "일정 보기"},
		{"mcp__look__look", `{"what":"memory","args":{"query":"지난 캠핑"}}`, "기억 찾기: 지난 캠핑"},
		{"mcp__look__look", `{"what":"nearby","args":{"open":true}}`, "근처 찾기: 음식점"},
		{"mcp__look__look", `{"what":"photos","args":{"place":"양평"}}`, "사진 찾기: 양평"},
		{"mcp__look__look", `{"what":"help","args":{"name":"calendar"}}`, ""},
		{"mcp__look__look", `{"what":"weather"}`, "찾아보기"},
		{"mcp__change__change", `{"what":"reminder.set","args":{"text":"장갑 챙기기","at":"2026-11-07T09:00"}}`, "리마인드 걸기: 장갑 챙기기"},
		{"mcp__change__change", `{"what":"checklist.make","args":{"title":"캠핑 준비물","items":["장갑"]}}`, "체크리스트 만들기: 캠핑 준비물"},

		// Cards, and the rest of what the conversation sets down.
		{"mcp__calendar__create_event", `{"date":"2026-11-07","title":"캠핑"}`, "일정 넣기 카드: 캠핑"},
		{"mcp__calendar__delete_event", `{"id":"e1"}`, "일정 지우기 카드"},
		{"mcp__vault__save_login", `{"site":"coupang.com","name":"쿠팡"}`, "로그인 저장 카드: 쿠팡"},
		{"mcp__jobs__create_job", `{"title":"생수 주문","goal":"주문"}`, "작업 맡기기: 생수 주문"},
		{"mcp__show__show_image", `{"uploads":["a.jpg"]}`, "사진 띄우기"},

		// The CLI's own tools: a photo opened is said, its notes are not.
		{"WebSearch", `{"query":"성수 팝업"}`, "웹 검색: 성수 팝업"},
		{"Read", `{"file_path":"/data/uploads/3f2a.jpg"}`, "사진 보기"},
		{"Read", `{"file_path":"/data/memory/MEMORY.md"}`, ""},
		{"Write", `{"file_path":"/data/memory/camping.md"}`, "기억에 적기"},
		{"ToolSearch", `{"query":"calendar"}`, ""},
		{"TodoWrite", `{}`, ""},
	}
	for _, c := range cases {
		if got := Line(c.tool, json.RawMessage(c.input)); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.tool, c.input, got, c.want)
		}
	}
}

// A long thing typed or searched is cut where it would run on.
func TestLineIsShort(t *testing.T) {
	long := `{"text":"가나다라마바사아자차카타파하가나다라마바사아자차카타파하가나다라마바사아자차카타파하"}`
	got := Line("mcp__mcp-browser__browser_type", json.RawMessage(long))
	if r := []rune(got); len(r) != len([]rune("입력: "))+40+1 || r[len(r)-1] != '…' {
		t.Fatalf("%q", got)
	}
}
