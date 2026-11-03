package store

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// Every next_reason the statements here write is said to be the operator's
// asking or not: the agent reads Asked, so a word added without a side here
// would quietly change whose wait it is.
func TestEveryReasonHasASide(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	notAsked := []string{"카드 결정", "먼저 하기로 함", "먼저 한 작업 다음에 이어서", "실행 중에 온 지시·답"}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`next_reason = '([^']+)'|now\(\), '([^']+)', \$6`).FindAllStringSubmatch(string(src), -1) {
		w := m[1] + m[2]
		seen[w] = true
		if !askedReasons[w] && !slices.Contains(notAsked, w) {
			t.Errorf("%q 는 운영자가 시킨 것인지 정하지 않았습니다", w)
		}
	}
	for w := range askedReasons {
		if !seen[w] {
			t.Errorf("%q 는 쓰는 곳이 없습니다", w)
		}
	}
	if len(seen) < 8 {
		t.Fatalf("문구를 다 찾지 못했습니다: %v", seen)
	}
}
