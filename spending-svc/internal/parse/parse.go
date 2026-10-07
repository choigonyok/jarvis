// Package parse reads a card company's payment alert.
//
// There is no single format. Each issuer has its own, each has changed it
// more than once, and the same issuer sends a labelled form ("가맹점 : 스타벅스")
// over 알림톡 and a run-on form over SMS. So this does not match templates.
// It looks for the parts every alert has - an amount, a date and time, a
// merchant - by what they look like, and treats anything it cannot account
// for as unreadable rather than guessing. An unreadable alert goes to a queue
// a person sees; a guessed one goes into the total and nobody notices.
package parse

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Outcome int

const (
	// NotPayment is a notice, an advert, a statement summary - nothing to record.
	NotPayment Outcome = iota
	Parsed
	// Unreadable looked like a payment but some part could not be found.
	Unreadable
)

const (
	KindApproval = "approval"
	KindCancel   = "cancel"
)

type Alert struct {
	Issuer   string
	CardTail string
	Kind     string
	// AmountKrw is 0 for a foreign charge with no won figure; the caller
	// estimates it.
	AmountKrw     int64
	Currency      string // KRW | USD
	ForeignAmount float64
	Installment   int // 0 = 일시불
	Merchant      string
	ApprovedAt    time.Time
}

var (
	krwRe     = regexp.MustCompile(`(\d{1,3}(?:,\d{3})+|\d+)\s*원`)
	foreignRe = regexp.MustCompile(`(?i)(USD|US\$|\$|EUR|JPY|GBP|CNY)\s*([\d,]+(?:\.\d+)?)|([\d,]+(?:\.\d+)?)\s*(USD|EUR|JPY|GBP|CNY|달러|엔|유로)`)
	// 10/06 12:34, 2026.10.06 12:34, 10-06(월) 12:34
	whenRe = regexp.MustCompile(`(?:(\d{4})[./-])?(\d{1,2})[./-](\d{1,2})\s*(?:\([월화수목금토일]\))?\s*(\d{1,2}):(\d{2})`)
	// 신한카드(1234), 신한(1*2*), 1234승인
	tailParenRe = regexp.MustCompile(`\(([\d*]{4})\)`)
	tailWordRe  = regexp.MustCompile(`(?:^|[^\d,])(\d{4})\s*(?:승인|취소)`)
	monthsRe    = regexp.MustCompile(`(\d{1,2})\s*개월`)
	labelRe     = regexp.MustCompile(`^(?:이용)?(?:가맹점(?:명)?|사용처|이용처|결제처)\s*[:：]?\s*(.+)$`)
	issuerRe    = regexp.MustCompile(`\[([^\]]*카드)\]`)
	maskedName  = regexp.MustCompile(`^[가-힣A-Za-z]{1,2}\*[가-힣A-Za-z*]{0,3}\s*(?:님|고객님)?$`)
	labelled    = regexp.MustCompile(`^[가-힣A-Za-z ]{1,8}\s*[:：]`)
	// 이용카드 : 하나 5*2*  - a labelled card line, tail at its end.
	tailLabelRe = regexp.MustCompile(`(?m)^[\s◆◇■□●○•·▶▷※*-]*(?:이용)?카드\s*[:：][^\n]*?([\d*]{4})\s*$`)
	// Bullets some issuers put in front of every line ("◆ 사용금액", "  - 가맹점명").
	bulletRe = regexp.MustCompile(`^[\s◆◇■□●○•·▶▷※*-]+`)
)

// Words that put a won figure next to the charge without being the charge.
var notTheAmount = []string{"누적", "잔액", "한도", "포인트", "적립", "청구", "결제예정", "이용가능", "할인", "캐시백"}

// Kept in one place so a new kind of non-merchant line is one entry.
var notMerchantPrefix = []string{"누적", "잔액", "한도", "일시불", "할부", "포인트", "적립", "승인", "취소", "결제", "사용", "카드", "금액", "일시", "일자"}

// Parse reads one message. issuer is the chat's name, which for an 알림톡
// channel is the card company.
func Parse(issuer, text string, sentAt time.Time) (Alert, Outcome) {
	text = strings.ReplaceAll(text, "\r", "")
	if strings.Contains(text, "(광고)") || strings.Contains(text, "[광고]") {
		return Alert{}, NotPayment
	}
	if !strings.ContainsAny(text, "0123456789") ||
		!(strings.Contains(text, "승인") || strings.Contains(text, "결제") ||
			strings.Contains(text, "취소") || strings.Contains(text, "사용")) {
		return Alert{}, NotPayment
	}
	when := whenRe.FindStringSubmatchIndex(text)
	krw, krwOK := amountKrw(text)
	foreign := foreignRe.FindStringSubmatch(text)
	// A payment alert always says when. Adverts and notices quote won figures
	// ("최대 10,000원 할인") but almost never a date with a clock time.
	if when == nil || (!krwOK && foreign == nil) {
		return Alert{}, NotPayment
	}

	a := Alert{Issuer: issuerOf(issuer, text), Kind: KindApproval, Currency: "KRW"}
	if strings.Contains(text, "취소") {
		a.Kind = KindCancel
	}
	a.ApprovedAt = approvedAt(text, when, sentAt)
	if m := tailLabelRe.FindStringSubmatch(text); m != nil {
		a.CardTail = m[1]
	} else if m := tailParenRe.FindStringSubmatch(text); m != nil {
		a.CardTail = m[1]
	} else if m := tailWordRe.FindStringSubmatch(text); m != nil {
		a.CardTail = m[1]
	}
	if m := monthsRe.FindStringSubmatch(text); m != nil {
		a.Installment, _ = strconv.Atoi(m[1])
	}

	if foreign != nil {
		cur, num := foreign[1], foreign[2]
		if cur == "" {
			cur, num = foreign[4], foreign[3]
		}
		switch strings.ToUpper(cur) {
		case "USD", "US$", "$", "달러":
			a.Currency = "USD"
		default:
			// Only dollars can be estimated (see ingest's rate). A yen or euro
			// charge goes to the queue for its won figure, rather than in at 0.
			return a, Unreadable
		}
		a.ForeignAmount, _ = strconv.ParseFloat(strings.ReplaceAll(num, ",", ""), 64)
	}
	if krwOK {
		a.AmountKrw = krw
	}
	if a.AmountKrw <= 0 && a.ForeignAmount <= 0 {
		return a, Unreadable
	}

	a.Merchant = merchant(text, when)
	if a.Merchant == "" {
		return a, Unreadable
	}
	return a, Parsed
}

func issuerOf(chat, text string) string {
	if strings.Contains(chat, "카드") {
		return strings.TrimSpace(chat)
	}
	if m := issuerRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return strings.TrimSpace(chat)
}

// amountKrw takes the first won figure that is not labelled as something
// else. The running total ("누적 345,678원") usually comes after the charge,
// but not always, so position alone is not enough.
func amountKrw(text string) (int64, bool) {
	for _, line := range strings.Split(text, "\n") {
		for _, m := range krwRe.FindAllStringSubmatchIndex(line, -1) {
			before := line[:m[0]]
			if len([]rune(before)) > 10 {
				r := []rune(before)
				before = string(r[len(r)-10:])
			}
			skip := false
			for _, w := range notTheAmount {
				if strings.Contains(before, w) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			n, err := strconv.ParseInt(strings.ReplaceAll(line[m[2]:m[3]], ",", ""), 10, 64)
			if err == nil && n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}

// approvedAt resolves the alert's own clock time. It carries no year, so the
// year is the one it was sent in - except across New Year, where a 12/31
// charge alerted on 1/1 belongs to the year before.
func approvedAt(text string, when []int, sentAt time.Time) time.Time {
	loc := sentAt.Location()
	num := func(i int) int {
		if when[2*i] < 0 {
			return 0
		}
		n, _ := strconv.Atoi(text[when[2*i]:when[2*i+1]])
		return n
	}
	year, month, day, hour, minute := num(1), num(2), num(3), num(4), num(5)
	if year == 0 {
		year = sentAt.Year()
		if month > int(sentAt.Month())+1 {
			year--
		}
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 {
		return sentAt
	}
	t := time.Date(year, time.Month(month), day, hour, minute, 0, 0, loc)
	// A clock time a week away from when it was sent is a misread, not a
	// late alert.
	if d := t.Sub(sentAt); d > 7*24*time.Hour || d < -7*24*time.Hour {
		return sentAt
	}
	return t
}

func merchant(text string, when []int) string {
	lines := strings.Split(text, "\n")

	// 1. A labelled line says it outright.
	for _, line := range lines {
		if m := labelRe.FindStringSubmatch(debullet(line)); m != nil {
			if v := cleanMerchant(m[1]); v != "" {
				return v
			}
		}
	}

	// 2. Whatever follows the clock time on its own line: "10/06 12:34 스타벅스".
	lineStart := strings.LastIndex(text[:when[0]], "\n") + 1
	lineEnd := strings.Index(text[when[1]:], "\n")
	if lineEnd < 0 {
		lineEnd = len(text)
	} else {
		lineEnd += when[1]
	}
	if v := cleanMerchant(text[when[1]:lineEnd]); v != "" && !strings.ContainsAny(v[:1], "0123456789") {
		return v
	}
	_ = lineStart

	// 3. The one line that is none of the other parts.
	for i, line := range lines {
		line = debullet(line)
		if line == "" || i == 0 && (strings.HasPrefix(line, "[") || strings.Contains(line, "승인") || strings.Contains(line, "취소")) {
			continue
		}
		if maskedName.MatchString(line) || labelled.MatchString(line) ||
			krwRe.MatchString(line) || foreignRe.MatchString(line) || whenRe.MatchString(line) ||
			strings.HasSuffix(line, "님") {
			continue
		}
		if hasPrefix(line, notMerchantPrefix) || strings.HasPrefix(line, "[") {
			continue
		}
		if v := cleanMerchant(line); v != "" {
			return v
		}
	}
	return ""
}

func debullet(line string) string {
	return strings.TrimSpace(bulletRe.ReplaceAllString(line, ""))
}

func cleanMerchant(s string) string {
	s = strings.TrimSpace(s)
	for _, stop := range []string{"누적", "잔액", "한도", "사용가능"} {
		if i := strings.Index(s, stop); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(strings.Trim(s, ":：-·|"))
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
