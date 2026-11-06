// Package steps says a tool call in one line a person can follow: where it
// went, what it pressed and typed, what it looked up or set down. A job's run
// log (module/jobs) and the conversation's line of what it is doing now
// (thread.Store.Step) both read it, so the two say the same thing the same
// way.
package steps

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Line is the call said in a line, or "" for one a person watching has no use
// for: the model's own bookkeeping (loading tools, its to-do list, a skill,
// a board's help) and a job's own tools, which log themselves in mind.
func Line(tool string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	server, name := split(tool)
	switch server {
	case "look":
		args, _ := in["args"].(map[string]any)
		return look(str(in, "what"), args)
	case "change":
		args, _ := in["args"].(map[string]any)
		return change(str(in, "what"), args)
	}
	switch name {
	// The browser, in a job or in the conversation.
	case "browser_navigate":
		return "열기: " + clamp(str(in, "url"), 120)
	case "browser_click":
		return "누르기"
	case "browser_type":
		return "입력: " + clamp(str(in, "text"), 40)
	case "browser_get_state", "browser_extract_content", "browser_get_html":
		return "화면 읽기"
	case "browser_screenshot":
		return "화면 보기"
	case "browser_scroll":
		return "스크롤"
	case "browser_go_back":
		return "뒤로"
	case "browser_upload_files":
		n := 0
		if ps, ok := in["paths"].([]any); ok {
			n = len(ps)
		}
		return fmt.Sprintf("사진 %d장 넣기", n)
	case "browser_fill_login":
		return "로그인 정보 넣기"
	case "browser_enter_payment_pin":
		return "결제 비밀번호 넣기"
	case "view_photos":
		return "사진 보기"

	// Finding things out.
	case "search":
		return "검색: " + clamp(str(in, "query"), 80)
	case "read":
		return "읽기: " + clamp(str(in, "url"), 120)
	case "WebSearch":
		return "웹 검색: " + clamp(str(in, "query"), 80)
	case "WebFetch":
		return "읽기: " + clamp(str(in, "url"), 120)
	case "Read":
		// A photo sent with the message is opened with Read; anything else
		// it reads is its own notes.
		if picture(str(in, "file_path")) {
			return "사진 보기"
		}
		return ""
	case "Write", "Edit":
		// Only its memory is written without a card (config: MemoryDir).
		return "기억에 적기"

	// What raises a card or leaves something, in the conversation.
	case "create_event":
		return about("일정 넣기 카드", str(in, "title"))
	case "update_event":
		return about("일정 고치기 카드", str(in, "title"))
	case "delete_event":
		return "일정 지우기 카드"
	case "save_login":
		return about("로그인 저장 카드", first(str(in, "name"), str(in, "site")))
	case "forget_login":
		return about("로그인 정보 지우기", first(str(in, "name"), str(in, "site")))
	case "save_payment_pin":
		return about("결제 비밀번호 저장 카드", str(in, "pay"))
	case "forget_payment_pin":
		return about("결제 비밀번호 지우기", str(in, "pay"))
	case "save_shipping_address":
		return "배송지 저장"
	case "forget_shipping_address":
		return "배송지 지우기"
	case "create_job":
		return about("작업 맡기기", str(in, "title"))
	case "instruct_job":
		return about("작업에 지시", clamp(str(in, "text"), 40))
	case "show_image":
		return about("사진 띄우기", clamp(str(in, "caption"), 40))
	case "save_order_items":
		return "주문 상품 적기"
	case "report_lookup":
		return "주문 조회 결과 적기"
	}
	return ""
}

// look is a row of the look board said in a line. A row added later with no
// line of its own is still something looked at.
func look(what string, args map[string]any) string {
	switch what {
	case "", "help":
		return ""
	case "messages":
		return about("메시지 찾기", clamp(str(args, "query"), 40))
	case "message_context":
		return "메시지 앞뒤 보기"
	case "calendar":
		return "일정 보기"
	case "plans":
		return "대화에서 정한 일정 보기"
	case "memory":
		return about("기억 찾기", clamp(str(args, "query"), 40))
	case "spending":
		return "가계부 보기"
	case "reminders":
		return "리마인드 보기"
	case "rules":
		return "규칙 보기"
	case "loops":
		return "약속·부탁 보기"
	case "notes":
		return "노트 보기"
	case "asked":
		return "최근에 묻고 알린 것 보기"
	case "expectations":
		return "오늘 있을 일 보기"
	case "context":
		return "지금 위치 보기"
	case "nearby":
		return about("근처 찾기", first(str(args, "what"), "음식점"))
	case "jobs", "job":
		return "작업 보기"
	case "photos":
		return about("사진 찾기", clamp(first(str(args, "query"), str(args, "place"), str(args, "city")), 40))
	case "portfolio":
		return "자산 보기"
	case "allocation":
		return "자산 비중 보기"
	case "asset_history":
		return "자산 기록 보기"
	case "goals":
		return "목표 보기"
	case "logins":
		return "저장된 로그인 보기"
	case "shipping_address":
		return "배송지 보기"
	case "checklists":
		return "체크리스트 보기"
	}
	return "찾아보기"
}

// change is a row of the change board said in a line.
func change(what string, args map[string]any) string {
	switch what {
	case "", "help":
		return ""
	case "reminder.set":
		return about("리마인드 걸기", clamp(str(args, "text"), 40))
	case "reminder.change":
		return "리마인드 고치기"
	case "reminder.cancel":
		return "리마인드 취소"
	case "rule.set":
		return about("규칙 남기기", clamp(str(args, "text"), 40))
	case "rule.end":
		return "규칙 거두기"
	case "expect.deny":
		return "오늘 있을 일 접기"
	case "goal.make":
		return about("목표 만들기", str(args, "title"))
	case "goal.check":
		return "목표에 기록"
	case "goal.change":
		return "목표 고치기"
	case "checklist.make":
		return about("체크리스트 만들기", str(args, "title"))
	case "checklist.show":
		return "체크리스트 띄우기"
	case "checklist.change":
		return "체크리스트 고치기"
	case "checklist.close":
		return "체크리스트 닫기"
	}
	return "적어 두기"
}

// split is a tool's server and its own name: mcp__web__search is web and
// search; a tool of the CLI's own (WebSearch) has no server.
func split(tool string) (server, name string) {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return "", tool
	}
	if i := strings.LastIndex(rest, "__"); i >= 0 {
		return rest[:i], rest[i+2:]
	}
	return "", rest
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return strings.TrimSpace(v)
}

// about is a line with what it is about, when there is that.
func about(line, what string) string {
	if what == "" {
		return line
	}
	return line + ": " + what
}

func first(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func picture(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".heic":
		return true
	}
	return false
}

// clamp is s cut to n characters, with … where it was cut.
func clamp(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
