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
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The two tools. Each is an MCP server of its own with one tool of its name.
const (
	Look   = "look"
	Change = "change"
)

// Qualified is how the CLI refers to a board's tool.
func Qualified(name string) string { return "mcp__" + name + "__" + name }

// keep is what the CLI keeps of a tool's description: past about 2,000
// characters it is cut off, and what lay below the cut did not exist for the
// model. The rows below the checklists had been out of its sight since the
// boards were made - a goal.check came with no mark, and said it had recorded
// it (2026-11-02). So a board that does not fit says each row in short, and
// help says the rest.
const keep = 2000

// Help is what asks a board for one row's whole description.
const Help = "help"

// Board is one tool and what it can be asked for.
type Board struct {
	name, about string
	rows        []row
}

type row struct {
	name, says, args string
	// names is every argument, need those it cannot do without; short says
	// them in a line.
	names, need []string
	short       string
	run         func(ctx context.Context, args json.RawMessage) (string, error)
}

// New is a board with nothing on it yet.
func New(name, about string) *Board { return &Board{name: name, about: about} }

// Add puts one thing on the board. In's fields are its arguments: their json
// names and jsonschema text are what the model reads of them. What run returns
// is what the model is told; an error is told as a failure.
func Add[In any](b *Board, name, says string, run func(ctx context.Context, in In) (string, error)) {
	t := reflect.TypeFor[In]()
	r := row{name: name, says: says, args: argsOf(t)}
	r.names, r.need, r.short = namesOf(t)
	r.run = func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := r.check(raw); err != nil {
			return "", err
		}
		var in In
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", fmt.Errorf("%s 의 인자를 읽지 못했습니다: %w", name, err)
			}
		}
		return run(ctx, in)
	}
	b.rows = append(b.rows, r)
}

// check turns down what would otherwise be read as nothing: an argument the row
// does not have, or one it cannot do without. Left to run, a mark under another
// name was a mark not made, told as made. The refusal says what it takes, so the
// next call has it.
func (r row) check(raw json.RawMessage) error {
	if len(r.names) == 0 {
		return nil
	}
	got := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &got); err != nil {
			return fmt.Errorf("%s 의 인자를 읽지 못했습니다: %w", r.name, err)
		}
	}
	var unknown []string
	for k := range got {
		if !slices.Contains(r.names, k) {
			unknown = append(unknown, k)
		}
	}
	slices.Sort(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("%s 에 없는 인자입니다: %s. 쓸 수 있는 인자: %s", r.name, strings.Join(unknown, ", "), r.args)
	}
	var missing []string
	for _, k := range r.need {
		if v, ok := got[k]; !ok || string(v) == "null" || string(v) == `""` {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s 에 꼭 있어야 할 인자가 없습니다: %s. 쓸 수 있는 인자: %s", r.name, strings.Join(missing, ", "), r.args)
	}
	return nil
}

// namesOf is a struct's arguments: all of them, those it needs, and the two in
// a line (a star on what it needs).
func namesOf(t reflect.Type) (names, need []string, short string) {
	if t.Kind() != reflect.Struct {
		return nil, nil, ""
	}
	var marks []string
	for i := range t.NumField() {
		name, opts, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
		if strings.Contains(opts, "omitempty") {
			marks = append(marks, name)
			continue
		}
		need = append(need, name)
		marks = append(marks, name+"*")
	}
	return names, need, strings.Join(marks, ", ")
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
	if what == Help {
		return b.help(args)
	}
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

// help is one row whole, or the names of them all.
func (b *Board) help(args map[string]any) (string, error) {
	name, _ := args["name"].(string)
	for _, r := range b.rows {
		if r.name == name {
			return r.whole(), nil
		}
	}
	return "", fmt.Errorf("args 의 name 에 이름 하나를 주세요. 있는 것: %s", strings.Join(b.names(), ", "))
}

func (r row) whole() string {
	if r.args == "" {
		return fmt.Sprintf("- %s: %s", r.name, r.says)
	}
	return fmt.Sprintf("- %s: %s [args] %s", r.name, r.says, r.args)
}

// brief is a row in a line of at most n characters of what it says, then its
// arguments by name.
func (r row) brief(n int) string {
	says := r.says
	if i := strings.IndexAny(says, ".\n"); i > 0 {
		says = says[:i]
	}
	if cut := []rune(says); len(cut) > n {
		says = strings.TrimSpace(string(cut[:n])) + "…"
	}
	if r.short == "" {
		return fmt.Sprintf("- %s: %s", r.name, says)
	}
	return fmt.Sprintf("- %s: %s [%s]", r.name, says, r.short)
}

// Description is the tool as the model reads it: whole when it fits in what the
// CLI keeps, each row in short when it does not. Every row is there either way.
func (b *Board) Description() string {
	head := b.about + "\nwhat 에 아래 이름 하나를, args 에 그 인자를 넣어 부른다.\n"
	join := func(line func(row) string) string {
		var s strings.Builder
		s.WriteString(head)
		for _, r := range b.rows {
			s.WriteString("\n" + line(r))
		}
		return s.String()
	}
	if whole := join(row.whole); utf8.RuneCountInString(whole) <= keep {
		return whole
	}
	foot := "\n\n* 는 꼭 있어야 할 인자. 줄 하나의 자세한 설명과 인자는 what 에 help, args 에 {\"name\": 이름}."
	for _, n := range []int{60, 44, 30, 18} {
		if s := join(func(r row) string { return r.brief(n) }) + foot; utf8.RuneCountInString(s) <= keep {
			return s
		}
	}
	return join(func(r row) string { return "- " + r.name }) + foot
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
