// Package words has every card the agent raises worded by mind's writer
// (mind-svc internal/words) before anyone sees it, and checks what comes back
// against the card the code raised: the words are the model's; what the card
// is about (its facts), what may be filled in (its fields) and what runs (its
// action) are not.
//
// A card whose words do not hold up goes up in the code's words, and so does
// one whose words do not come in time: words are never what a card waits on
// for long, and never a reason it does not go up.
package words

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	mindmod "github.com/choigonyok/jarvis/agent/internal/module/mind"
)

// Writer asks mind-svc for a card's words (POST /api/cards/words).
type Writer struct {
	URL   string
	Token string
	// Wait is as long as a card waits for its words.
	Wait time.Duration
	HTTP *http.Client
	Log  *slog.Logger
}

// Ask is a card as the code raised it, as mind's writer reads it.
type Ask struct {
	Kind        string          `json:"kind"`
	Origin      string          `json:"origin"`
	Title       string          `json:"title"`
	Body        string          `json:"body,omitempty"`
	Consequence string          `json:"consequence,omitempty"`
	Facts       []action.Fact   `json:"facts,omitempty"`
	Fields      []askField      `json:"fields,omitempty"`
	Photos      int             `json:"photos,omitempty"`
	Choices     []WrittenChoice `json:"choices,omitempty"`
}

type askField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Value    string   `json:"value,omitempty"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	Private  bool     `json:"private,omitempty"`
}

// Words is the card in the model's words, as mind's writer answers.
type Words struct {
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Consequence string          `json:"consequence"`
	Fields      []WrittenField  `json:"fields"`
	Choices     []WrittenChoice `json:"choices"`
}

// WrittenField is a field's label and hint in the model's words.
type WrittenField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// WrittenChoice is an answer button in the model's words.
type WrittenChoice struct {
	Label    string `json:"label"`
	Decision string `json:"decision"`
	Tone     string `json:"tone"`
	Why      bool   `json:"why"`
	Values   []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"values"`
}

// Write words a card (proposal.Writer). A card already in a model's words (a
// job's question) and the literal call of a tool the CLI runs itself are
// left as they are: the one was written by the model that asks, the other is
// read for what it says to the letter.
func (w *Writer) Write(ctx context.Context, p proposal.Proposal) action.Card {
	code := p.Card
	if w == nil || w.URL == "" || code.Writer != "" || strings.HasPrefix(p.Action.Kind, "claude.") {
		return code
	}
	wait := w.Wait
	if wait <= 0 {
		wait = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	started := time.Now()
	got, err := w.ask(ctx, AskOf(p))
	if err != nil {
		w.log().Warn("카드 문구를 받지 못해 코드의 문구로 올립니다", "kind", p.Action.Kind, "err", err, "took", time.Since(started).Round(time.Millisecond))
		return code
	}
	card, why := Merge(code, got)
	if why != "" {
		w.log().Warn("받은 카드 문구를 쓰지 않습니다", "kind", p.Action.Kind, "why", why)
		return code
	}
	w.log().Info("카드 문구를 받았습니다", "kind", p.Action.Kind, "took", time.Since(started).Round(time.Millisecond), "choices", len(card.Choices))
	return card
}

func (w *Writer) log() *slog.Logger {
	if w.Log == nil {
		return slog.Default()
	}
	return w.Log
}

// AskOf is a card as mind's writer is asked about it: what it runs - a mind
// card by the intent it stands for - who raised it, and the card, with no
// private field's value.
func AskOf(p proposal.Proposal) Ask {
	c := p.Card
	a := Ask{Kind: mindmod.KindOf(p.Action), Origin: p.Origin, Title: c.Title, Body: c.Body, Consequence: c.Consequence,
		Facts: c.Facts, Photos: len(c.Images)}
	for _, f := range c.Fields {
		af := askField{Key: f.Key, Label: f.Label, Kind: f.Kind, Value: f.Value, Options: f.Options, Required: f.Required, Hint: f.Hint, Private: f.IsPrivate()}
		if af.Private {
			af.Value = ""
		}
		a.Fields = append(a.Fields, af)
	}
	for _, ch := range c.Choices {
		wc := WrittenChoice{Label: ch.Label, Decision: ch.Decision, Tone: ch.Tone, Why: ch.Why}
		a.Choices = append(a.Choices, wc)
	}
	return a
}

func (w *Writer) ask(ctx context.Context, a Ask) (Words, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return Words{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(w.URL, "/")+"/api/cards/words", bytes.NewReader(raw))
	if err != nil {
		return Words{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if w.Token != "" {
		req.Header.Set("Authorization", "Bearer "+w.Token)
	}
	client := w.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Words{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return Words{}, fmt.Errorf("mind %d: %s", resp.StatusCode, e.Error)
	}
	var out Words
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Words{}, err
	}
	return out, nil
}

// How long the words may be: a title read off a phone's notice, a body of a
// few lines, a button's label (action.Choice), a field's label and hint.
const (
	maxTitle       = 60
	maxBody        = 600
	maxConsequence = 240
	maxLabel       = 30
	maxHint        = 160
)

// irreversible is what a card that cannot be taken back says, and its words
// must say too.
const irreversible = "되돌릴 수 없"

// secret is what a card's words never speak of unless the code's card does
// already: a password comes only through a vault card's own secret field,
// never as words a page or a message put in a model's mouth.
var secret = regexp.MustCompile(`(?i)비밀번호|인증번호|카드번호|보안코드|주민등록번호|주민번호|계좌번호|핀번호|\b(?:CVC|CVV|OTP|PIN)\b`)

// link is an address in words: a link no card's words may add.
var link = regexp.MustCompile(`(?i)(https?://|www\.)[^\s)]+`)

// Merge is the code's card in the model's words, or why the words cannot be
// used. The facts, the fields' keys and kinds, the photos and the action stay
// the code's; the title must say every lead fact as it is; nothing may ask for
// a secret or add a link the code's card does not have; and a card that
// cannot be taken back keeps saying so. Choices that do not hold up are left
// out - the card is 승인 and 반려 then - and the code's own choices are only
// relabelled.
func Merge(code action.Card, w Words) (action.Card, string) {
	said := codeText(code)
	title := strings.TrimSpace(w.Title)
	body := strings.TrimSpace(w.Body)
	consequence := strings.TrimSpace(w.Consequence)
	switch {
	case title == "":
		return code, "제목이 없습니다"
	case utf8.RuneCountInString(title) > maxTitle:
		return code, "제목이 깁니다"
	case utf8.RuneCountInString(body) > maxBody:
		return code, "본문이 깁니다"
	}
	for _, f := range code.Facts {
		if f.Lead && !strings.Contains(title, f.Value) {
			return code, "제목에 " + f.Label + "(" + f.Value + ")이 그대로 없습니다"
		}
	}
	written := []string{title, body, consequence}
	for _, f := range w.Fields {
		written = append(written, f.Label, f.Hint)
	}
	for _, c := range w.Choices {
		written = append(written, c.Label)
	}
	if why := unsaid(said, written...); why != "" {
		return code, why
	}

	card := code
	card.Title, card.Body, card.Writer = title, body, action.WriterModel
	switch {
	case consequence == "" || utf8.RuneCountInString(consequence) > maxConsequence:
	case strings.Contains(code.Consequence, irreversible) && !strings.Contains(consequence, irreversible):
	default:
		card.Consequence = consequence
	}
	card.Fields = relabel(code.Fields, w.Fields)
	card.Choices = choicesOf(code, card.Fields, w.Choices)
	return card, ""
}

// unsaid says what in the written words the code's card did not say and the
// words may not add: a link, or a secret asked for.
func unsaid(said string, written ...string) string {
	for _, text := range written {
		for _, l := range link.FindAllString(text, -1) {
			if !strings.Contains(said, l) {
				return "카드에 없던 주소를 씁니다: " + l
			}
		}
		for _, s := range secret.FindAllString(text, -1) {
			if !strings.Contains(strings.ToUpper(said), strings.ToUpper(s)) {
				return "카드에 없던 비밀(" + s + ")을 말합니다"
			}
		}
	}
	return ""
}

// codeText is everything the code's card says, for what the words may repeat.
func codeText(c action.Card) string {
	parts := []string{c.Title, c.Body, c.Consequence}
	for _, f := range c.Facts {
		parts = append(parts, f.Label, f.Value)
	}
	for _, f := range c.Fields {
		parts = append(parts, f.Label, f.Hint)
	}
	return strings.Join(parts, "\n")
}

// relabel is the code's fields with the labels and hints the words gave them,
// by key: a field is never added, dropped or made another kind.
func relabel(fields []action.Field, words []WrittenField) []action.Field {
	if len(fields) == 0 {
		return fields
	}
	byKey := map[string]WrittenField{}
	for _, w := range words {
		byKey[w.Key] = w
	}
	out := append([]action.Field(nil), fields...)
	for i, f := range out {
		w, ok := byKey[f.Key]
		if !ok {
			continue
		}
		if l := strings.TrimSpace(w.Label); l != "" && utf8.RuneCountInString(l) <= maxLabel {
			out[i].Label = l
		}
		if h := strings.TrimSpace(w.Hint); h != "" && utf8.RuneCountInString(h) <= maxHint {
			out[i].Hint = h
		}
	}
	return out
}

// choicesOf are the card's answers in the words. The code's own (예 and
// 아니요) keep what each decides and only take new labels, and only when the
// words keep them all in order; otherwise the words' answers, when they hold
// up (action.Card.CheckChoices) - or none, and the card is 승인 and 반려.
func choicesOf(code action.Card, fields []action.Field, words []WrittenChoice) []action.Choice {
	if len(code.Choices) > 0 {
		if len(words) != len(code.Choices) {
			return code.Choices
		}
		out := append([]action.Choice(nil), code.Choices...)
		for i, w := range words {
			if w.Decision != out[i].Decision {
				return code.Choices
			}
			out[i].Label = strings.TrimSpace(w.Label)
			if w.Tone != "" {
				out[i].Tone = w.Tone
			}
		}
		if (action.Card{Fields: fields, Choices: out}).CheckChoices() != nil {
			return code.Choices
		}
		return out
	}
	var out []action.Choice
	for i, w := range words {
		ch := action.Choice{ID: fmt.Sprintf("c%d", i+1), Label: strings.TrimSpace(w.Label), Decision: w.Decision, Tone: w.Tone,
			Why: w.Why && w.Decision == action.ChoiceReject}
		for _, v := range w.Values {
			if ch.Values == nil {
				ch.Values = map[string]string{}
			}
			ch.Values[v.Key] = v.Value
		}
		out = append(out, ch)
	}
	if len(out) == 0 || (action.Card{Fields: fields, Choices: out}).CheckChoices() != nil {
		return nil
	}
	return out
}
