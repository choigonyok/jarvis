package permission

import (
	"path/filepath"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
)

func TestModuleKind(t *testing.T) {
	cases := map[string]string{
		"mcp__calendar__create_event": "calendar.create_event",
		"mcp__calendar__list_events":  "calendar.list_events",
		"Bash":                        "",
		"mcp__broken":                 "",
	}
	for tool, want := range cases {
		got, ok := moduleKind(tool)
		if want == "" && ok {
			t.Fatalf("%s: 모듈 도구가 아닌데 매핑됐습니다: %s", tool, got)
		}
		if want != "" && got != want {
			t.Fatalf("%s: got %q, want %q", tool, got, want)
		}
	}
}

// The payoff of the registry: a module's own renderer writes the card, so the
// operator reads the change rather than the JSON that encodes it.
func TestModuleActionGetsTheModulesCard(t *testing.T) {
	store := calendar.NewStore(filepath.Join(t.TempDir(), "c.json"), bus.New())
	modules := module.NewRegistry()
	modules.Add(calendar.New(store))

	g := &Gate{modules: modules}
	_, card, err := g.describe("mcp__calendar__create_event",
		map[string]any{"date": "2026-09-25", "start": "19:00", "title": "합주"}, nil)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if card.Title != "일정을 추가합니다" {
		t.Fatalf("모듈 카드가 아닙니다: %+v", card)
	}
	if card.Body != "9월 25일 (금) 19:00  합주" {
		t.Fatalf("카드 본문: %q", card.Body)
	}
}

// A tool Claude Code runs itself has no module behind it; the card must still
// show the literal call rather than failing.
func TestUnknownToolFallsBackToGenericCard(t *testing.T) {
	g := &Gate{modules: module.NewRegistry()}
	act, card, err := g.describe("Bash", map[string]any{"command": "rm -rf ./cache"}, map[string]any{"command": "rm -rf ./cache"})
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if act.Kind != "claude.Bash" || card.Body != "rm -rf ./cache" {
		t.Fatalf("got %+v / %+v", act, card)
	}
}
