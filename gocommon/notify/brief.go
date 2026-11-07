package notify

import (
	"regexp"
	"strings"
)

// Brief is words as a notification says them: one line however many they came
// in (" · " between them), without the marks of a document - a list's "-" or
// "1.", a heading's "#", **bold**, `code`, a link's brackets - without the
// name the writer opened with ("고뇩님,"), at most n characters: a job's report
// and a card's body are written for the conversation, not for a phone's notice.
func Brief(s string, n int) string {
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		line = listMark.ReplaceAllString(strings.TrimSpace(line), "")
		line = heading.ReplaceAllString(line, "")
		for _, m := range marks {
			line = m.ReplaceAllString(line, "$1")
		}
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	out := greeting.ReplaceAllString(strings.Join(parts, " · "), "")
	out = strings.Join(strings.Fields(out), " ")
	if r := []rune(out); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	return out
}

var (
	listMark = regexp.MustCompile(`^(?:[-*•]\s+|\d{1,2}[.)]\s+)`)
	heading  = regexp.MustCompile(`^#{1,6}\s+`)
	marks    = []*regexp.Regexp{
		regexp.MustCompile(`\*\*(.+?)\*\*`),
		regexp.MustCompile(`__(.+?)__`),
		regexp.MustCompile("`([^`]+)`"),
		regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`),
	}
	greeting = regexp.MustCompile(`^[^\s,]{1,12}님,\s*`)
)
