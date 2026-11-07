package registry

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
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

// The kinds the code reads by name (model) have their rows here, on the lane
// whose reader takes them: a kind the code reads with no row, or with another
// lane's, never reaches its reader.
func TestTheCodesKindsHaveTheirLanes(t *testing.T) {
	s := Default()
	for lane, kinds := range map[Lane][]string{
		Structured: {model.KindCalendarNear, model.KindCalendarYear, model.KindContextVisits, model.KindAssetsHoldings,
			model.KindContextNow, model.KindContextNowGuest, model.KindContextRegions, model.KindContextTravel,
			model.KindWeatherForecast, model.KindPhotosRecent, model.KindPhotosDays, model.KindAssetsFX,
			model.KindAssetsOutflows, model.KindWorkoutChanges, model.KindChatTurn},
		Deterministic: {model.KindIntentAnswer, model.KindExecuteResult, model.KindManualEdit, model.KindPaymentRequested,
			model.KindTimerFired, model.KindRegistryChanged, model.KindContextState},
		Interpret: {model.KindMessageObserved, model.KindOccurrenceObserved, model.KindActionExecuted, model.KindActionUndone,
			model.KindAssetsBand, model.KindLocationEnter, model.KindLocationLeave, model.KindLocationGuestEnter,
			model.KindLocationGuestLeave, model.KindLocationFix},
	} {
		for _, k := range kinds {
			if !s.Known(k) || s.LaneOf(k) != lane {
				t.Errorf("%s: %s 레인이어야 합니다 (지금 %s)", k, lane, s.LaneOf(k))
			}
		}
	}
	for _, k := range locked {
		if s.LaneOf(k) != Deterministic {
			t.Errorf("%s: 고정된 종류는 결정 레인입니다", k)
		}
	}
}

// The intents mind raises by name (model) have their action rows: the gate
// does nothing it has no row for. A notice does nothing and needs none.
func TestTheCodesIntentsHaveTheirRows(t *testing.T) {
	s := Default()
	for _, k := range []string{model.CalendarCreate, model.CalendarUpdate, model.CalendarDelete, model.CartAdd,
		model.RemindSet, model.MessageDraft, model.SettleRequest, model.ReviewPlace, model.PreferenceConfirm,
		model.AssetsRebalance, model.PhotosAlbum, model.PhotosPlace, model.SpendingCategory, model.NoteOnly} {
		if _, ok := s.Action(k); !ok {
			t.Errorf("%s: 행동 줄이 없습니다", k)
		}
	}
	if _, ok := s.Action(model.Info); ok {
		t.Error("알림은 행동 줄이 없습니다")
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

// A title's kind is the first in order whose word it holds, and a short word -
// Latin of three letters or fewer, one Hangul letter - only as a word of its own.
func TestKindOfATitle(t *testing.T) {
	r := Default()
	for title, want := range map[string]string{
		"가평 캠핑": "캠핑", "호캉스": "여행", "💕": "데이트", "팀 회의": "업무", "재택근무": "업무", "본사 출근": "업무",
		"발표 연습": "업무", "치과": "병원", "무릎 수술": "병원", "미용실 가용": "미용", "헤어 커트": "미용",
		"PT 받기": "운동", "서연이랑 야구 보기": "공연", "민수와 저녁": "식사", "술 약속": "식사", "술자리": "식사",
		"밴드 합주": "취미", "합주 연습": "취미", "베이스 레슨": "취미", "밴드 뒷풀이": "모임", "장보기": "쇼핑",
		"ChatGPT 구독 해지": OtherKind, "미술관 관람": OtherKind, "기술 스터디": OtherKind, "예술의전당": OtherKind, "뭔가": OtherKind,
	} {
		if got := r.KindOf(title); got != want {
			t.Errorf("%s: %s, %s 여야 합니다", title, got, want)
		}
	}
	if k := r.PlanKind("식사"); !k.Meal || !slices.Contains(k.Fits, "식비") || r.PlanKind("취미").Meal {
		t.Fatalf("%+v", k)
	}
	if r.PlanKind("캠핑").Stay != "캠핑장" || !r.PlanKind("여행").Outdoors || !r.PlanKind("업무").Work || r.HasPlanKind("없는 종류") {
		t.Fatal("종류마다의 성질")
	}
	kinds := r.PlanKinds()
	if kinds[len(kinds)-1].Name != OtherKind {
		t.Fatalf("기타는 맨 끝: %+v", kinds[len(kinds)-1])
	}
}

// A kind is a row: written, it is in the table the next read sees, in its
// place; a name with a separator in it would break the interpreter's list.
func TestAKindIsARow(t *testing.T) {
	rows := append(Seed(), Row{Section: SectionPlan, Key: "봉사", Body: []byte(`{"order": 14, "words": ["봉사"]}`), By: ByOperator})
	r, err := Parse(rows)
	if err != nil {
		t.Fatal(err)
	}
	if r.KindOf("주말 봉사") != "봉사" || !r.HasPlanKind("봉사") {
		t.Fatal("새 종류")
	}
	if err := Check(SectionPlan, "봉사", []byte(`{"order": 14}`)); err != nil {
		t.Fatal(err)
	}
	if err := Check(SectionPlan, "봉사·취미", []byte(`{}`)); err == nil {
		t.Fatal("구분 글자가 든 이름은 안 됩니다")
	}
	SetCurrent(r)
	defer SetCurrent(Default())
	if Current().KindOf("주말 봉사") != "봉사" {
		t.Fatal("지금 표")
	}
}
