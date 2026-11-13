// Package holiday knows Korea's public holidays. They change from year to
// year - substitute days, election days, a day the law makes new - so they are
// read off a public calendar feed once a day, and a list kept here fills in
// what the feed leaves out or cannot give while it is unreachable.
package holiday

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// GoogleKR is Google's public calendar of Korea's holidays: public holidays
// carry the description 공휴일, days that are only observed 기념일.
const GoogleKR = "https://calendar.google.com/calendar/ical/ko.south_korea%23holiday%40group.v.calendar.google.com/public/basic.ics"

// Source reads the feed and keeps what it read.
type Source struct {
	url  string
	http *http.Client
	log  *slog.Logger

	mu    sync.Mutex
	days  map[string]string // YYYY-MM-DD → name
	read  time.Time         // when the feed was last read
	tried time.Time         // when it was last asked for
}

// fresh is how long a reading stands; retry how soon a failed one is tried again.
const (
	fresh = 24 * time.Hour
	retry = 10 * time.Minute
)

// New reads from url; empty reads only the kept list.
func New(url string, log *slog.Logger) *Source {
	return &Source{url: url, http: &http.Client{Timeout: 10 * time.Second}, log: log}
}

// Between is the holidays from one day to another, both kept.
func (s *Source) Between(ctx context.Context, from, to string) (map[string]string, error) {
	if !day.MatchString(from) || !day.MatchString(to) || from > to {
		return nil, fmt.Errorf("from 과 to 는 YYYY-MM-DD 여야 해요")
	}
	days := s.current(ctx)
	out := map[string]string{}
	for d, name := range days {
		if d >= from && d <= to {
			out[d] = name
		}
	}
	return out, nil
}

var day = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// current is the feed as last read, joined with the kept list, reading it
// again when it is a day old.
func (s *Source) current(ctx context.Context) map[string]string {
	s.mu.Lock()
	stale := s.url != "" && time.Since(s.read) > fresh && time.Since(s.tried) > retry
	if stale {
		s.tried = time.Now()
	}
	days := s.days
	s.mu.Unlock()

	if stale {
		got, err := s.fetch(ctx)
		if err != nil {
			s.log.Warn("공휴일 피드를 읽지 못했습니다", "err", err)
		} else {
			s.mu.Lock()
			s.days, s.read = got, time.Now()
			days = got
			s.mu.Unlock()
		}
	}
	return join(days, kept)
}

func (s *Source) fetch(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("공휴일 피드: %s", res.Status)
	}
	days, err := Parse(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("공휴일 피드에 공휴일이 없습니다")
	}
	return days, nil
}

// Parse reads the public holidays out of an iCalendar feed: all-day events
// whose description starts with 공휴일. Two on one day read as one name.
func Parse(r io.Reader) (map[string]string, error) {
	var lines []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		// A folded line goes on with a space or a tab.
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := map[string]string{}
	var start, end, summary, description string
	in := false
	for _, l := range lines {
		switch {
		case l == "BEGIN:VEVENT":
			in, start, end, summary, description = true, "", "", "", ""
		case l == "END:VEVENT":
			in = false
			if !strings.HasPrefix(description, "공휴일") || start == "" || summary == "" {
				continue
			}
			name := Name(summary)
			for _, d := range span(start, end) {
				if have, ok := out[d]; ok && have != name && !strings.Contains(have, name) {
					out[d] = have + "·" + name
				} else if !ok {
					out[d] = name
				}
			}
		case !in:
		case strings.HasPrefix(l, "DTSTART"):
			start = value(l)
		case strings.HasPrefix(l, "DTEND"):
			end = value(l)
		case strings.HasPrefix(l, "SUMMARY"):
			summary = strings.TrimSpace(unescape(value(l)))
		case strings.HasPrefix(l, "DESCRIPTION"):
			description = strings.TrimSpace(unescape(value(l)))
		}
	}
	return out, nil
}

func value(line string) string {
	_, v, _ := strings.Cut(line, ":")
	return v
}

func unescape(s string) string {
	return strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}

// span is the days of an all-day event, its end not kept (iCalendar's way).
func span(start, end string) []string {
	s, err := time.Parse("20060102", start)
	if err != nil {
		return nil
	}
	e, err := time.Parse("20060102", end)
	if err != nil || !e.After(s) {
		e = s.AddDate(0, 0, 1)
	}
	var out []string
	for d := s; d.Before(e) && len(out) < 14; d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format(time.DateOnly))
	}
	return out
}

// Name is a holiday as the calendar shows it: the feed's 새해첫날 is 신정, a
// day off for a holiday on a weekend is 대체공휴일.
func Name(summary string) string {
	switch {
	case strings.HasPrefix(summary, "쉬는 날"), strings.Contains(summary, "대체"):
		return "대체공휴일"
	case summary == "새해첫날", summary == "새해 첫날":
		return "신정"
	case summary == "크리스마스":
		return "성탄절"
	case summary == "지방선거일":
		return "지방선거"
	}
	return summary
}

// join is the feed with the kept list's days it does not have.
func join(feed, list map[string]string) map[string]string {
	out := make(map[string]string, len(feed)+len(list))
	for d, n := range list {
		out[d] = n
	}
	for d, n := range feed {
		out[d] = n
	}
	return out
}

// Days is every day kept in a set, in order: for tests and checks.
func Days(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for d := range m {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// kept is the government's list for the years it has announced, for what a
// feed misses (the first day of 2027's 설날 연휴 is a Saturday the feed
// leaves out) and for while no feed can be read.
var kept = map[string]string{
	"2026-01-01": "신정", "2026-02-16": "설날 연휴", "2026-02-17": "설날", "2026-02-18": "설날 연휴", "2026-03-01": "삼일절",
	"2026-03-02": "대체공휴일", "2026-05-01": "노동절", "2026-05-05": "어린이날", "2026-05-24": "부처님오신날", "2026-05-25": "대체공휴일",
	"2026-06-03": "지방선거", "2026-06-06": "현충일", "2026-07-17": "제헌절", "2026-08-15": "광복절", "2026-08-17": "대체공휴일",
	"2026-09-24": "추석 연휴", "2026-09-25": "추석", "2026-09-26": "추석 연휴", "2026-10-03": "개천절", "2026-10-05": "대체공휴일",
	"2026-10-09": "한글날", "2026-12-25": "성탄절",
	"2027-01-01": "신정", "2027-02-06": "설날 연휴", "2027-02-07": "설날", "2027-02-08": "설날 연휴", "2027-02-09": "대체공휴일",
	"2027-03-01": "삼일절", "2027-05-01": "노동절", "2027-05-03": "대체공휴일", "2027-05-05": "어린이날", "2027-05-13": "부처님오신날",
	"2027-06-06": "현충일", "2027-07-17": "제헌절", "2027-07-19": "대체공휴일", "2027-08-15": "광복절", "2027-08-16": "대체공휴일",
	"2027-09-14": "추석 연휴", "2027-09-15": "추석", "2027-09-16": "추석 연휴", "2027-10-03": "개천절", "2027-10-04": "대체공휴일",
	"2027-10-09": "한글날", "2027-10-11": "대체공휴일", "2027-12-25": "성탄절", "2027-12-27": "대체공휴일",
}
