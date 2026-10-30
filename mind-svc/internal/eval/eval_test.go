package eval

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

// Every scenario, with its script standing in for the model: the loop
// around the model must turn a correct reading into the right world model,
// intents, calendar and reminders.
func TestScenariosScripted(t *testing.T) {
	scs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var results []Result
	for _, sc := range scs {
		t.Run(sc.Name, func(t *testing.T) {
			r := Run(context.Background(), sc, Scripted(sc), seoul)
			for _, f := range r.Failures {
				t.Error(f)
			}
			if r.Scored > 0 {
				t.Logf("예측 %d개 · Brier %.3f", r.Scored, r.Brier)
			}
			if r.Cards > 0 {
				t.Logf("카드 %d장(하루 최대 %d장) · 승인 %d · 거절 %d", r.Cards, r.BusiestDay, r.Approved, r.Rejected)
			}
			results = append(results, r)
		})
	}
	t.Log(Summary(results))
	// The harness reads the score here and holds it to its baseline.
	if path := os.Getenv("JARVIS_EVAL_JSON"); path != "" {
		out, err := json.MarshalIndent(Scores(results), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The same scenarios with the real model, when a key is given:
// MIND_EVAL_LIVE=1 MIND_ANTHROPIC_API_KEY=... go test ./internal/eval -run Live -v
func TestScenariosLive(t *testing.T) {
	key := os.Getenv("MIND_ANTHROPIC_API_KEY")
	if os.Getenv("MIND_EVAL_LIVE") != "1" || key == "" {
		t.Skip("MIND_EVAL_LIVE=1 과 MIND_ANTHROPIC_API_KEY 가 있을 때만")
	}
	client := llm.NewAnthropic(key, os.Getenv("MIND_MODEL"), 1)
	results, err := RunAll(context.Background(), client, seoul)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		for _, f := range r.Failures {
			t.Errorf("%s: %s", r.Name, f)
		}
	}
	t.Log(Summary(results))
	t.Logf("비용 약 $%.4f", client.Spent())
}
