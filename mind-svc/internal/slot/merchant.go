package slot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Charge is a card payment, or a product of a split order, that the book's
// rules left unfiled.
type Charge struct {
	Kind       string    `json:"kind"` // tx | item
	ID         int64     `json:"id"`
	TxID       int64     `json:"txId"`
	Merchant   string    `json:"merchant"`
	Name       string    `json:"name,omitempty"`
	AmountKrw  int64     `json:"amountKrw"`
	ApprovedAt time.Time `json:"approvedAt"`
	Issuer     string    `json:"issuer"`
	CardTail   string    `json:"cardTail"`
}

// Ref is the charge's name in a question and in a topic: tx:12, item:34.
func (c Charge) Ref() string { return fmt.Sprintf("%s:%d", c.Kind, c.ID) }

// Key is what is classified: a merchant, or a product at one.
func (c Charge) Key() string {
	if c.Kind == "item" {
		return c.Merchant + " · " + c.Name
	}
	return c.Merchant
}

// Other is the book's catch-all: what is not an answer.
const Other = "기타"

// batch is how many charges go to the model in one request.
const batch = 8

const system = `당신은 가계부의 분류 담당입니다. 카드 결제 알림의 가맹점 이름(과 상품명)만 보고 그 결제가 어떤 분류인지 판단합니다.
- 가맹점 이름에서 업종이 누구에게나 분명하면(프랜차이즈 식당·카페·편의점·마트·주유소·약국·병원·영화관·통신사·택시 등) 그 분류와 높은 확신도(0.85 이상)를 답합니다. 지점명·법인 표기·띄어쓰기·철자 차이("써브웨이"="서브웨이")는 무시합니다.
- 이름만으로 모르는 곳, 여러 분류가 다 말이 되는 곳(복합몰, 생소한 상호, 개인 이름, 온라인 결제 대행)은 가장 그럴듯한 분류와 낮은 확신도를 답합니다.
- 답은 JSON 배열 하나뿐입니다. 다른 말, 코드 블록 표시는 쓰지 않습니다.`

func prompt(charges []Charge, categories []string) string {
	var b strings.Builder
	b.WriteString("고를 수 있는 분류: ")
	var choices []string
	for _, c := range categories {
		if c != Other {
			choices = append(choices, c)
		}
	}
	b.WriteString(strings.Join(choices, ", "))
	b.WriteString("\n\n결제:\n")
	for _, c := range charges {
		fmt.Fprintf(&b, "- id=%s 가맹점=%q 금액=%d원", c.Ref(), c.Merchant, c.AmountKrw)
		if c.Name != "" {
			fmt.Fprintf(&b, " 상품=%q", c.Name)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n각 id 에 대해 {\"id\":\"tx:12\",\"category\":\"식비\",\"confidence\":0.95} 꼴의 객체를 담은 JSON 배열만 출력하세요.")
	return b.String()
}

type judgement struct {
	ID         string  `json:"id"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
}

// parse reads the model's answer: a JSON array, possibly wrapped in a code
// fence or prose.
func parse(text string) []judgement {
	start, end := strings.Index(text, "["), strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil
	}
	var out []judgement
	if json.Unmarshal([]byte(text[start:end+1]), &out) != nil {
		return nil
	}
	return out
}

// Classifier says what a charge was for: what is already known, then the
// model, once, with the answer kept.
type Classifier struct {
	St  store.Store
	LLM llm.Client
	Now func() time.Time
}

// Classes reads the charges, by Key. A charge nobody can place has no entry:
// it is asked with the form empty.
func (c *Classifier) Classes(ctx context.Context, charges []Charge, categories []string) map[string]model.Class {
	out := map[string]model.Class{}
	var ask []Charge
	seen := map[string]bool{}
	for _, ch := range charges {
		key := ch.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		got, err := c.St.Class(ctx, MerchantCategory.Name, key)
		switch {
		case err == nil:
			out[key] = got
		case errors.Is(err, model.ErrNotFound):
			ask = append(ask, ch)
		}
	}
	if c.LLM == nil || !c.LLM.Ready() {
		return out
	}
	for len(ask) > 0 {
		n := min(batch, len(ask))
		part := ask[:n]
		ask = ask[n:]
		answer, err := c.LLM.Complete(llm.WithPurpose(ctx, llm.Judge), system, prompt(part, categories))
		if err != nil {
			return out // what is left is asked with the form empty, and read again next time
		}
		verdicts := map[string]judgement{}
		for _, j := range parse(answer) {
			verdicts[j.ID] = j
		}
		for _, ch := range part {
			j, ok := verdicts[ch.Ref()]
			category := strings.TrimSpace(j.Category)
			if !ok || category == "" || category == Other || !contains(categories, category) {
				continue
			}
			class := model.Class{Kind: MerchantCategory.Name, Key: ch.Key(), Value: category, P: j.Confidence, By: model.ClassByLLM, At: c.Now()}
			if err := c.St.PutClass(ctx, class); err != nil {
				continue
			}
			out[ch.Key()] = class
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
