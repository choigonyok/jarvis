package tool

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type monthIn struct {
	Month string `json:"month,omitempty" jsonschema:"조회할 달"`
	Who   string `json:"who" jsonschema:"누구"`
}

func board() *Board {
	b := New(Look, "읽는다.")
	Add(b, "spending", "가계부", func(_ context.Context, in monthIn) (string, error) {
		return in.Who + " " + in.Month, nil
	})
	Add(b, "broken", "늘 실패", func(context.Context, struct{}) (string, error) {
		return "", errors.New("읽지 못했습니다")
	})
	return b
}

// What the model reads names each thing and its arguments, from the same
// struct the call is read into.
func TestTheBoardSaysWhatIsOnIt(t *testing.T) {
	d := board().Description()
	for _, want := range []string{"읽는다.", "- spending: 가계부", "month: 조회할 달", "who(필수): 누구", "- broken: 늘 실패"} {
		if !strings.Contains(d, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, d)
		}
	}
}

// Through MCP as the CLI calls it: a name off the list, a failure said as one,
// and a name that is not there says what is.
func TestTheToolDoesWhatItNames(t *testing.T) {
	srv := httptest.NewServer(board().Handler())
	defer srv.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	call := func(args map[string]any) (string, bool) {
		t.Helper()
		res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: Look, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res.Content[0].(*mcp.TextContent).Text, res.IsError
	}
	if got, bad := call(map[string]any{"what": "spending", "args": map[string]any{"who": "나", "month": "2026-10"}}); bad || got != "나 2026-10" {
		t.Fatalf("%q %v", got, bad)
	}
	if got, bad := call(map[string]any{"what": "broken"}); !bad || got != "읽지 못했습니다" {
		t.Fatalf("%q %v", got, bad)
	}
	if got, bad := call(map[string]any{"what": "없는것"}); !bad || !strings.Contains(got, "spending, broken") {
		t.Fatalf("%q %v", got, bad)
	}
	if got, bad := call(map[string]any{"what": "spending", "args": map[string]any{"month": 3}}); !bad || !strings.Contains(got, "인자") {
		t.Fatalf("%q %v", got, bad)
	}
}

func TestQualified(t *testing.T) {
	if Qualified(Look) != "mcp__look__look" {
		t.Fatal(Qualified(Look))
	}
}

type markIn struct {
	ID   string `json:"id" jsonschema:"목표 id"`
	Mark string `json:"mark" jsonschema:"done 또는 missed"`
	Day  string `json:"day,omitempty" jsonschema:"어느 날"`
}

// What the row does not have, or cannot do without, is refused with what it
// takes - it does not run on nothing and say it did.
func TestArgumentsAreHeldToTheRow(t *testing.T) {
	ran := 0
	b := New(Change, "남긴다.")
	Add(b, "goal.check", "기록한다.", func(context.Context, markIn) (string, error) {
		ran++
		return "기록했습니다", nil
	})
	ctx := context.Background()
	if _, err := b.Do(ctx, "goal.check", map[string]any{"id": "g", "status": "done"}); err == nil || !strings.Contains(err.Error(), "status") || !strings.Contains(err.Error(), "mark(필수)") {
		t.Fatalf("%v", err)
	}
	if _, err := b.Do(ctx, "goal.check", map[string]any{"id": "g", "mark": ""}); err == nil || !strings.Contains(err.Error(), "꼭 있어야 할 인자가 없습니다: mark") {
		t.Fatalf("%v", err)
	}
	if ran != 0 {
		t.Fatalf("돌면 안 됩니다: %d", ran)
	}
	if out, err := b.Do(ctx, "goal.check", map[string]any{"id": "g", "mark": "done", "day": "2026-11-02"}); err != nil || out != "기록했습니다" {
		t.Fatalf("%q %v", out, err)
	}
}

// A board longer than the CLI keeps says every row in short and every argument
// by name, inside the limit; help says a row whole.
func TestALongBoardFits(t *testing.T) {
	b := New(Change, "남긴다.")
	for i := range 24 {
		Add(b, fmt.Sprintf("row.%d", i), strings.Repeat("아주 길게 설명하는 줄입니다 ", 12)+". 그다음 문장", func(context.Context, markIn) (string, error) { return "", nil })
	}
	d := b.Description()
	if n := utf8.RuneCountInString(d); n > keep {
		t.Fatalf("%d자", n)
	}
	for _, want := range []string{"row.0", "row.23", "id*, mark*, day", "what 에 help"} {
		if !strings.Contains(d, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, d)
		}
	}
	whole, err := b.Do(context.Background(), Help, map[string]any{"name": "row.23"})
	if err != nil || !strings.Contains(whole, "그다음 문장") || !strings.Contains(whole, "mark(필수): done 또는 missed") {
		t.Fatalf("%q %v", whole, err)
	}
	if _, err := b.Do(context.Background(), Help, nil); err == nil || !strings.Contains(err.Error(), "row.23") {
		t.Fatalf("%v", err)
	}
}
