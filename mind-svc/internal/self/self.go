// Package self keeps what the assistant believes about the operator, as
// notes a person can read and correct: "캠핑 며칠 전에 장작을 산다",
// "21시 이후 퇴근한 날은 운동을 거의 안 한다". A note is a hypothesis
// until the operator confirms it; a rejected note is never brought back;
// every change is a new version with its reason.
package self

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

type Self struct {
	St  store.Store
	Now func() time.Time
}

// ID is a note's id from what it is about, so the same belief found again
// is the same note.
func ID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "note_" + hex.EncodeToString(sum[:5])
}

// Hold writes a belief found from the evidence: a new hypothesis, or a new
// version of the note it already is when its numbers or words moved. A
// note the operator rejected stays rejected; one they confirmed stays
// confirmed, with fresh numbers.
func (s *Self) Hold(ctx context.Context, n model.Note, reason string) (model.Note, bool, error) {
	old, err := s.St.Note(ctx, n.ID)
	now := s.Now()
	switch {
	case errors.Is(err, model.ErrNotFound):
		n.Status, n.Version, n.Reason, n.CreatedAt, n.UpdatedAt = model.Hypothesis, 1, reason, now, now
		return n, true, s.St.PutNote(ctx, n)
	case err != nil:
		return n, false, err
	case old.Status == model.Rejected:
		return old, false, nil
	}
	if old.Text == n.Text && math.Abs(old.Confidence-n.Confidence) < 0.05 && sameRule(old.Rule, n.Rule) {
		return old, false, nil
	}
	n.Status, n.Version, n.Reason, n.CreatedAt, n.UpdatedAt = old.Status, old.Version+1, reason, old.CreatedAt, now
	if n.Rule != nil && old.Rule != nil {
		n.Rule.Except = old.Rule.Except
	}
	return n, true, s.St.PutNote(ctx, n)
}

func sameRule(a, b *model.Rule) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Support == b.Support && a.Total == b.Total && a.Bucket == b.Bucket &&
		math.Abs(a.Rate-b.Rate) < 0.02 && a.Days == b.Days
}

// Decide is the operator's word on a note: confirm, reject, or rewrite.
func (s *Self) Decide(ctx context.Context, id, status, text, reason string) (model.Note, error) {
	n, err := s.St.Note(ctx, id)
	if err != nil {
		return n, err
	}
	if status != "" {
		if status != model.Confirmed && status != model.Rejected && status != model.Hypothesis {
			return n, fmt.Errorf("모르는 상태 %q", status)
		}
		n.Status = status
	}
	if t := strings.TrimSpace(text); t != "" {
		n.Text = t
	}
	if n.Status == model.Confirmed {
		n.Confidence = math.Max(n.Confidence, 0.9)
	}
	n.Version++
	n.Reason, n.UpdatedAt = "운영자: "+strings.TrimSpace(reason), s.Now()
	if strings.TrimSpace(reason) == "" {
		n.Reason = "운영자가 " + map[string]string{model.Confirmed: "확인", model.Rejected: "기각", model.Hypothesis: "보류"}[n.Status]
	}
	return n, s.St.PutNote(ctx, n)
}

// Learn moves a note by what came of acting on it: approved, it is surer;
// turned down with a reason, the reason becomes a condition the note holds
// under ("직전 캠핑 후 남은 경우 제외").
func (s *Self) Learn(ctx context.Context, id string, approved bool, reason string) error {
	n, err := s.St.Note(ctx, id)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	n.Version++
	n.UpdatedAt = s.Now()
	if approved {
		n.Confidence = math.Min(0.95, math.Round((n.Confidence+0.1)*100)/100)
		n.Reason = "제안을 승인하심"
		return s.St.PutNote(ctx, n)
	}
	n.Confidence = math.Max(0.1, math.Round((n.Confidence-0.1)*100)/100)
	n.Reason = "제안을 거절하심"
	if cond := Condition(n, reason); cond != "" && n.Rule != nil && !slices.Contains(n.Rule.Except, cond) {
		n.Rule.Except = append(n.Rule.Except, cond)
		n.Text = strings.TrimSuffix(n.Text, ".") + " (" + cond + ")"
		n.Reason += ": " + strings.TrimSpace(reason)
	}
	return s.St.PutNote(ctx, n)
}

// Condition is a rejection's reason as the condition a note does not hold
// under. "집에 남았어" on "캠핑 전에 장작을 산다" is "직전 캠핑 후 남은 경우
// 제외"; any other reason is kept in its own words.
func Condition(n model.Note, reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		return ""
	}
	if n.Rule != nil && n.Rule.Kind == "prep" {
		for _, w := range []string{"남았", "남아", "있어", "있음", "아직 있"} {
			if strings.Contains(r, w) {
				return fmt.Sprintf("직전 %s 후 남은 경우 제외", n.Rule.PlanKind)
			}
		}
	}
	return "제외: " + r
}

// PrepText is a prep rule in a sentence: "캠핑 며칠 전에 장작을 산다".
func PrepText(kind, class, bucket string) string {
	when := map[string]string{"d1": "하루 전", "d3": "며칠 전", "d7": "일주일 전쯤", "d14": "2주 전쯤"}[bucket]
	if when == "" {
		when = "전"
	}
	return fmt.Sprintf("%s %s에 %s 산다", kind, when, Object(class))
}

// Object is a noun with its object particle: 장작을, 생수를.
func Object(noun string) string {
	r := []rune(noun)
	if len(r) == 0 {
		return noun
	}
	last := r[len(r)-1]
	if last >= 0xAC00 && last <= 0xD7A3 && (last-0xAC00)%28 != 0 {
		return noun + "을"
	}
	return noun + "를"
}

// Topic is a noun with its topic particle: 향수는, 장작은.
func Topic(noun string) string {
	r := []rune(noun)
	if len(r) == 0 {
		return noun
	}
	last := r[len(r)-1]
	if last >= 0xAC00 && last <= 0xD7A3 && (last-0xAC00)%28 != 0 {
		return noun + "은"
	}
	return noun + "는"
}

// Shrunk is a rate pulled toward the usual one the fewer times it was seen:
// (k + m·base) / (n + m).
func Shrunk(k, n int, base, m float64) float64 {
	return (float64(k) + m*base) / (float64(n) + m)
}
