package calendar

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The same rules calendar-svc enforces, applied before the round trip.
//
// Duplicated on purpose. The model is told to send absolute dates, and this is
// what makes that instruction enforceable at the point where the answer goes
// back to it - a tool call that gets "날짜는 YYYY-MM-DD 형식이어야 합니다"
// immediately is one the model can fix in the same turn. The copy in the
// service is the one that guards the table against writers that never pass
// through here.
var (
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
)

func validate(e *Event) error {
	e.Title = strings.TrimSpace(e.Title)
	e.Date = strings.TrimSpace(e.Date)
	e.Start = strings.TrimSpace(e.Start)
	e.End = strings.TrimSpace(e.End)
	e.Place = strings.TrimSpace(e.Place)

	if e.Title == "" {
		return errors.New("제목이 비어 있습니다")
	}
	if !datePattern.MatchString(e.Date) {
		return fmt.Errorf("날짜는 YYYY-MM-DD 형식이어야 합니다: %q", e.Date)
	}
	if _, err := time.Parse("2006-01-02", e.Date); err != nil {
		return fmt.Errorf("없는 날짜입니다: %q", e.Date)
	}
	for _, t := range []string{e.Start, e.End} {
		if t != "" && !timePattern.MatchString(t) {
			return fmt.Errorf("시각은 HH:MM 형식이어야 합니다: %q", t)
		}
	}
	if e.End != "" && e.Start == "" {
		return errors.New("끝나는 시각만 있고 시작 시각이 없습니다")
	}
	e.EndDate = strings.TrimSpace(e.EndDate)
	if e.EndDate == e.Date {
		e.EndDate = ""
	}
	if e.EndDate != "" {
		if !datePattern.MatchString(e.EndDate) {
			return fmt.Errorf("끝나는 날짜는 YYYY-MM-DD 형식이어야 합니다: %q", e.EndDate)
		}
		if e.EndDate < e.Date {
			return fmt.Errorf("끝나는 날짜가 시작보다 빠릅니다: %s → %s", e.Date, e.EndDate)
		}
	}
	// 여러 날에 걸친 일정의 끝시각은 마지막 날의 것이라 시작보다 이를 수 있다.
	if e.Start != "" && e.End != "" && e.End < e.Start && e.EndDate == "" {
		return fmt.Errorf("끝나는 시각이 시작보다 빠릅니다: %s → %s", e.Start, e.End)
	}
	return nil
}
