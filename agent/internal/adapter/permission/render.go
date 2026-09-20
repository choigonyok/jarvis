package permission

// Generic rendering for Claude Code's own tools - the ones with no module
// behind them. A module's action is rendered by the module itself, which is
// the only place that knows what the change actually means.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// title turns a tool name into the one line the operator reads. Claude Code
// names its own tools, so the mapping lives here rather than in the model.
func title(tool string) string {
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
	default:
		return tool + " 실행을 요청합니다"
	}
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
	default:
		return "승인하면 Claude Code가 바로 실행합니다"
	}
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
		return pretty(input)
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

	for _, key := range []string{"command", "file_path", "path", "url", "query", "pattern"} {
		if v := pickString(fields, key); v != "" {
			return v
		}
	}
	return pretty(fields)
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

func pretty(v any) string {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(body)
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
