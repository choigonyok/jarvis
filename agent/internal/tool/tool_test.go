package tool

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

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
