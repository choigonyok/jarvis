// Package llm is the one call the interpreters make: a system prompt and a
// user message in, text out. Anthropic's Messages API in the stack; a script
// in tests, so the loop around the model can be checked without it.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Client interface {
	Complete(ctx context.Context, system, user string) (string, error)
	Ready() bool
	Model() string
	// Spent is today's cost in USD, as far as this process knows.
	Spent() float64
}

// Purposes a call is made for, for the cost report.
const (
	Interpret = "interpret"
	History   = "history" // interpreting the past, once
	Judge     = "judge"
	Reflect   = "reflect"
	Draft     = "draft"
)

type purposeKey struct{}

// WithPurpose tags the calls made with ctx: what they are for.
func WithPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, purposeKey{}, purpose)
}

// PurposeOf is what a call is for; interpreting, unless tagged.
func PurposeOf(ctx context.Context) string {
	if p, ok := ctx.Value(purposeKey{}).(string); ok && p != "" {
		return p
	}
	return Interpret
}

// Use is one call's cost, for whoever keeps the books.
type Use struct {
	Purpose string
	In, Out int
	USD     float64
	At      time.Time
}

var ErrBudget = errors.New("오늘 LLM 예산을 다 썼습니다")
var ErrNoKey = errors.New("LLM 키가 없습니다")

// ErrNoAnswer is a call that ended without an answer to read: the model
// thought until max_tokens and wrote nothing, or declined (refusal). The
// same prompt tends to end the same way, so a reader asks about less.
var ErrNoAnswer = errors.New("모델이 답을 쓰지 않았습니다")

// maxTokens is room for the model's thinking and its answer together:
// Haiku 5.5 thinks before it answers unless told not to, and the thinking
// counts against max_tokens. At 2000 a long batch could think the whole of
// it away and answer nothing - a 40-message group-chat batch thought about
// 1,500 tokens for a 150-token answer, and the reads that failed ran out
// (2026-10-22, "JSON 이 없습니다" on one busy room for hours). Only what is
// used is paid for.
const maxTokens = 8000

// Anthropic calls the Messages API. Haiku 5.5 is the default: interpreting a
// conversation is reading, not reasoning, and it is run on every one.
type Anthropic struct {
	key      string
	model    string
	url      string
	budget   float64 // USD per day; 0 = no limit
	inPrice  float64 // USD per input token
	outPrice float64
	http     *http.Client
	now      func() time.Time

	mu    sync.Mutex
	day   string
	spent float64

	// OnUse hears every call's cost (the store keeps it).
	OnUse func(Use)
}

// Seed starts today's spend from what was spent before a restart, so the
// daily budget holds across one.
func (a *Anthropic) Seed(spent float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.day, a.spent = a.now().Format("2006-01-02"), spent
}

func NewAnthropic(key, model string, dailyUSD float64) *Anthropic {
	if model == "" {
		model = "claude-haiku-5-5"
	}
	return &Anthropic{
		key: key, model: model, url: "https://api.anthropic.com/v1/messages", budget: dailyUSD,
		// Claude Haiku 5.5: $0.10 / $0.50 per million tokens.
		inPrice: 0.10 / 1e6, outPrice: 0.50 / 1e6,
		http: &http.Client{Timeout: 90 * time.Second}, now: time.Now,
	}
}

func (a *Anthropic) Ready() bool   { return a.key != "" }
func (a *Anthropic) Model() string { return a.model }

func (a *Anthropic) Spent() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.day != a.now().Format("2006-01-02") {
		return 0
	}
	return a.spent
}

func (a *Anthropic) Complete(ctx context.Context, system, user string) (string, error) {
	if a.key == "" {
		return "", ErrNoKey
	}
	if a.budget > 0 && a.Spent() >= a.budget {
		return "", ErrBudget
	}
	body, _ := json.Marshal(map[string]any{
		"model":      a.model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := a.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("LLM 응답을 읽지 못했습니다 (%d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 300 || out.Error != nil {
		msg := ""
		if out.Error != nil {
			msg = out.Error.Message
		}
		return "", fmt.Errorf("LLM 이 거절했습니다 (%d): %s", resp.StatusCode, msg)
	}
	cost := float64(out.Usage.In)*a.inPrice + float64(out.Usage.Out)*a.outPrice
	a.add(cost)
	if a.OnUse != nil {
		a.OnUse(Use{Purpose: PurposeOf(ctx), In: out.Usage.In, Out: out.Usage.Out, USD: cost, At: a.now()})
	}
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	if out.StopReason == "refusal" || strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("%w (멈춘 이유: %s)", ErrNoAnswer, out.StopReason)
	}
	return text.String(), nil
}

func (a *Anthropic) add(usd float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.now().Format("2006-01-02"); d != a.day {
		a.day, a.spent = d, 0
	}
	a.spent += usd
}

// Script answers from a function of the prompt - the model's part in a test,
// written down. It records every prompt it was given.
type Script struct {
	mu      sync.Mutex
	Answer  func(system, user string) (string, error)
	Prompts []string
}

func (s *Script) Ready() bool    { return s.Answer != nil }
func (s *Script) Model() string  { return "script" }
func (s *Script) Spent() float64 { return 0 }
func (s *Script) Calls() int     { s.mu.Lock(); defer s.mu.Unlock(); return len(s.Prompts) }
func (s *Script) Complete(_ context.Context, system, user string) (string, error) {
	s.mu.Lock()
	s.Prompts = append(s.Prompts, user)
	s.mu.Unlock()
	if s.Answer == nil {
		return "", ErrNoKey
	}
	return s.Answer(system, user)
}
