package permission

// Generic rendering for the tools with no module card behind them: Claude
// Code's own, the browser's, and a module's tool the CLI runs itself (a job
// made or instructed). A module's action is rendered by the module itself,
// which is the only place that knows what the change actually means.
//
// What a person reads is never the JSON the model sent (2026-10-20: a job's
// card showed its arguments in braces): a title in Korean, and the arguments
// as "항목: 값" lines in an order that leads with what the call is about.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// mcpName splits mcp__<server>__<tool>; ok is false for Claude Code's own tools.
func mcpName(tool string) (server, name string, ok bool) {
	rest, found := strings.CutPrefix(tool, "mcp__")
	if !found {
		return "", "", false
	}
	server, name, ok = strings.Cut(rest, "__")
	return server, name, ok && server != "" && name != ""
}

// toolTitles are the card titles of tools the CLI runs itself, by the tool's
// own name.
var toolTitles = map[string]string{
	"list_jobs":                 "작업 목록을 봅니다",
	"get_job":                   "작업을 자세히 봅니다",
	"get_shipping_address":      "저장된 배송지를 읽습니다",
	"browser_navigate":          "브라우저로 주소를 엽니다",
	"browser_click":             "브라우저에서 누릅니다",
	"browser_type":              "브라우저에 입력합니다",
	"browser_get_state":         "브라우저 화면을 읽습니다",
	"browser_get_html":          "페이지 내용을 읽습니다",
	"browser_extract_content":   "페이지에서 내용을 뽑습니다",
	"browser_screenshot":        "브라우저 화면을 찍습니다",
	"browser_scroll":            "페이지를 스크롤합니다",
	"browser_go_back":           "이전 페이지로 갑니다",
	"browser_list_tabs":         "열린 탭을 봅니다",
	"browser_switch_tab":        "다른 탭으로 옮깁니다",
	"browser_close_tab":         "탭을 닫습니다",
	"browser_upload_files":      "사진을 올립니다",
	"browser_fill_login":        "저장된 로그인으로 채웁니다",
	"browser_enter_payment_pin": "결제 비밀번호를 넣습니다",
}

// serverNames say whose tool an unknown one is.
var serverNames = map[string]string{
	"browser": "브라우저", "jobs": "작업", "calendar": "캘린더", "memory": "기억", "mind": "판단",
	"vault": "로그인 보관함", "place": "위치", "web": "웹 찾기", "messages": "메시지", "spending": "가계부", "assets": "자산",
}

// title turns a tool call into the one line the operator reads. Claude Code
// and the MCP servers name their tools in English, for the model; the card
// speaks to a person.
func title(tool string, fields map[string]any) string {
	switch tool {
	case "Bash":
		return "명령을 실행합니다"
	case "Write":
		return "파일을 새로 씁니다"
	case "Edit":
		return "파일을 고칩니다"
	case "WebFetch":
		return "외부 주소를 가져옵니다"
	case "WebSearch":
		return "웹을 검색합니다"
	case "":
		return "도구 실행을 요청합니다"
	}
	server, name, ok := mcpName(tool)
	if !ok {
		return tool + " 실행을 요청합니다"
	}
	switch name {
	case "create_job":
		if t := pickString(fields, "title"); t != "" {
			return "작업을 맡깁니다: " + t
		}
		return "작업을 맡깁니다"
	case "instruct_job":
		if id := scalar(fields["id"]); id != "" {
			return "작업 " + id + "에 지시를 더합니다"
		}
		return "작업에 지시를 더합니다"
	}
	if t, ok := toolTitles[name]; ok {
		return t
	}
	who := serverNames[server]
	if who == "" {
		who = server
	}
	return fmt.Sprintf("%s 도구를 씁니다: %s", who, strings.ReplaceAll(name, "_", " "))
}

func consequence(tool string) string {
	switch tool {
	case "Bash":
		return "작업 공간에서 실행됩니다 · 되돌릴 수 없을 수 있습니다"
	case "Write":
		return "기존 내용이 덮어쓰기 됩니다"
	case "Edit":
		return "파일 내용이 바뀝니다"
	case "WebFetch", "WebSearch":
		return "외부로 요청이 나갑니다"
	}
	switch _, name, _ := mcpName(tool); {
	case name == "create_job":
		return "승인하면 작업이 백그라운드에서 시작됩니다"
	case name == "instruct_job":
		return "승인하면 작업이 이 지시를 반영합니다"
	case strings.HasPrefix(name, "browser_"):
		return "승인하면 브라우저에서 바로 실행합니다"
	}
	return "승인하면 바로 실행합니다"
}

// body renders what the operator is actually judging. Approving a write
// while seeing only its path is approving blind, so file tools show the
// content that would land, not just where.
func body(tool string, input any) string {
	if input == nil {
		return "(입력 없음)"
	}
	fields, ok := input.(map[string]any)
	if !ok {
		return fallback(value(input), "(입력 없음)")
	}

	path := pickString(fields, "file_path", "path")
	switch tool {
	case "Bash":
		return pickString(fields, "command")
	case "Write":
		return joinNonEmpty(path, clampBody(pickString(fields, "content")))
	case "Edit":
		return joinNonEmpty(path,
			prefixLines("- ", clampBody(pickString(fields, "old_string"))),
			prefixLines("+ ", clampBody(pickString(fields, "new_string"))))
	}
	_, name, _ := mcpName(tool)
	switch name {
	case "create_job":
		// The title is the card's; the rest is what the job is told.
		return fallback(lines(fields, "title"), "(입력 없음)")
	case "instruct_job":
		return fallback(clampBody(pickString(fields, "text")), lines(fields, "id"))
	}
	// One thing to judge by - a command, an address, a search - is the body
	// by itself; anything else is every argument, one line each.
	if len(fields) == 1 {
		for _, key := range []string{"command", "file_path", "path", "url", "query", "pattern"} {
			if v := pickString(fields, key); v != "" {
				return v
			}
		}
	}
	return fallback(lines(fields, "description"), "(입력 없음)")
}

// labels name the arguments a card shows.
var labels = map[string]string{
	"title": "제목", "name": "이름", "goal": "목표", "text": "내용", "instructions": "지시", "question": "질문",
	"sites": "쓰는 사이트", "photos": "사진", "url": "주소", "query": "검색어", "pattern": "찾을 말",
	"element": "대상", "index": "번호", "value": "값", "values": "값", "date": "날짜", "start": "시작",
	"end": "끝", "place": "장소", "reason": "이유", "command": "명령", "content": "내용", "file_path": "파일",
	"path": "경로", "id": "번호", "site": "사이트", "keys": "키", "amount": "금액", "schedule": "주기",
	"afterMinutes": "몇 분 뒤", "summary": "요약", "note": "메모", "tab_id": "탭", "direction": "방향",
	"new_tab": "새 탭", "extract_links": "링크도 뽑기", "file_paths": "파일", "paths": "파일",
}

// order is which arguments lead: what the call is about before its details.
var order = []string{"title", "name", "goal", "question", "text", "instructions", "url", "query", "element", "value",
	"date", "start", "end", "place", "sites", "photos", "reason"}

// lines is the arguments as "항목: 값", one each, leading ones first and the
// rest by name; skip names what the card shows elsewhere. An element's ref
// ("e12") is the browser's handle, not something a person reads by.
func lines(fields map[string]any, skip ...string) string {
	skip = append(skip, "ref")
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if !contains(skip, k) {
			keys = append(keys, k)
		}
	}
	rank := func(k string) int {
		for i, o := range order {
			if o == k {
				return i
			}
		}
		return len(order)
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	var out []string
	for _, k := range keys {
		v := value(fields[k])
		if v == "" {
			continue
		}
		label := labels[k]
		if label == "" {
			label = k
		}
		if strings.Contains(v, "\n") {
			out = append(out, label+":\n"+indent(v, "  "))
		} else {
			out = append(out, label+": "+v)
		}
	}
	return strings.Join(out, "\n")
}

// value is one argument as words: text as it is (kept short), a number as a
// number, yes and no in Korean, a list of words on one line, anything nested
// as lines of its own - never braces.
func value(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return clampBody(strings.TrimSpace(x))
	case bool:
		if x {
			return "예"
		}
		return "아니요"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case []any:
		if len(x) == 0 {
			return ""
		}
		flat := true
		parts := make([]string, 0, len(x))
		for _, e := range x {
			if _, nested := e.(map[string]any); nested {
				flat = false
			}
			if s := value(e); s != "" {
				parts = append(parts, s)
			}
		}
		if flat && !strings.Contains(strings.Join(parts, ""), "\n") {
			return strings.Join(parts, ", ")
		}
		for i, p := range parts {
			parts[i] = "- " + strings.ReplaceAll(p, "\n", "\n  ")
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		return lines(x)
	}
	return fmt.Sprint(v)
}

// scalar is a number or a word as text, "" for anything else.
func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64, bool:
		return value(x)
	}
	return ""
}

func indent(s, pad string) string {
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = pad + l
	}
	return strings.Join(ls, "\n")
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// clampBody keeps a card readable: enough to judge by, never a whole file.
func clampBody(s string) string {
	const maxLines, maxRunes = 14, 800
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	if r := []rune(s); len(r) > maxRunes {
		s = string(r[:maxRunes]) + "\n…"
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], fmt.Sprintf("…(%d줄 더)", len(lines)-maxLines))
	}
	return strings.Join(lines, "\n")
}

func prefixLines(prefix, s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return "(입력 없음)"
	}
	return strings.Join(kept, "\n\n")
}

func pickString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func pickAny(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}
