package sleep

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

var seoul = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		panic(err)
	}
	return loc
}()

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// One night as iOS's sleep analysis keeps it: the iPhone's in bed under the
// watch's stages. 23:30 in bed, asleep 23:42, awake at 02:40 for ten
// minutes, out of bed 07:10 after the last stage ends at 07:05.
var watchNight = [][3]string{
	{"2026-11-15T23:30:00+09:00", "2026-11-16T07:10:00+09:00", "In Bed"},
	{"2026-11-15T23:42:00+09:00", "2026-11-16T00:40:00+09:00", "Core"},
	{"2026-11-16T00:40:00+09:00", "2026-11-16T01:30:00+09:00", "Deep"},
	{"2026-11-16T01:30:00+09:00", "2026-11-16T02:10:00+09:00", "Core"},
	{"2026-11-16T02:10:00+09:00", "2026-11-16T02:40:00+09:00", "REM"},
	{"2026-11-16T02:40:00+09:00", "2026-11-16T02:50:00+09:00", "Awake"},
	{"2026-11-16T02:50:00+09:00", "2026-11-16T04:00:00+09:00", "Core"},
	{"2026-11-16T04:00:00+09:00", "2026-11-16T04:30:00+09:00", "Deep"},
	{"2026-11-16T04:30:00+09:00", "2026-11-16T05:20:00+09:00", "REM"},
	{"2026-11-16T05:20:00+09:00", "2026-11-16T06:40:00+09:00", "Core"},
	{"2026-11-16T06:40:00+09:00", "2026-11-16T07:05:00+09:00", "REM"},
}

// samplesBody is a shortcut's send: {"source":"shortcut","samples":[...]},
// with each value and time put through name and format.
func samplesBody(rows [][3]string, name func(string) string, format func(time.Time) string) string {
	var items []string
	for _, r := range rows {
		s, _ := time.Parse(time.RFC3339, r[0])
		e, _ := time.Parse(time.RFC3339, r[1])
		items = append(items, fmt.Sprintf(`{"start":%q,"end":%q,"value":%q,"source":"홍길동의 Apple Watch"}`, format(s), format(e), name(r[2])))
	}
	return `{"source":"shortcut","samples":[` + strings.Join(items, ",") + `]}`
}

func iso(t time.Time) string { return t.In(seoul).Format(time.RFC3339) }

func one(t *testing.T, body string) Night {
	t.Helper()
	in, err := Parse([]byte(body), seoul)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Unknown) > 0 || in.Skipped > 0 {
		t.Fatalf("읽지 못한 것: %v, 건너뜀 %d", in.Unknown, in.Skipped)
	}
	nights := append(Sleeps(in.Samples, seoul), in.Nights...)
	if len(nights) != 1 {
		t.Fatalf("밤 하나여야 함: %+v", nights)
	}
	return nights[0]
}

func val(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}

func check(t *testing.T, n Night, want string) {
	t.Helper()
	got := fmt.Sprintf("%s %s→%s asleep=%d inBed=%d awake=%d deep=%s core=%s rem=%s bedOnly=%v",
		n.Night, n.BedAt.In(seoul).Format("01-02 15:04"), n.WakeAt.In(seoul).Format("01-02 15:04"),
		n.AsleepMinutes, n.InBedMinutes, n.AwakeMinutes, val(n.DeepMinutes), val(n.CoreMinutes), val(n.REMMinutes), n.BedOnly)
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

const wantWatchNight = "2026-11-16 11-15 23:30→11-16 07:10 asleep=433 inBed=460 awake=27 deep=80 core=248 rem=105 bedOnly=false"

func TestShortcutSamples(t *testing.T) {
	n := one(t, samplesBody(watchNight, func(s string) string { return s }, iso))
	check(t, n, wantWatchNight)
	// The stretches, in minutes from 23:30: the bed before sleep, the stages,
	// the waking at 02:40, the bed after the last stage.
	want := []Segment{
		{0, 12, InBed}, {12, 70, Core}, {70, 120, Deep}, {120, 160, Core}, {160, 190, REM}, {190, 200, Awake},
		{200, 270, Core}, {270, 300, Deep}, {300, 350, REM}, {350, 430, Core}, {430, 455, REM}, {455, 460, InBed},
	}
	if !slices.Equal(n.Timeline, want) {
		t.Fatalf("타임라인:\n got %v\nwant %v", n.Timeline, want)
	}
	if n.Source != "홍길동의 Apple Watch" {
		t.Fatalf("출처: %q", n.Source)
	}
}

// The same night from a Korean iPhone: the Health app's Korean names, and the
// dates as a shortcut prints them when not told to use ISO 8601.
func TestKoreanShortcut(t *testing.T) {
	ko := map[string]string{"In Bed": "침대에 있음", "Core": "코어 수면", "Deep": "깊은 수면", "REM": "렘 수면", "Awake": "깨어 있음"}
	printed := func(t time.Time) string {
		t = t.In(seoul)
		half, h := "오전", t.Hour()
		if h >= 12 {
			half = "오후"
		}
		if h%12 == 0 {
			h = 12
		} else {
			h %= 12
		}
		return fmt.Sprintf("%d. %d. %d. %s %d:%02d", t.Year(), t.Month(), t.Day(), half, h, t.Minute())
	}
	n := one(t, samplesBody(watchNight, func(s string) string { return ko[s] }, printed))
	check(t, n, wantWatchNight)
}

func TestStageNames(t *testing.T) {
	for in, want := range map[any]Stage{
		"In Bed": InBed, "inBed": InBed, "INBED": InBed, "침대에 있음": InBed, "침대": InBed,
		"Asleep": Asleep, "asleepUnspecified": Asleep, "Asleep (Unspecified)": Asleep, "Unspecified": Asleep, "수면": Asleep, "수면 중": Asleep, "잠": Asleep,
		"Awake": Awake, "깨어 있음": Awake, "깸": Awake,
		"Core": Core, "Asleep Core": Core, "asleepCore": Core, "코어": Core, "코어 수면": Core, "Light": Core, "얕은 수면": Core,
		"Deep": Deep, "AsleepDeep": Deep, "깊은 수면": Deep,
		"REM": REM, "asleepREM": REM, "렘 수면": REM, "렘": REM, "REM 수면": REM,
		"HKCategoryValueSleepAnalysisAsleepREM": REM, "HKCategoryValueSleepAnalysisInBed": InBed,
		json.Number("0"): InBed, json.Number("1"): Asleep, json.Number("2"): Awake, json.Number("3"): Core, json.Number("4"): Deep, json.Number("5"): REM,
		"4": Deep,
	} {
		if got, ok := StageOf(in); !ok || got != want {
			t.Errorf("%v: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []any{"수면 분석", "Sleep Analysis", "", "코골이", "Snoring", "7", json.Number("9"), nil, true} {
		if got, ok := StageOf(in); ok {
			t.Errorf("%v 는 단계가 아님: %q", in, got)
		}
	}
}

func TestTimes(t *testing.T) {
	want := at(t, "2026-11-16T07:10:00+09:00")
	for _, s := range []any{
		"2026-11-16T07:10:00+09:00", "2026-11-16T07:10:00.000+09:00", "2026-11-15T22:10:00Z", "2026-11-16T07:10:00+0900",
		"2026-11-16 07:10:00 +0900", "2026-11-16 07:10:00 +09:00", "2026-11-15 22:10:00 +0000",
		"2026-11-16T07:10:00", "2026-11-16 07:10", "2026/11/16 07:10:00",
		"2026. 11. 16. 오전 7:10", "2026. 11. 16. 07:10", "2026. 11. 16. 오전 7:10:00", "2026년 11월 16일 오전 7:10", "2026년 11월 16일 월요일 오전 7:10:00",
		"Nov 16, 2026 at 7:10 AM", "Nov 16, 2026 at 7:10 AM", "November 16, 2026 at 7:10:00 AM", "Nov 16, 2026, 7:10 AM", "16 Nov 2026 at 07:10",
		json.Number("1794780600"), json.Number("1794780600000"), "1794780600",
	} {
		got, ok := when(s, seoul)
		if !ok || !got.Equal(want) {
			t.Errorf("%v → %v %v", s, got, ok)
		}
	}
	if got, ok := when("2026. 11. 16. 오후 11:42", seoul); !ok || !got.Equal(at(t, "2026-11-16T23:42:00+09:00")) {
		t.Errorf("오후: %v", got)
	}
	if got, ok := when("2026. 11. 16. 오전 12:05", seoul); !ok || !got.Equal(at(t, "2026-11-16T00:05:00+09:00")) {
		t.Errorf("오전 12시는 자정: %v", got)
	}
	for _, s := range []any{"어제", "07:10", "2026-13-01 07:10", json.Number("7")} {
		if got, ok := when(s, seoul); ok {
			t.Errorf("%v 는 시각이 아님: %v", s, got)
		}
	}
}

func TestSimpleNight(t *testing.T) {
	n := one(t, `{"source":"자동화 앱","start":"2026-11-15 23:40:00 +0900","end":"2026-11-16 07:10:00 +0900",
		"asleepMinutes":412,"deepMinutes":60,"coreMinutes":250,"remMinutes":102}`)
	check(t, n, "2026-11-16 11-15 23:40→11-16 07:10 asleep=412 inBed=450 awake=38 deep=60 core=250 rem=102 bedOnly=false")
	if n.Timeline != nil {
		t.Fatalf("합해서 온 밤에는 타임라인이 없음: %v", n.Timeline)
	}
	// The window alone is the sleep.
	check(t, one(t, `{"start":"2026-11-16T01:10:00+09:00","end":"2026-11-16T08:00:00+09:00"}`),
		"2026-11-16 11-16 01:10→11-16 08:00 asleep=410 inBed=410 awake=0 deep=- core=- rem=- bedOnly=false")
	// Hours, a list of nights, the awake time given.
	in, err := Parse([]byte(`{"nights":[
		{"bedAt":"2026-11-14T23:00:00+09:00","wakeAt":"2026-11-15T06:30:00+09:00","asleep_hours":6.5,"in bed hours":7.5},
		{"start":"2026-11-13T00:00:00+09:00","end":"2026-11-13T07:00:00+09:00","awakeMinutes":30}]}`), seoul)
	if err != nil || len(in.Nights) != 2 {
		t.Fatal(err, in)
	}
	check(t, in.Nights[0], "2026-11-15 11-14 23:00→11-15 06:30 asleep=390 inBed=450 awake=60 deep=- core=- rem=- bedOnly=false")
	check(t, in.Nights[1], "2026-11-13 11-13 00:00→11-13 07:00 asleep=390 inBed=420 awake=30 deep=- core=- rem=- bedOnly=false")
	// No times, no night.
	in, err = Parse([]byte(`{"asleepMinutes":400}`), seoul)
	if err != nil || len(in.Nights) != 0 || in.Skipped != 1 {
		t.Fatalf("시각 없는 밤: %v %+v", err, in)
	}
}

// Health Auto Export's REST export, as its help pages describe it (units hr,
// dates "yyyy-MM-dd HH:mm:ss Z"): summarized - a night an entry - and not.
func TestHealthAutoExport(t *testing.T) {
	in, err := Parse([]byte(`{"data":{"metrics":[
		{"name":"step_count","units":"count","data":[{"qty":5120,"date":"2026-11-16 00:00:00 +0900"}]},
		{"name":"sleep_analysis","units":"hr","data":[
			{"date":"2026-11-16 00:00:00 +0900","totalSleep":7.2,"asleep":0,"core":4.1,"deep":1.2,"rem":1.9,"inBed":7.6,"awake":0.4,
			 "sleepStart":"2026-11-15 23:48:00 +0900","sleepEnd":"2026-11-16 07:05:00 +0900",
			 "inBedStart":"2026-11-15 23:35:00 +0900","inBedEnd":"2026-11-16 07:11:00 +0900","source":"Apple Watch"},
			{"date":"2026-11-15 00:00:00 +0900","asleep":0.5,"core":4,"deep":1,"rem":1.5,
			 "sleepStart":"2026-11-15 00:10:00 +0900","sleepEnd":"2026-11-15 07:10:00 +0900"},
			{"date":"2026-11-14 00:00:00 +0900","totalSleep":0,"asleep":0,"core":0,"deep":0,"rem":0,"inBed":7.5,
			 "inBedStart":"2026-11-13 23:30:00 +0900","inBedEnd":"2026-11-14 07:00:00 +0900","source":"iPhone"}
		]}]}}`), seoul)
	if err != nil || in.Format != "health-auto-export" || len(in.Nights) != 3 || len(in.Samples) != 0 {
		t.Fatalf("%v %+v", err, in)
	}
	check(t, in.Nights[0], "2026-11-16 11-15 23:35→11-16 07:11 asleep=432 inBed=456 awake=24 deep=72 core=246 rem=114 bedOnly=false")
	if in.Nights[0].Source != "Apple Watch" {
		t.Fatalf("출처: %q", in.Nights[0].Source)
	}
	// No totalSleep: the part with no stage, and the stages on top.
	check(t, in.Nights[1], "2026-11-15 11-15 00:10→11-15 07:10 asleep=420 inBed=420 awake=0 deep=60 core=240 rem=90 bedOnly=false")
	// The iPhone alone: the bed and nothing of sleep.
	check(t, in.Nights[2], "2026-11-14 11-13 23:30→11-14 07:00 asleep=450 inBed=450 awake=0 deep=- core=- rem=- bedOnly=true")

	// Not summarized: a sample an entry.
	in, err = Parse([]byte(`{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[
		{"date":"2026-11-15 23:42:00 +0900","startDate":"2026-11-15 23:42:00 +0900","endDate":"2026-11-16 00:40:00 +0900","qty":0.9667,"value":"Core","source":"Apple Watch"},
		{"startDate":"2026-11-16 00:40:00 +0900","qty":0.8333,"value":"Deep","source":"Apple Watch"},
		{"start":"2026-11-16 01:30:00 +0900","end":"2026-11-16 02:00:00 +0900","value":"Unspecified"},
		{"start":"2026-11-16 02:00:00 +0900","end":"2026-11-16 02:05:00 +0900","value":"Snoring"}]}]}}`), seoul)
	if err != nil || len(in.Samples) != 3 || !slices.Equal(in.Unknown, []string{"Snoring"}) {
		t.Fatalf("%v %+v", err, in)
	}
	nights := Sleeps(in.Samples, seoul)
	if len(nights) != 1 {
		t.Fatalf("%+v", nights)
	}
	check(t, nights[0], "2026-11-16 11-15 23:42→11-16 02:00 asleep=138 inBed=138 awake=0 deep=50 core=58 rem=0 bedOnly=false")
}

// A night is the day it ended on in Seoul, whatever clock the times came on:
// a sleep from 23:40 to 07:05 sent in UTC ends on the 15th in UTC and is the
// 16th's.
func TestNightsAcrossMidnight(t *testing.T) {
	in, err := Parse([]byte(`[
		{"start":"2026-11-15T14:40:00Z","end":"2026-11-15T22:05:00Z","value":"Asleep"},
		{"start":"2026-11-16T05:00:00Z","end":"2026-11-16T05:45:00Z","value":"Asleep"},
		{"start":"2026-11-16T16:30:00Z","end":"2026-11-16T23:00:00Z","value":"Asleep"}]`), seoul)
	if err != nil {
		t.Fatal(err)
	}
	nights := Sleeps(in.Samples, seoul)
	var got []string
	for _, n := range nights {
		got = append(got, fmt.Sprintf("%s %d", n.Night, n.AsleepMinutes))
	}
	// The night of the 15th to the 16th, the 16th's afternoon nap (14:00),
	// and the night that began after midnight (01:30 to 08:00, the 17th's).
	if want := []string{"2026-11-16 445", "2026-11-16 45", "2026-11-17 390"}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Two sources saying the same stretch, and an hour's gap: counted once, and
// the gap splits the sleep.
func TestOverlapsAndGaps(t *testing.T) {
	ss := []Sample{
		{at(t, "2026-11-15T23:00:00+09:00"), at(t, "2026-11-16T03:00:00+09:00"), Asleep, "AutoSleep"},
		{at(t, "2026-11-15T23:00:00+09:00"), at(t, "2026-11-16T03:00:00+09:00"), Asleep, "Pillow"},
		{at(t, "2026-11-16T01:00:00+09:00"), at(t, "2026-11-16T01:20:00+09:00"), Awake, "Apple Watch"},
		// 59 minutes later: the same sleep.
		{at(t, "2026-11-16T03:59:00+09:00"), at(t, "2026-11-16T06:00:00+09:00"), Asleep, "AutoSleep"},
		// 61 minutes after that: another.
		{at(t, "2026-11-16T07:01:00+09:00"), at(t, "2026-11-16T07:30:00+09:00"), Asleep, "AutoSleep"},
		// Nonsense: ends before it starts, or a stage nobody knows.
		{at(t, "2026-11-16T09:00:00+09:00"), at(t, "2026-11-16T08:00:00+09:00"), Asleep, ""},
		{at(t, "2026-11-16T09:00:00+09:00"), at(t, "2026-11-16T10:00:00+09:00"), Stage("dream"), ""},
	}
	nights := Sleeps(ss, seoul)
	if len(nights) != 2 {
		t.Fatalf("잠 둘: %+v", nights)
	}
	// 23:00-03:00 less the 20 minutes awake, and 03:59-06:00.
	check(t, nights[0], "2026-11-16 11-15 23:00→11-16 06:00 asleep=341 inBed=361 awake=20 deep=- core=- rem=- bedOnly=false")
	if nights[0].Source != "AutoSleep, Pillow, Apple Watch" {
		t.Fatalf("출처: %q", nights[0].Source)
	}
	// The gap is not in bed: 361 minutes covered of 420.
	check(t, nights[1], "2026-11-16 11-16 07:01→11-16 07:30 asleep=29 inBed=29 awake=0 deep=- core=- rem=- bedOnly=false")
	// Awake alone is no sleep.
	if got := Sleeps([]Sample{{at(t, "2026-11-16T03:00:00+09:00"), at(t, "2026-11-16T03:30:00+09:00"), Awake, ""}}, seoul); len(got) != 0 {
		t.Fatalf("깬 것만으로는 잠이 아님: %+v", got)
	}
}

func night(t *testing.T, from, to string, asleep int) Night {
	t.Helper()
	b, w := at(t, from), at(t, to)
	return Night{Night: DayOf(w, seoul), BedAt: b, WakeAt: w, AsleepMinutes: asleep, InBedMinutes: int(w.Sub(b).Minutes()), Naps: []Nap{}}
}

func TestMergeSameNightAgain(t *testing.T) {
	n := night(t, "2026-11-15T23:30:00+09:00", "2026-11-16T07:10:00+09:00", 433)
	day, res := Merge(nil, []Night{n})
	if day == nil || res[0] != Saved || day.AsleepMinutes != 433 {
		t.Fatalf("%+v %v", day, res)
	}
	again, res := Merge(day, []Night{n})
	if res[0] != Saved || again.AsleepMinutes != 433 || len(again.Naps) != 0 || !again.BedAt.Equal(day.BedAt) {
		t.Fatalf("같은 밤을 다시: %+v %v", again, res)
	}
}

func TestMergeMoreAndLessOfANight(t *testing.T) {
	early := night(t, "2026-11-15T23:30:00+09:00", "2026-11-16T06:50:00+09:00", 410)
	full := night(t, "2026-11-15T23:30:00+09:00", "2026-11-16T07:10:00+09:00", 433)
	// Sent at waking, before the watch gave the end; then whole.
	day, _ := Merge(nil, []Night{early})
	day, res := Merge(day, []Night{full})
	if res[0] != Saved || day.AsleepMinutes != 433 {
		t.Fatalf("더 본 밤이 바꿈: %+v %v", day, res)
	}
	// The tail of the night, cut by the window it was asked for in: kept out.
	tail := night(t, "2026-11-16T06:00:00+09:00", "2026-11-16T07:10:00+09:00", 70)
	day, res = Merge(day, []Night{tail})
	if res[0] != Kept || day.AsleepMinutes != 433 || len(day.Naps) != 0 {
		t.Fatalf("잘린 꼬리는 남은 밤을 덮지 않음: %+v %v", day, res)
	}
	// Within Slack it is as much of it: the later one stands (stages
	// reprocessed by the watch).
	redone := night(t, "2026-11-15T23:33:00+09:00", "2026-11-16T07:05:00+09:00", 425)
	day, res = Merge(day, []Night{redone})
	if res[0] != Saved || day.AsleepMinutes != 425 {
		t.Fatalf("10분 안은 같은 길이: %+v %v", day, res)
	}
}

func TestMergeNaps(t *testing.T) {
	n := night(t, "2026-11-15T23:30:00+09:00", "2026-11-16T07:10:00+09:00", 433)
	nap := night(t, "2026-11-16T14:00:00+09:00", "2026-11-16T14:40:00+09:00", 38)
	doze := night(t, "2026-11-16T21:00:00+09:00", "2026-11-16T21:06:00+09:00", 6)
	day, res := Merge(&n, []Night{nap, doze})
	if !slices.Equal(res, []Result{Napped, Short}) || day.AsleepMinutes != 433 || len(day.Naps) != 1 || day.Naps[0].AsleepMinutes != 38 {
		t.Fatalf("낮잠: %+v %v", day, res)
	}
	// The nap again, and more of it: still one.
	longer := night(t, "2026-11-16T13:55:00+09:00", "2026-11-16T14:50:00+09:00", 50)
	day, _ = Merge(day, []Night{nap})
	day, _ = Merge(day, []Night{longer})
	if len(day.Naps) != 1 || day.Naps[0].AsleepMinutes != 50 {
		t.Fatalf("같은 낮잠은 하나: %+v", day.Naps)
	}
	// A night sent with its day's nap, the longest first whatever the order.
	day, res = Merge(nil, []Night{nap, n})
	if !slices.Equal(res, []Result{Napped, Saved}) || day.AsleepMinutes != 433 || len(day.Naps) != 1 {
		t.Fatalf("한 번에 온 밤과 낮잠: %+v %v", day, res)
	}
}

// Yesterday afternoon's nap at the edge of the first send's window: too short
// to be the day's night, with none to sit beside. Two hours of it is a night
// until the day's real night comes, which makes it a nap.
func TestMergeNapBeforeItsNight(t *testing.T) {
	nap := night(t, "2026-11-15T14:00:00+09:00", "2026-11-15T14:40:00+09:00", 40)
	day, res := Merge(nil, []Night{nap})
	if day != nil || res[0] != Short {
		t.Fatalf("짧은 낮잠은 밤이 아님: %+v %v", day, res)
	}
	long := night(t, "2026-11-15T13:00:00+09:00", "2026-11-15T15:00:00+09:00", 120)
	day, _ = Merge(nil, []Night{long})
	real := night(t, "2026-11-14T23:30:00+09:00", "2026-11-15T07:00:00+09:00", 420)
	day, res = Merge(day, []Night{real})
	if res[0] != Saved || day.AsleepMinutes != 420 || len(day.Naps) != 1 || day.Naps[0].AsleepMinutes != 120 {
		t.Fatalf("진짜 밤이 오면 낮잠으로: %+v %v", day, res)
	}
	// A nap kept before turns out to be the night's own end: it goes.
	end := night(t, "2026-11-16T07:30:00+09:00", "2026-11-16T08:30:00+09:00", 60)
	n := night(t, "2026-11-15T23:30:00+09:00", "2026-11-16T07:10:00+09:00", 433)
	day, _ = Merge(&n, []Night{end})
	whole := night(t, "2026-11-15T23:30:00+09:00", "2026-11-16T08:30:00+09:00", 493)
	day, res = Merge(day, []Night{whole})
	if res[0] != Saved || len(day.Naps) != 0 {
		t.Fatalf("밤에 든 낮잠은 밤의 것: %+v %v", day, res)
	}
}

// The phone's time in bed against the watch's night: what knows sleep wins,
// whichever is longer.
func TestMergeBedOnly(t *testing.T) {
	bed := night(t, "2026-11-15T22:30:00+09:00", "2026-11-16T07:30:00+09:00", 540)
	bed.BedOnly = true
	watch := night(t, "2026-11-15T23:40:00+09:00", "2026-11-16T07:05:00+09:00", 420)
	day, res := Merge(&bed, []Night{watch})
	if res[0] != Saved || day.BedOnly || day.AsleepMinutes != 420 {
		t.Fatalf("워치의 밤이 침대만을 바꿈: %+v %v", day, res)
	}
	day, res = Merge(&watch, []Night{bed})
	if res[0] != Kept || day.BedOnly {
		t.Fatalf("침대만으로는 바꾸지 않음: %+v %v", day, res)
	}
}

func TestSummarize(t *testing.T) {
	var ns []Night
	// Thirty nights back from the 16th: 23:30 and 00:30 by turns, waking at
	// 07:00 and 07:20, 420 and 400 minutes - and one forty days back, outside.
	for i := 0; i < 30; i++ {
		day := time.Date(2026, 11, 16-i, 0, 0, 0, 0, seoul)
		bed, wake, asleep := day.Add(-30*time.Minute), day.Add(7*time.Hour), 420
		if i%2 == 1 {
			bed, wake, asleep = day.Add(30*time.Minute), day.Add(7*time.Hour+20*time.Minute), 400
		}
		ns = append(ns, Night{Night: DayOf(wake, seoul), BedAt: bed, WakeAt: wake, AsleepMinutes: asleep})
	}
	old := time.Date(2026, 10, 7, 7, 0, 0, 0, seoul)
	ns = append(ns, Night{Night: DayOf(old, seoul), BedAt: old.Add(-8 * time.Hour), WakeAt: old, AsleepMinutes: 100})
	s := Summarize(ns, "2026-11-16", seoul)
	// Midnight is the mean of 23:30 and 00:30, not noon.
	if s.Week != (Window{Days: 7, Nights: 7, AsleepMinutes: 411, BedAt: "23:56", WakeAt: "07:09", BedSpreadMinutes: 29, WakeSpreadMinutes: 10}) {
		t.Fatalf("주: %+v", s.Week)
	}
	if s.Month != (Window{Days: 30, Nights: 30, AsleepMinutes: 410, BedAt: "00:00", WakeAt: "07:10", BedSpreadMinutes: 30, WakeSpreadMinutes: 10}) {
		t.Fatalf("달: %+v", s.Month)
	}
	if empty := Summarize(nil, "2026-11-16", seoul); empty.Week != (Window{Days: 7}) {
		t.Fatalf("빈 주: %+v", empty.Week)
	}
}
