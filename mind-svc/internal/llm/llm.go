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

type searchKey struct{}

// WithSearch lets the calls made with ctx search the web, at most n times
// each - the model decides whether it needs to (where a company's office is,
// say). A script only notes it.
func WithSearch(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, searchKey{}, n)
}

// SearchOf is how many searches the calls made with ctx may make.
func SearchOf(ctx context.Context) int {
	n, _ := ctx.Value(searchKey{}).(int)
	return n
}

// Use is one call's cost, for whoever keeps the books.
type Use struct {
	Purpose  string
	In, Out  int
	Searches int // web searches, priced into USD
	USD      float64
	At       time.Time
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

// searchPrice is one web search: $10 per thousand. Its results are read as
// input tokens besides - about 15,000 for one search.
const searchPrice = 0.01

// webSearch is the server-side search tool. Haiku 5.5 takes the basic
// version; the 2026-02-09 one (dynamic filtering) answers its queries with
// invalid_tool_input there (2026-10-26).
const webSearch = "web_search_20250305"

// maxResumes bounds how often a turn the server paused (pause_turn, its
// tool loop at its limit) is sent back to go on.
const maxResumes = 3

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
	params := map[string]any{"model": a.model, "max_tokens": maxTokens, "system": system}
	if n := SearchOf(ctx); n > 0 {
		params["tools"] = []map[string]any{{"type": webSearch, "name": "web_search", "max_uses": n}}
	}
	// A turn the server paused goes back as it is, and goes on from there.
	var blocks []json.RawMessage
	var stop string
	for resumed := 0; ; resumed++ {
		messages := []map[string]any{{"role": "user", "content": user}}
		if len(blocks) > 0 {
			messages = append(messages, map[string]any{"role": "assistant", "content": blocks})
		}
		params["messages"] = messages
		content, reason, err := a.call(ctx, params)
		if err != nil {
			return "", err
		}
		blocks, stop = append(blocks, content...), reason
		if stop != "pause_turn" || resumed == maxResumes {
			break
		}
	}
	text := answer(blocks)
	if stop == "refusal" || stop == "pause_turn" || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%w (멈춘 이유: %s)", ErrNoAnswer, stop)
	}
	return text, nil
}

// answer is the text written after the last search result: what came before
// it was on the way there ("찾아볼게요"), and a cited answer comes in several
// text blocks, to be read together.
func answer(blocks []json.RawMessage) string {
	var text strings.Builder
	for _, raw := range blocks {
		var b struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		switch {
		case strings.HasSuffix(b.Type, "_tool_result"):
			text.Reset()
		case b.Type == "text":
			text.WriteString(b.Text)
		}
	}
	return text.String()
}

// call is one request: its content blocks as they came, and why it stopped.
func (a *Anthropic) call(ctx context.Context, params map[string]any) ([]json.RawMessage, string, error) {
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	var out struct {
		Content    []json.RawMessage `json:"content"`
		StopReason string            `json:"stop_reason"`
		Usage      struct {
			In     int `json:"input_tokens"`
			Out    int `json:"output_tokens"`
			Server struct {
				Searches int `json:"web_search_requests"`
			} `json:"server_tool_use"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", fmt.Errorf("LLM 응답을 읽지 못했습니다 (%d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 300 || out.Error != nil {
		msg := ""
		if out.Error != nil {
			msg = out.Error.Message
		}
		return nil, "", fmt.Errorf("LLM 이 거절했습니다 (%d): %s", resp.StatusCode, msg)
	}
	u := out.Usage
	cost := float64(u.In)*a.inPrice + float64(u.Out)*a.outPrice + float64(u.Server.Searches)*searchPrice
	a.add(cost)
	if a.OnUse != nil {
		a.OnUse(Use{Purpose: PurposeOf(ctx), In: u.In, Out: u.Out, Searches: u.Server.Searches, USD: cost, At: a.now()})
	}
	return out.Content, out.StopReason, nil
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
	// Searches is how many searches each call was let make.
	Searches []int
}

func (s *Script) Ready() bool    { return s.Answer != nil }
func (s *Script) Model() string  { return "script" }
func (s *Script) Spent() float64 { return 0 }
func (s *Script) Calls() int     { s.mu.Lock(); defer s.mu.Unlock(); return len(s.Prompts) }
func (s *Script) Complete(ctx context.Context, system, user string) (string, error) {
	s.mu.Lock()
	s.Prompts = append(s.Prompts, user)
	s.Searches = append(s.Searches, SearchOf(ctx))
	s.mu.Unlock()
	if s.Answer == nil {
		return "", ErrNoKey
	}
	return s.Answer(system, user)
}
