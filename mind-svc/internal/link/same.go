package link

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// SameSystem is what the rules leave open of two plans: something in common,
// and no telling from the words alone (model.Compare).
const SameSystem = `일정 두 개가 주어진다. 달력에 한 칸으로 적을 같은 약속인지 판단하라.
같은 약속: 뭉뚱그린 말과 구체적인 말이 같은 날 같은 사람을 만나는 하나의 약속을 가리킬 때(시각 없는 "지수 만남"과 저녁 7시 "지수와 저녁"), 시각을 다르게 들은 같은 약속, 한 모임의 이어지는 순서(합주와 뒤풀이), 여행과 그 여행 안의 일정.
다른 일: 종일 표시(생일·휴가·재택)와 그날의 약속, 같은 사람을 점심과 저녁에 따로 만나는 일, 같은 이름이라도 몇 시간 떨어져 따로 하는 일, 준비하는 일(예약 전화, 축의금 보내기)과 그 일.
둘 중 어느 쪽인지 모르겠으면 다른 일이다.
JSON 하나만: {"same":false}`

const (
	sameTimeout = 20 * time.Second
	sameKept    = 1024 // answers kept; past it they are asked again
)

// SameJudge settles it, once per pair of tellings: a plan is compared to every
// plan near it each time it changes, and the answer does not.
type SameJudge struct {
	LLM llm.Client
	Log *slog.Logger

	mu   sync.Mutex
	seen map[[sha256.Size]byte]bool
}

// Of is whether two plans are one event. No answer is not: they stay two, and
// the operator is asked about each as before.
func (j *SameJudge) Of(ctx context.Context, a, b model.State) bool {
	if j.LLM == nil || !j.LLM.Ready() {
		return false
	}
	one, other := telling(a), telling(b)
	if other < one {
		one, other = other, one
	}
	key := sha256.Sum256([]byte(one + "\n" + other))
	j.mu.Lock()
	said, ok := j.seen[key]
	j.mu.Unlock()
	if ok {
		return said
	}
	ctx, cancel := context.WithTimeout(ctx, sameTimeout)
	defer cancel()
	answer, err := j.LLM.Complete(llm.WithPurpose(ctx, llm.Judge), SameSystem, one+"\n"+other)
	if err == nil {
		said, err = parseSame(answer)
	}
	if err != nil {
		j.Log.Warn("같은 일정인지 판단하지 못했습니다", "err", err)
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.seen == nil || len(j.seen) >= sameKept {
		j.seen = map[[sha256.Size]byte]bool{}
	}
	j.seen[key] = said
	return said
}

// telling is a plan as the model reads it: what the rules read too.
func telling(s model.State) string {
	people := slices.Clone(s.People)
	slices.Sort(people)
	return fmt.Sprintf("일정: %q 날짜 %s~%s 시각 %s~%s 장소 %q 함께 %s", s.Title, s.Date, s.EndDate, s.Start, s.End, s.Place, strings.Join(people, ", "))
}

func parseSame(answer string) (bool, error) {
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return false, fmt.Errorf("판정을 읽지 못했습니다: %q", answer)
	}
	var j struct {
		Same bool `json:"same"`
	}
	err := json.Unmarshal([]byte(answer[start:end+1]), &j)
	return j.Same, err
}
