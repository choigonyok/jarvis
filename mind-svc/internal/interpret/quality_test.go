package interpret

import (
	"context"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

func facts(t *testing.T, c *Conversation) []model.Fact {
	t.Helper()
	fs, err := c.st.Facts(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// One relation in other words, from either end, or already in memory, is the
// fact already known.
func TestRepeatInOtherWordsIsSkipped(t *testing.T) {
	ctx := context.Background()
	c, st, _ := setup(t, `{"changes":[
		{"op":"remember","kind":"fact","set":{"subject":"지수","subjectKind":"Person","relation":"IS_IN_RELATIONSHIP_WITH","object":"윤","objectKind":"Person","text":"지수는 윤과 사귄다"},"evidence":[{"line":1,"quote":"사랑해"}],"confidence":0.9},
		{"op":"remember","kind":"fact","set":{"subject":"지수","subjectKind":"Person","relation":"LIKES","object":"마라탕","objectKind":"Thing","text":"지수는 마라탕을 좋아한다"},"evidence":[{"line":1,"quote":"마라탕"}],"confidence":0.9}
	]}`)
	_ = st.PutFact(ctx, model.Fact{ID: "mf_1", Room: "other", Subject: model.Party{Name: "나", Self: true}, Relation: "PARTNER_OF", Object: model.Party{Name: "지수", Kind: "Person"}, Text: "나는 지수와 연인 사이다", Confidence: 0.9, Status: model.FactStored})
	c.cfg.Recall = func(context.Context, string, string, []model.Message) []Memory {
		return []Memory{{ID: "e1", Fact: "지수는 마라탕을 좋아한다", Subject: "지수", Relation: "LIKES", Object: "마라탕"}}
	}
	say(t, st, "sim:1", "사랑해 마라탕 먹자", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if fs := facts(t, c); len(fs) != 1 {
		t.Fatalf("이미 아는 두 사실은 다시 쓰지 않아야 합니다: %+v", fs)
	}
}

// A marriage or a child is not read from a pet name: only the person's own
// clear words, with confidence, make one.
func TestWeightyFactsNeedTheirOwnWords(t *testing.T) {
	ctx := context.Background()
	c, st, _ := setup(t, `{"changes":[
		{"op":"remember","kind":"fact","set":{"subject":"나","relation":"IS_SPOUSE_OF","object":"지수","objectKind":"Person","text":"나와 지수는 부부 사이다"},"evidence":[{"line":1,"quote":"여보~"}],"confidence":0.9},
		{"op":"remember","kind":"fact","set":{"subject":"지수","subjectKind":"Person","relation":"FAMILY_OF","object":"아이","objectKind":"Person","text":"지수에게 아이가 있다"},"evidence":[{"line":2,"quote":"우리 애를 어떻게 데리고 가지"}],"confidence":0.6},
		{"op":"remember","kind":"fact","set":{"subject":"지수","subjectKind":"Person","relation":"MARRIED_TO","object":"민호","objectKind":"Person","text":"지수는 민호와 결혼했다"},"evidence":[{"line":3,"quote":"나 민호랑 결혼했어"}],"confidence":0.95}
	]}`)
	say(t, st, "sim:1", "여보~", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
	say(t, st, "sim:2", "우리 애를 어떻게 데리고 가지?", false, time.Date(2026, 10, 12, 20, 2, 0, 0, seoul))
	say(t, st, "sim:3", "사실 나 민호랑 결혼했어", false, time.Date(2026, 10, 12, 20, 3, 0, 0, seoul))
	if err := c.Read(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	fs := facts(t, c)
	if len(fs) != 1 || fs[0].Relation != "MARRIED_TO" || fs[0].Object.Name != "민호" {
		t.Fatalf("본인이 분명히 말한 결혼만 남아야 합니다: %+v", fs)
	}
}

// The reverse of a fact held with more confidence is not taken; with more,
// it is (memory then ends the older one).
func TestContradictionNeedsMoreConfidence(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		conf string
		keep int
	}{{"0.6", 1}, {"0.9", 2}} {
		c, st, _ := setup(t, `{"changes":[{"op":"remember","kind":"fact","set":{"subject":"지수","subjectKind":"Person","relation":"SMOKES","object":"담배","objectKind":"Thing","text":"지수는 담배를 피운다"},"evidence":[{"line":1,"quote":"담배"}],"confidence":`+tc.conf+`}]}`)
		_ = st.PutFact(ctx, model.Fact{ID: "mf_1", Room: "r", Subject: model.Party{Name: "지수", Kind: "Person"}, Relation: "IS_NON_SMOKER", Object: model.Party{Name: "담배", Kind: "Thing"}, Text: "지수는 비흡연자다", Confidence: 0.7, Status: model.FactStored})
		say(t, st, "sim:1", "나 요즘 담배 다시 펴", false, time.Date(2026, 10, 12, 20, 1, 0, 0, seoul))
		if err := c.Read(ctx, "r"); err != nil {
			t.Fatal(err)
		}
		if fs := facts(t, c); len(fs) != tc.keep {
			t.Fatalf("확신도 %s: 사실 %d개여야 합니다: %+v", tc.conf, tc.keep, fs)
		}
	}
}

func TestRelationNamesFold(t *testing.T) {
	for in, want := range map[string]string{"is spouse of": "MARRIED_TO", "IS_NON_SMOKER": "DOES_NOT_SMOKE", "dating": "PARTNER_OF", "LIKES": "LIKES"} {
		if got := relationName(in); got != want {
			t.Fatalf("%q → %q, 원하는 것 %q", in, got, want)
		}
	}
}
