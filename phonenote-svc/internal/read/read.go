// Package read decides what a phone notification says: rules for what
// needs no model (an ad, a login code, an app whose notifications are never
// anything), the model for the rest.
package read

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
)

// Apps the operator would know by name; the model is told the name too.
var Apps = map[string]string{
	"com.jawebs.baedal":            "배달의민족",
	"com.coupang.coupangeats":      "쿠팡이츠",
	"com.coupang.mobile.eats":      "쿠팡이츠",
	"com.yogiyo.yogiyoapp":         "요기요",
	"kr.co.ddangyo":                "땡겨요",
	"com.coupang.mobile":           "쿠팡",
	"com.coupang.coupang":          "쿠팡",
	"com.nhncorp.NaverMap":         "네이버 지도",
	"com.nhn.NaverSearch":          "네이버",
	"com.naver.pay":                "네이버페이",
	"com.kbstar.kbbank":            "KB국민은행",
	"com.kbstar.starbanking":       "KB스타뱅킹",
	"com.shinhan.sbanking":         "신한 SOL뱅크",
	"com.hanabank.smart.HanaNBank": "하나은행",
	"com.wooribank.smart.wbank":    "우리은행",
	"com.kakaobank.channel":        "카카오뱅크",
	"com.vivarepublica.toss":       "토스",
	"com.apple.Passbook":           "지갑(Apple Pay)",
	"com.cjlogistics.cjtrans":      "CJ대한통운",
	"kr.co.hanjin.hanjinexpress":   "한진택배",
	"com.epost.psf.sd":             "우체국",
	"com.coupang.mobile.wing":      "쿠팡",
}

// Name is how the operator would call the app.
func Name(app string) string {
	if n, ok := Apps[app]; ok {
		return n
	}
	return app
}

var (
	// A notification that says it is an ad: Korean law makes them say so.
	adRE = regexp.MustCompile(`^\s*[(\[]\s*광고\s*[)\]]`)
	// A login or payment code.
	codeRE = regexp.MustCompile(`(?i)(인증\s*번호|인증\s*코드|보안\s*코드|OTP|verification code|일회용)`)
)

// Rule reads what needs no model. ok false: the model should read it.
func Rule(n note.Note) (note.Reading, bool) {
	text := strings.TrimSpace(n.Text())
	switch {
	case text == "":
		return note.Reading{Kind: note.Other, By: "rule"}, true
	case adRE.MatchString(n.Title) || adRE.MatchString(n.Body):
		return note.Reading{Kind: note.Promotion, Summary: "광고", By: "rule"}, true
	case codeRE.MatchString(text):
		return note.Reading{Kind: note.Security, Summary: "인증 번호", By: "rule"}, true
	}
	return note.Reading{}, false
}

// Quiet says an app's recent notifications were all noise: reading the next
// one with the model would cost and find nothing. `usual` is newest first.
func Quiet(usual []string) bool {
	if len(usual) < 8 {
		return false
	}
	for _, k := range usual {
		if k != "noise" {
			return false
		}
	}
	return true
}

// Model is the language model that reads the rest.
type Model interface {
	Complete(ctx context.Context, system, user string) (string, error)
	Ready() bool
}

// Categories spending-svc files an item under.
var Categories = []string{"식비", "카페·간식", "편의점·마트", "교통", "쇼핑", "구독", "생활·통신", "의료·건강", "미용·뷰티", "운동", "문화·여가", "여행", "기타"}

const system = `너는 운영자의 휴대폰에 온 알림 하나를 읽고, 그것이 운영자의 생활에서 실제로 일어난 일인지 판단해 JSON 하나로만 답한다.

kind 는 다음 중 하나다:
- delivery_order: 배달 음식 주문(주문 접수, 조리 시작, 배달 출발, 배달 완료, 주문 취소)
- purchase: 결제·구매(카드 결제, 간편결제, 쇼핑몰 주문 완료)
- deposit: 돈이 들어옴(입금, 월급, 환급)
- withdrawal: 돈이 나감(이체, 출금, 자동이체, 공과금 납부)
- shipping: 택배·배송(발송, 배송 중, 도착 예정, 배송 완료)
- reservation: 예약(예약 확정·변경·취소, 곧 다가오는 예약)
- bill: 내야 할 돈(청구서, 납부 기한, 카드 대금)
- message: 사람이 보낸 메시지
- reminder: 운영자가 앞서 정해 둔 것을 앱이 알려 줌
- promotion: 광고, 쿠폰, 이벤트, 추천
- security: 로그인, 인증, 보안 경고
- other: 그 밖에(앱 업데이트, 일반 공지 등)

meaningful 은 운영자의 삶에서 일어난 일로 남길 가치가 있으면 true 다(주문·결제·입출금·배송·예약·청구는 보통 true, 광고·인증·앱 공지는 false).

읽어 낼 수 있는 것만 채우고, 알림에 없는 것은 비우거나 0 으로 둔다. 추측해서 지어내지 않는다.
- merchant: 가게·식당·쇼핑몰 이름(배달앱 이름이 아니라 실제 가게가 보이면 그 가게)
- items: 주문한 것들 [{"name": "...", "quantity": 1, "priceKrw": 0, "category": "..."}] - 알림에 메뉴·상품 이름이 보일 때만. category 는 ` + "식비, 카페·간식, 편의점·마트, 교통, 쇼핑, 구독, 생활·통신, 의료·건강, 미용·뷰티, 운동, 문화·여가, 여행, 기타" + ` 중 하나
- amountKrw: 금액(원). 외화면 0
- counterparty: 돈을 보낸·받은 사람이나 회사
- salary: 입금이 급여(월급, 상여)면 true
- stage: 주문·배송 단계 placed | accepted | shipped | arriving | delivered | cancelled 중 하나, 아니면 빈 값
- summary: 무슨 일인지 한국어 한 줄(가게·금액·단계를 담아서)

형식: {"kind": "...", "meaningful": true, "summary": "...", "merchant": "", "items": [], "amountKrw": 0, "counterparty": "", "salary": false, "stage": ""}`

// Reader reads a notification with rules first, the model after.
type Reader struct{ Model Model }

var ErrNoModel = errors.New("모델 키가 없어 알림을 해석하지 않습니다")

type reply struct {
	Kind       string `json:"kind"`
	Meaningful bool   `json:"meaningful"`
	Summary    string `json:"summary"`
	Merchant   string `json:"merchant"`
	Items      []struct {
		Name     string `json:"name"`
		Quantity int    `json:"quantity"`
		PriceKrw int64  `json:"priceKrw"`
		Category string `json:"category"`
	} `json:"items"`
	AmountKrw    int64  `json:"amountKrw"`
	Counterparty string `json:"counterparty"`
	Salary       bool   `json:"salary"`
	Stage        string `json:"stage"`
}

var kinds = map[string]bool{
	note.DeliveryOrder: true, note.Purchase: true, note.Deposit: true, note.Withdrawal: true, note.Shipping: true,
	note.Reservation: true, note.Bill: true, note.Message: true, note.Reminder: true, note.Promotion: true,
	note.Security: true, note.Other: true,
}

// Read asks the model. The answer is checked: an unknown kind is Other, an
// item without a name is dropped, a category not on the list is 기타.
func (r Reader) Read(ctx context.Context, n note.Note) (note.Reading, error) {
	if r.Model == nil || !r.Model.Ready() {
		return note.Reading{}, ErrNoModel
	}
	user := fmt.Sprintf("앱: %s (%s)\n시각: %s\n알림:\n%s", Name(n.App), n.App, n.At.Format("2006-01-02 15:04 (Mon)"), n.Text())
	out, err := r.Model.Complete(ctx, system, user)
	if err != nil {
		return note.Reading{}, err
	}
	var p reply
	if err := json.Unmarshal([]byte(firstObject(out)), &p); err != nil {
		return note.Reading{}, fmt.Errorf("해석을 읽지 못했습니다: %w", err)
	}
	if !kinds[p.Kind] {
		p.Kind = note.Other
	}
	reading := note.Reading{
		Kind: p.Kind, Meaningful: p.Meaningful, Summary: clip(p.Summary, 120), Merchant: clip(p.Merchant, 60),
		AmountKrw: max(p.AmountKrw, 0), Counterparty: clip(p.Counterparty, 60), Salary: p.Salary && p.Kind == note.Deposit,
		Stage: p.Stage, By: "model",
	}
	for _, it := range p.Items {
		name := clip(strings.TrimSpace(it.Name), 120)
		if name == "" || len(reading.Items) >= 30 {
			continue
		}
		cat := it.Category
		if !valid(cat) {
			cat = "기타"
		}
		reading.Items = append(reading.Items, note.Item{Name: name, Quantity: max(it.Quantity, 1), PriceKrw: max(it.PriceKrw, 0), Category: cat})
	}
	return reading, nil
}

func valid(c string) bool {
	for _, x := range Categories {
		if x == c {
			return true
		}
	}
	return false
}

// firstObject is the first {...} in the model's answer.
func firstObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

// Anthropic is the Messages API with a daily budget.
type Anthropic struct {
	Key     string
	ModelID string  // claude-haiku-5-5 when empty
	Budget  float64 // USD per day; 0: no limit
	HTTP    *http.Client
	Now     func() time.Time

	mu    sync.Mutex
	day   string
	spent float64
}

func (a *Anthropic) Ready() bool { return a.Key != "" }

var ErrBudget = errors.New("오늘 해석 예산을 다 썼습니다")

func (a *Anthropic) Complete(ctx context.Context, system, user string) (string, error) {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	a.mu.Lock()
	if d := now().Format("2006-01-02"); d != a.day {
		a.day, a.spent = d, 0
	}
	over := a.Budget > 0 && a.spent >= a.Budget
	a.mu.Unlock()
	if over {
		return "", ErrBudget
	}
	model := a.ModelID
	if model == "" {
		model = "claude-haiku-5-5"
	}
	body, _ := json.Marshal(map[string]any{
		"model": model, "max_tokens": 800, "system": system,
		"messages": []map[string]string{{"role": "user", "content": user}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.Key)
	req.Header.Set("anthropic-version", "2023-06-01")
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 || out.Error != nil {
		msg := resp.Status
		if out.Error != nil {
			msg = out.Error.Message
		}
		return "", fmt.Errorf("모델이 거절했습니다: %s", msg)
	}
	// Claude Haiku 5.5: $0.10 / $0.50 per million tokens.
	a.mu.Lock()
	a.spent += float64(out.Usage.In)*0.10/1e6 + float64(out.Usage.Out)*0.50/1e6
	a.mu.Unlock()
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return text.String(), nil
}
