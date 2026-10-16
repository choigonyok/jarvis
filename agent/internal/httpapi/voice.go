package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// The phone's voice shortcut: the operator speaks, the shortcut posts what
// was heard, and waits for what jarvis says, to read it aloud. One request,
// one answer - the same conversation as the console's, so what was asked by
// voice is there to read later and the next question can follow on from it.

// voiceWait is how long a spoken question waits for its answer: a phone
// holding a request open longer than this is a phone in a pocket.
var voiceWait = 90 * time.Second

// guardVoice lets through the voice shortcut's token (or the console's).
func (s *Server) guardVoice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		voice := ok && s.voiceToken != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(s.voiceToken)) == 1
		console := ok && s.apiToken != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(s.apiToken)) == 1
		if !voice && !console {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

// postVoice is POST /voice {text} → {answer}: the turn's own words, as plain
// sentences to be read aloud.
func (s *Server) postVoice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "요청을 읽지 못했어요."})
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "말씀하신 내용이 비어 있어요."})
		return
	}
	events, unsubscribe := s.bus.Subscribe()
	defer unsubscribe()
	if err := s.runner.Send(text, nil); err != nil {
		if errors.Is(err, claudecode.ErrBusy) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "지금 다른 일을 하는 중이에요. 잠시 뒤에 다시 물어봐 주세요."})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "요청을 시작하지 못했어요."})
		return
	}
	var said []string
	card := false
	started := false
	timeout := time.NewTimer(voiceWait)
	defer timeout.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-timeout.C:
			answer := Spoken(said, card)
			if answer == "" {
				answer = "아직 생각하는 중이에요. 답은 jarvis 앱에서 이어서 볼 수 있어요."
			}
			writeJSON(w, http.StatusOK, map[string]any{"answer": answer, "done": false})
			return
		case ev := <-events:
			switch ev.Type {
			case "turn":
				t, ok := ev.Turn.(thread.Turn)
				if !ok || t.Role != thread.RoleAgent {
					continue
				}
				said = append(said, t.Paragraphs...)
				card = card || t.ProposalID != ""
			case "status":
				if ev.Thinking {
					started = true
				} else if started {
					writeJSON(w, http.StatusOK, map[string]any{"answer": Spoken(said, card), "done": true})
					return
				}
			case "error":
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": ev.Message})
				return
			}
		}
	}
}

var (
	mdLink   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdMarks  = regexp.MustCompile("[*_`#>]+")
	mdBullet = regexp.MustCompile(`(?m)^\s*(?:[-•]|\d+\.)\s+`)
	spaces   = regexp.MustCompile(`[ \t]+`)
)

// Spoken is what was said, as sentences for a voice: no markdown, no list
// marks, links by their words; a card raised says so, since a card is read
// on a screen.
func Spoken(paragraphs []string, card bool) string {
	text := strings.Join(paragraphs, "\n")
	text = mdLink.ReplaceAllString(text, "$1")
	text = mdBullet.ReplaceAllString(text, "")
	text = mdMarks.ReplaceAllString(text, "")
	text = spaces.ReplaceAllString(text, " ")
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "|") {
			lines = append(lines, l)
		}
	}
	out := strings.Join(lines, " ")
	if card {
		out = strings.TrimSpace(out + " 확인할 카드가 있어요. jarvis 앱에서 봐 주세요.")
	}
	if out == "" {
		out = "답할 말이 없었어요."
	}
	return out
}
