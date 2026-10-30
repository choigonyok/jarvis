// Package tool is the conversation's two general tools: look reads, change
// leaves what changes. Each takes what it acts on by name, from a list the
// modules fill, so a new lookup is a line on a board and not a tool of its own:
// every tool is described in every request, and a person has fifty things they
// might ask for but only ever asks for one.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The two tools. Each is an MCP server of its own with one tool of its name.
const (
	Look   = "look"
	Change = "change"
)

// Qualified is how the CLI refers to a board's tool.
func Qualified(name string) string { return "mcp__" + name + "__" + name }

// Board is one tool and what it can be asked for.
type Board struct {
	name, about string
	rows        []row
}

type row struct {
	name, says, args string
	run              func(ctx context.Context, args json.RawMessage) (string, error)
}

// New is a board with nothing on it yet.
func New(name, about string) *Board { return &Board{name: name, about: about} }

// Add puts one thing on the board. In's fields are its arguments: their json
// names and jsonschema text are what the model reads of them. What run returns
// is what the model is told; an error is told as a failure.
func Add[In any](b *Board, name, says string, run func(ctx context.Context, in In) (string, error)) {
	b.rows = append(b.rows, row{name: name, says: says, args: argsOf(reflect.TypeFor[In]()),
		run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in In
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", fmt.Errorf("%s 의 인자를 읽지 못했습니다: %w", name, err)
				}
			}
			return run(ctx, in)
		}})
}

// argsOf is a struct's fields as the model reads them.
func argsOf(t reflect.Type) string {
	if t.Kind() != reflect.Struct {
		return ""
	}
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		line := name
		if !strings.Contains(opts, "omitempty") {
			line += "(필수)"
		}
		if about := f.Tag.Get("jsonschema"); about != "" {
			line += ": " + about
		}
		out = append(out, line)
	}
	return strings.Join(out, "; ")
}

// Do runs one thing on the board: what the tool does with a call.
func (b *Board) Do(ctx context.Context, what string, args map[string]any) (string, error) {
	for _, r := range b.rows {
		if r.name != what {
			continue
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		return r.run(ctx, raw)
	}
	return "", fmt.Errorf("%s 은(는) %s 에 없습니다. 있는 것: %s", what, b.name, strings.Join(b.names(), ", "))
}

func (b *Board) names() []string {
	out := make([]string, len(b.rows))
	for i, r := range b.rows {
		out[i] = r.name
	}
	return out
}

// Input is the tool's one call.
type Input struct {
	What string         `json:"what" jsonschema:"목록의 이름"`
	Args map[string]any `json:"args,omitempty" jsonschema:"그것의 인자. 이름은 목록에 적힌 대로"`
}

// Description is the tool as the model reads it.
func (b *Board) Description() string {
	var s strings.Builder
	s.WriteString(b.about)
	s.WriteString("\nwhat 에 아래 이름 하나를, args 에 그 인자를 넣어 부른다.\n")
	for _, r := range b.rows {
		fmt.Fprintf(&s, "\n- %s: %s", r.name, r.says)
		if r.args != "" {
			fmt.Fprintf(&s, " [args] %s", r.args)
		}
	}
	return s.String()
}

// Handler is the board as an MCP server. It reads the board as it stands, so
// call it once every module has added its rows.
func (b *Board) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: b.name, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: b.name, Description: b.Description()},
		func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, any, error) {
			text, err := b.Do(ctx, in.What, in.Args)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
		})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
