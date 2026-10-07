// Package muscle says which part a movement trains.
//
// This table used to live in the browser (web/src/lib/workout.ts). It is here
// too, and on purpose: the group is written into the row when the set is
// logged rather than derived when it is read. If it were derived, editing this
// table would silently rewrite what last March's leg volume was.
//
// Anything not listed is 기타 rather than guessed at.
package muscle

import "strings"

const Other = "기타"

var groups = map[string]string{
	"벤치프레스":         "가슴",
	"인클라인 덤벨프레스":    "가슴",
	"딥스":            "가슴",
	"스쿼트":           "하체",
	"레그프레스":         "하체",
	"루마니안 데드리프트":    "하체",
	"데드리프트":         "등",
	"바벨로우":          "등",
	"랫풀다운":          "등",
	"풀업":            "등",
	"오버헤드프레스":       "어깨",
	"사이드 레터럴 레이즈":   "어깨",
	"덤벨컬":           "팔",
	"케이블 푸시다운":      "팔",
	"플랭크":           "코어",
	"행잉 레그레이즈":      "코어",
}

func Of(name string) string {
	if g, ok := groups[strings.TrimSpace(name)]; ok {
		return g
	}
	return Other
}
