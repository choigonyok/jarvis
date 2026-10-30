package registry

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestTheSeedReads(t *testing.T) {
	s := Default()
	if !slices.Contains(s.Kinds(Deterministic, false), "timer.fired") || !slices.Contains(s.Kinds(Structured, false), "message.received") {
		t.Fatalf("%v", s.Kinds(Deterministic, false))
	}
	if g := s.Gate(); g.DailyCards != 5 || g.MinConfidence != 0.7 {
		t.Fatalf("%+v", g)
	}
	if !s.Single("LIVES_IN") || s.Single("LIKES") {
		t.Fatal("한 답만 있는 관계")
	}
	if a, ok := s.Action("cart.add"); !ok || !a.Fixed || a.Level != "ask" {
		t.Fatalf("%+v", a)
	}
	if _, ok := s.Action("없는.행동"); ok {
		t.Fatal("등록하지 않은 행동입니다")
	}
	for _, r := range Seed() {
		if err := Check(r.Section, r.Key, r.Body); err != nil {
			t.Errorf("%s/%s: %v", r.Section, r.Key, err)
		}
	}
}

// A kind's own row comes first, then the nearest prefix. What has no row goes
// to interpretation: the way in is wide.
func TestAKindFindsItsLane(t *testing.T) {
	s := Default()
	for kind, want := range map[string]Lane{
		"assets.band":     Interpret,
		"assets.holdings": Structured,
		"assets.daily":    Relay,
		"reminder.set":    Deterministic,
		"처음.보는.종류":        Interpret,
	} {
		if got := s.LaneOf(kind); got != want {
			t.Errorf("%s: %s", kind, got)
		}
	}
	if s.Known("처음.보는.종류") || !s.Known("job.step") {
		t.Fatal("등록 여부")
	}
}

// What only the guest's stream carries is read on theirs, and a kind with a
// reader of its own is not in its lane's line.
func TestWhatEachReaderSubscribesTo(t *testing.T) {
	s := Default()
	guest := s.Kinds(Deterministic, true)
	if !slices.Contains(guest, "reminder.*") || !slices.Contains(guest, "jobs.write") || slices.Contains(guest, "intent.answer") {
		t.Fatalf("%v", guest)
	}
	if slices.Contains(s.Kinds(Deterministic, false), "execute.result") {
		t.Fatal("실행 결과는 자기 소비자가 읽습니다")
	}
}

func TestARowIsCheckedBeforeItIsWritten(t *testing.T) {
	for name, c := range map[string]struct {
		section, key, body string
		ok                 bool
	}{
		"새 종류":       {SectionKind, "new.kind", `{"lane":"structured"}`, true},
		"없는 레인":      {SectionKind, "new.kind", `{"lane":"fast"}`, false},
		"고정된 종류":     {SectionKind, "timer.fired", `{"lane":"interpret"}`, false},
		"고정된 종류 그대로": {SectionKind, "timer.fired", `{"lane":"deterministic"}`, true},
		"행동":         {SectionAction, "new.action", `{"level":"ask"}`, true},
		"없는 수준":      {SectionAction, "new.action", `{"level":"always"}`, false},
		"슬롯":         {SectionSlot, "s", `{"kind":"k"}`, false},
		"관계":         {SectionFact, "OWNS", `{"single":false}`, true},
		"정책":         {SectionPolicy, "gate", `{"dailyCards":3}`, true},
		"없는 정책":      {SectionPolicy, "other", `{}`, false},
		"없는 구역":      {"other", "k", `{}`, false},
	} {
		err := Check(c.section, c.key, json.RawMessage(c.body))
		if (err == nil) != c.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLiveIsReplacedWhole(t *testing.T) {
	l := NewLive(Default())
	rows := append(Seed(), Row{Section: SectionFact, Key: "OWNS", Body: json.RawMessage(`{"single":true}`)})
	next, err := Parse(rows)
	if err != nil {
		t.Fatal(err)
	}
	before := l.Now()
	l.Set(next)
	if before.Single("OWNS") || !l.Now().Single("OWNS") {
		t.Fatal("읽는 쪽이 보던 표는 그대로입니다")
	}
}

// What ships says who should be heard from, and a row that makes no sense is
// refused: a warning later than the fault, a row with nothing to look at.
func TestSourcesShipAndAreChecked(t *testing.T) {
	var found int
	for _, r := range Seed() {
		if r.Section != SectionSource {
			continue
		}
		found++
		if err := Check(r.Section, r.Key, r.Body); err != nil {
			t.Errorf("%s: %v", r.Key, err)
		}
	}
	if found == 0 {
		t.Fatal("씨앗에 소스가 없습니다")
	}
	for _, body := range []string{
		`{"kind":"a","from":"b","name":"c","warn":"2h","fail":"1h"}`,
		`{"from":"b","name":"c","warn":"1h","fail":"2h"}`,
		`{"kind":"a","from":"b","name":"c","warn":"나중","fail":"2h"}`,
	} {
		if Check(SectionSource, "x", json.RawMessage(body)) == nil {
			t.Errorf("거절해야 합니다: %s", body)
		}
	}
	if err := Check(SectionSource, "x", json.RawMessage(`{"kind":"a","from":"b","name":"c","warn":"1h","fail":"2h"}`)); err != nil {
		t.Fatal(err)
	}
}
