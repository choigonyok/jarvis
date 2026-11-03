package convo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// example is what satisfies an expected argument: its first alternative, its
// date words turned into dates.
func example(want string, now time.Time) string {
	first, _, _ := strings.Cut(want, "|")
	return resolve(strings.TrimSpace(first), now)
}

// streamOf is a CLI's stream that makes these calls and then says this.
func streamOf(calls []Call, says string, now time.Time) string {
	lines := []string{`{"type":"system","subtype":"init","session_id":"s-convo"}`}
	for i, c := range calls {
		if c.Tool == "*" {
			continue
		}
		name, input := "mcp__stub__"+c.Tool, map[string]any{}
		args := map[string]any{}
		for k, v := range c.Args {
			if k == "*" {
				k = "note"
			}
			args[k] = example(v, now)
		}
		switch {
		case c.Tool == "look" || c.Tool == "change":
			name = "mcp__" + c.Tool + "__" + c.Tool
			input["what"], input["args"] = c.What, args
		case !strings.Contains(c.Tool, "_") && c.Tool[0] >= 'A' && c.Tool[0] <= 'Z':
			name, input = c.Tool, args // the CLI's own tools: Read, WebSearch
		default:
			input = args
		}
		block, _ := json.Marshal(map[string]any{"type": "tool_use", "id": fmt.Sprintf("t%d", i), "name": name, "input": input})
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","message":{"id":"m%d","content":[%s]}}`, i, block))
	}
	if says == "" {
		says = "네."
	}
	text, _ := json.Marshal(map[string]any{"type": "text", "text": says})
	lines = append(lines, fmt.Sprintf(`{"type":"assistant","message":{"id":"mz","content":[%s]}}`, text), `{"type":"result","result":"ok"}`)
	return strings.Join(lines, "\n") + "\n"
}

// scripted is a CLI that plays a stream back, whatever it is asked, and waits
// for its input to close as the real one does.
func scripted(t *testing.T, stream string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stream.jsonl"), []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nIFS= read -r line\ncat '" + filepath.Join(dir, "stream.jsonl") + "'\ncat > /dev/null\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// passing is the run each scenario asks for: the calls it expects and the
// words it wants said.
func passing(sc Scenario, now time.Time) string {
	calls := slices.Clone(sc.Expect.Calls)
	if len(sc.Expect.AnyOf) > 0 {
		calls = append(calls, sc.Expect.AnyOf[0])
	}
	var says []string
	for _, s := range sc.Expect.Says {
		says = append(says, example(s, now))
	}
	return streamOf(calls, strings.Join(says, " "), now)
}

func load(t *testing.T) []Scenario {
	t.Helper()
	scs, err := Load("scenarios")
	if err != nil {
		t.Fatal(err)
	}
	if len(scs) < 20 {
		t.Fatalf("시나리오가 %d개뿐입니다", len(scs))
	}
	return scs
}

// Every scenario can be passed and graded through the whole harness - the
// agent's configuration, its boards and servers, the gate, the runner and
// its stream - with the model played back: what each expects is coherent,
// and a run that does what it must not, or not what it must, fails.
func TestEveryScenarioCanPassAndFail(t *testing.T) {
	// The played-back CLI needs no subscription; the configuration still asks
	// for the token the real one would.
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "scripted")
	skills, _ := filepath.Abs("../../../workspace/.claude/skills")
	ctx := context.Background()
	for _, sc := range load(t) {
		t.Run(sc.File, func(t *testing.T) {
			now := time.Now()
			out := Run(ctx, Options{Skills: skills, Claude: scripted(t, passing(sc, now)), Timeout: 20 * time.Second}, sc)
			if !out.Pass {
				t.Fatalf("통과해야 합니다: %v\n%s", out.Failures, out.Answer)
			}
			if out.TotalMS < 0 {
				t.Fatalf("턴의 시간을 읽어야 합니다: %+v", out)
			}
			var bad []Call
			for _, c := range sc.Expect.Never {
				if c.Tool != "*" {
					bad = append(bad, c)
				}
			}
			switch {
			case len(bad) > 0:
				wrong := scripted(t, streamOf(append(slices.Clone(sc.Expect.Calls), bad[0]), "", now))
				if out := Run(ctx, Options{Skills: skills, Claude: wrong, Timeout: 20 * time.Second}, sc); out.Pass {
					t.Fatalf("하면 안 되는 것을 하면 실패해야 합니다: %s", bad[0])
				}
			case len(sc.Expect.Calls) > 0 || len(sc.Expect.AnyOf) > 0:
				nothing := scripted(t, streamOf(nil, "", now))
				if out := Run(ctx, Options{Skills: skills, Claude: nothing, Timeout: 20 * time.Second}, sc); out.Pass {
					t.Fatal("해야 할 것을 하지 않으면 실패해야 합니다")
				}
			}
		})
	}
}

// The tests put up what the agent puts up: a module with tools for the
// conversation that wire does not add is one no scenario sees.
func TestEveryModuleIsWired(t *testing.T) {
	src, err := os.ReadFile("wire.go")
	if err != nil {
		t.Fatal(err)
	}
	offers := regexp.MustCompile(`func \(m \*Module\) (Look|Change|Handler)\(`)
	dirs, err := os.ReadDir("../module")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join("../module", d.Name(), "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			body, _ := os.ReadFile(f)
			if offers.Match(body) && !strings.Contains(string(src), `"github.com/choigonyok/jarvis/agent/internal/module/`+d.Name()+`"`) {
				t.Errorf("internal/module/%s 는 대화에 도구를 내놓는데 wire.go 가 세우지 않습니다", d.Name())
			}
		}
	}
}

// A date word in an expectation is the date it names, from when the run was.
func TestDateWords(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	wed := time.Date(2026, 11, 4, 21, 0, 0, 0, seoul) // a Wednesday
	for want, got := range map[string]string{
		"@today": "2026-11-04", "@tomorrow": "2026-11-05", "@yesterday": "2026-11-03",
		"@next:토": "2026-11-07", "@next:수": "2026-11-11", "@nextweek:토": "2026-11-14", "@nextweek:월": "2026-11-09",
	} {
		if r := resolve(want, wed); r != got {
			t.Errorf("%s = %s, want %s", want, r, got)
		}
	}
}
