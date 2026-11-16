package sleep

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Input is what one send said: samples to make sleeps of, nights summed
// already, and what could not be read.
type Input struct {
	// Format is the shape it came in: samples, nights or health-auto-export.
	Format  string
	Samples []Sample
	Nights  []Night
	// Unknown is the values read as no stage, each once (ten at most): a name
	// for a stage not known here shows itself in the reply.
	Unknown []string
	// Skipped counts the entries without the times to place them.
	Skipped int
	// Source is what the send says it came from, at its top level.
	Source string
}

var (
	ErrJSON    = errors.New("JSON 을 읽지 못했습니다")
	ErrNothing = errors.New("수면이 없습니다: samples, start·end, 또는 Health Auto Export 의 data.metrics(sleep_analysis) 가 필요합니다")
)

// Parse reads a send in any of the shapes it comes in:
//
//   - samples, as a shortcut sends iOS's sleep analysis: {"samples": [{start,
//     end, value, source?}]} or the list alone. The value in English (In Bed,
//     Asleep, Awake, Core, Deep, REM, Asleep Core …), in Korean as the Health
//     app says it (침대에 있음, 수면, 깨어 있음, 코어 수면, 깊은 수면, 렘 수면), or
//     as HealthKit's number (0 in bed, 1 asleep, 2 awake, 3 core, 4 deep, 5 REM);
//   - a night summed by whatever sends it: {start, end, asleepMinutes?,
//     inBedMinutes?, awakeMinutes?, deepMinutes?, coreMinutes?, remMinutes?,
//     source?}, or {"nights": [...]}. Hours as asleepHours and the like; a
//     bare amount ("deep": 62) is minutes. Without asleepMinutes, the window
//     is the sleep;
//   - Health Auto Export's REST export: {"data": {"metrics": [{"name":
//     "sleep_analysis", "units": "hr", "data": [...]}]}}. Summarized, an entry
//     is a night (sleepStart, sleepEnd, inBedStart, inBedEnd, totalSleep,
//     asleep - the part with no stage -, inBed, awake, core, deep, rem, source)
//     in the metric's units; not summarized, an entry is a sample (startDate,
//     endDate, qty, value, source).
//
// Keys are read in any case, with or without spaces and underscores, and the
// common Korean ones too. Times are ISO 8601 with or without an offset (none
// is Seoul's clock), "2026-11-16 07:10:00 +0900", seconds since 1970, or as
// a shortcut prints a date it was not told to format ("2026. 11. 16. 오전
// 7:10", "Nov 16, 2026 at 7:10 AM").
func Parse(body []byte, loc *time.Location) (Input, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return Input{}, ErrJSON
	}
	var in Input
	switch t := v.(type) {
	case []any:
		in.Format = "samples"
		in.entries(t, 1, loc)
	case map[string]any:
		m := keyed(t)
		in.Source = Clip(text(m["source"]), 40)
		data, _ := m["data"].(map[string]any)
		switch {
		case data != nil:
			in.Format = "health-auto-export"
			for _, raw := range list(keyed(data)["metrics"]) {
				metric, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				mk := keyed(metric)
				if !strings.Contains(norm(text(mk["name"])), "sleep") {
					continue // sleep_analysis is the one; the export may carry others
				}
				in.entries(list(mk["data"]), unitOf(text(mk["units"])), loc)
			}
		case m["samples"] != nil:
			in.Format = "samples"
			in.entries(list(m["samples"]), 1, loc)
		case m["nights"] != nil:
			in.Format = "nights"
			in.entries(list(m["nights"]), 1, loc)
		default:
			in.Format = "nights"
			in.entry(m, 1, loc)
		}
	}
	if len(in.Samples)+len(in.Nights) == 0 && in.Skipped == 0 && len(in.Unknown) == 0 {
		return in, ErrNothing
	}
	return in, nil
}

// entries reads a list, each entry a sample (it says a value) or a night.
// unit is how many minutes a bare amount is.
func (in *Input) entries(items []any, unit float64, loc *time.Location) {
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			in.Skipped++
			continue
		}
		in.entry(keyed(m), unit, loc)
	}
}

func (in *Input) entry(m map[string]any, unit float64, loc *time.Location) {
	if get(m, "value", "stage", "값", "단계") != nil {
		in.sample(m, unit, loc)
		return
	}
	in.night(m, unit, loc)
}

func (in *Input) sample(m map[string]any, unit float64, loc *time.Location) {
	start, okS := when(get(m, "start", "startdate", "from", "시작", "시작날짜", "시작일", "date"), loc)
	end, okE := when(get(m, "end", "enddate", "to", "종료", "종료날짜", "종료일", "끝"), loc)
	if okS && !okE {
		// Health Auto Export's qty is the stretch's length, in the metric's units.
		if q, ok := number(get(m, "qty", "duration")); ok && q > 0 {
			end, okE = start.Add(time.Duration(q*unit*float64(time.Minute))), true
		}
	}
	if !okS || !okE {
		in.Skipped++
		return
	}
	raw := get(m, "value", "stage", "값", "단계")
	stage, ok := StageOf(raw)
	if !ok {
		in.unknown(raw)
		return
	}
	in.Samples = append(in.Samples, Sample{Start: start, End: end, Stage: stage, Source: text(get(m, "source", "sourcename", "소스", "출처"))})
}

func (in *Input) night(m map[string]any, unit float64, loc *time.Location) {
	// The bed's window first, the sleep's when that is all there is.
	bed, okB := when(get(m, "inbedstart", "start", "bedat", "bedtime", "sleepstart", "시작", "취침"), loc)
	wake, okW := when(get(m, "inbedend", "end", "wakeat", "waketime", "sleepend", "종료", "기상"), loc)

	// An amount with its unit in its name, else a bare one in the send's unit.
	named := func(name string) (float64, bool) {
		for _, u := range []struct {
			suffix string
			k      float64
		}{{"minutes", 1}, {"mins", 1}, {"min", 1}, {"hours", 60}, {"hrs", 60}, {"hr", 60}} {
			if v, ok := number(m[name+u.suffix]); ok {
				return v * u.k, true
			}
		}
		return 0, false
	}
	bare := func(name string) (float64, bool) {
		v, ok := number(m[name])
		return v * unit, ok
	}
	amount := func(name string) (float64, bool) {
		if v, ok := named(name); ok {
			return v, true
		}
		return bare(name)
	}
	deep, okD := amount("deep")
	core, okC := amount("core")
	if !okC {
		core, okC = amount("light")
	}
	rem, okR := amount("rem")
	stages := max(0, deep) + max(0, core) + max(0, rem)
	// The whole of sleep: totalSleep, or asleepMinutes (a unit in the name
	// says it is the whole). Health Auto Export's bare asleep is only the part
	// with no stage, so the stages go on top of it.
	total, okT := amount("totalsleep")
	if !okT {
		total, okT = named("asleep")
	}
	if !okT {
		total, okT = named("sleep")
	}
	if a, ok := bare("asleep"); !okT && (ok || okD || okC || okR) {
		total, okT = max(0, a)+stages, true
	}
	inBed, okI := amount("inbed")
	awake, okA := amount("awake")

	// A window given at one end only: the other from how long it was.
	if length, ok := firstOk(inBed, okI, total, okT); ok && length > 0 {
		switch {
		case okB && !okW:
			wake, okW = bed.Add(time.Duration(length*float64(time.Minute))), true
		case okW && !okB:
			bed, okB = wake.Add(-time.Duration(length*float64(time.Minute))), true
		}
	}
	if !okB || !okW || !wake.After(bed) || wake.Sub(bed) > 24*time.Hour {
		in.Skipped++
		return
	}
	span := wake.Sub(bed).Minutes()
	if !okI || inBed <= 0 {
		inBed = span
	}
	if !okT {
		if okA {
			total = inBed - awake
		} else {
			total = span // the window it gave is the sleep
		}
	}
	n := Night{
		Night: DayOf(wake, loc), BedAt: bed, WakeAt: wake, Naps: []Nap{},
		Source: Clip(text(get(m, "source", "sleepsource", "inbedsource", "소스", "출처")), 120),
	}
	n.InBedMinutes = round(inBed)
	if total <= 0 {
		// Nothing of sleep, only the bed (an iPhone without a watch).
		n.BedOnly, n.AsleepMinutes = true, n.InBedMinutes
	} else {
		n.AsleepMinutes = round(total)
		if okA {
			n.AwakeMinutes = max(0, round(awake))
		} else {
			n.AwakeMinutes = max(0, n.InBedMinutes-n.AsleepMinutes)
		}
		if (okD || okC || okR) && stages > 0 {
			n.DeepMinutes, n.CoreMinutes, n.REMMinutes = ptr(round(deep)), ptr(round(core)), ptr(round(rem))
		}
	}
	in.Nights = append(in.Nights, n)
}

func firstOk(a float64, okA bool, b float64, okB bool) (float64, bool) {
	if okA && a > 0 {
		return a, true
	}
	return b, okB
}

func round(v float64) int { return int(math.Round(max(0, v))) }

func (in *Input) unknown(v any) {
	word := Clip(text(v), 40)
	if word == "" {
		word = "(값 없음)"
	}
	if len(in.Unknown) < 10 && !slices.Contains(in.Unknown, word) {
		in.Unknown = append(in.Unknown, word)
	}
}

// StageOf reads a sample's value: HealthKit's number, or the stage's name in
// English or Korean, in any case and spacing. The more particular word is
// looked for first - 렘 수면 is REM before it is 수면, Asleep Core is core.
func StageOf(v any) (Stage, bool) {
	if n, ok := v.(json.Number); ok {
		v = n.String()
	}
	if f, ok := v.(float64); ok {
		v = strconv.FormatFloat(f, 'f', -1, 64)
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	w := strings.NewReplacer("(", "", ")", "", "[", "", "]", "").Replace(norm(s))
	w = strings.TrimPrefix(w, "hkcategoryvaluesleepanalysis")
	if w == "" || w == "sleepanalysis" || w == "수면분석" {
		return "", false // the kind's name, not a stage
	}
	switch w {
	case "0":
		return InBed, true
	case "1":
		return Asleep, true
	case "2":
		return Awake, true
	case "3":
		return Core, true
	case "4":
		return Deep, true
	case "5":
		return REM, true
	}
	has := func(words ...string) bool {
		return slices.ContainsFunc(words, func(x string) bool { return strings.Contains(w, x) })
	}
	switch {
	case has("inbed", "침대", "누움", "누워"):
		return InBed, true
	case has("awake", "wake", "깨어", "깸", "깬"):
		return Awake, true
	case has("deep", "깊은", "깊음", "숙면"):
		return Deep, true
	case has("rem", "렘", "램"):
		return REM, true
	case has("core", "light", "코어", "얕은", "얕음"):
		return Core, true
	case has("asleep", "sleep", "수면", "잠", "unspecified", "지정"):
		return Asleep, true
	}
	return "", false
}

// unitOf is how many minutes one of a metric's units is: Health Auto Export
// says hr for sleep.
func unitOf(u string) float64 {
	switch norm(u) {
	case "min", "mins", "m", "minute", "minutes", "분":
		return 1
	case "s", "sec", "secs", "second", "seconds", "초":
		return 1.0 / 60
	}
	return 60
}

// keyed is a map with its keys in one form: lower case, without spaces,
// underscores, hyphens or dots.
func keyed(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[norm(k)] = v
	}
	return out
}

func norm(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '_' || r == '-' || r == '.' {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// get is the first of the keys that holds something.
func get(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil && v != "" {
			return v
		}
	}
	return nil
}

// list is a JSON list - also one a shortcut put in a text field, still
// written as JSON, and a single entry on its own.
func list(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case map[string]any:
		return []any{t}
	case string:
		dec := json.NewDecoder(strings.NewReader(t))
		dec.UseNumber()
		var out []any
		if dec.Decode(&out) == nil {
			return out
		}
	}
	return nil
}

func text(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	}
	return ""
}

func number(v any) (float64, bool) {
	var f float64
	var err error
	switch t := v.(type) {
	case json.Number:
		f, err = t.Float64()
	case float64:
		f = t
	case string:
		f, err = strconv.ParseFloat(strings.TrimSpace(t), 64)
	default:
		return 0, false
	}
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

var (
	// With an offset: as given.
	zoned = []string{
		time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04Z07:00", "2006-01-02T15:04Z0700",
		"2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05 -07:00", "2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00", "2006-01-02 15:04 -0700",
	}
	// Without one: on the operator's clock.
	local = []string{
		"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04",
		"2006/01/02 15:04:05", "2006/01/02 15:04",
		"Jan 2, 2006 at 3:04 PM", "Jan 2, 2006 at 3:04:05 PM", "January 2, 2006 at 3:04 PM", "January 2, 2006 at 3:04:05 PM",
		"Jan 2, 2006, 3:04 PM", "Jan 2, 2006, 3:04:05 PM", "January 2, 2006, 3:04 PM", "January 2, 2006, 3:04:05 PM",
		"2 Jan 2006 at 15:04", "2 Jan 2006 at 15:04:05", "2 January 2006 at 15:04", "2 January 2006 at 15:04:05",
	}
	// As iOS prints a date in Korean: 2026. 11. 16. 오전 7:10, 2026년 11월 16일 (일) 오후 11:42:05.
	korean = regexp.MustCompile(`^(\d{4})\s*[.년/-]\s*(\d{1,2})\s*[.월/-]\s*(\d{1,2})\s*[.일]?\s*(?:\(?[월화수목금토일](?:요일)?\)?\s*)?(오전|오후|AM|PM|am|pm)?\s*(\d{1,2}):(\d{2})(?::(\d{2}))?\s*(오전|오후|AM|PM|am|pm)?$`)
)

// when reads a time (see Parse).
func when(v any, loc *time.Location) (time.Time, bool) {
	if v == nil {
		return time.Time{}, false
	}
	if f, ok := number(v); ok {
		if _, isText := v.(string); !isText || len(strings.TrimSpace(v.(string))) >= 10 {
			return epoch(f)
		}
	}
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	s = strings.Join(strings.Fields(strings.NewReplacer(" ", " ", " ", " ").Replace(s)), " ")
	for _, layout := range zoned {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	for _, layout := range local {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	if m := korean.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		h, _ := strconv.Atoi(m[5])
		mi, _ := strconv.Atoi(m[6])
		sec, _ := strconv.Atoi(m[7])
		half := strings.ToUpper(m[4] + m[8])
		switch {
		case (half == "오후" || half == "PM") && h < 12:
			h += 12
		case (half == "오전" || half == "AM") && h == 12:
			h = 0
		}
		if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 59 {
			return time.Time{}, false
		}
		return time.Date(y, time.Month(mo), d, h, mi, sec, 0, loc), true
	}
	return time.Time{}, false
}

// epoch is seconds since 1970, or milliseconds when that large.
func epoch(f float64) (time.Time, bool) {
	switch {
	case f > 1e12:
		return time.UnixMilli(int64(f)), true
	case f > 1e9:
		return time.Unix(int64(f), 0), true
	}
	return time.Time{}, false
}
