package convo

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// The tests put up what the agent puts up: the harness is put together by
// the agent's own assembly (internal/assemble), and builds nothing of the
// agent beside it - no module, board, gate, store or runner of its own. The
// jobs module is named only for the shared browser's server name. Nor does it
// rewire what the assembly returns: a setter on it (Runner.OnTool,
// Proposals.OnDecide), called or taken as a value, or a field written over
// would swap or add to the agent's own wiring. The assembly goes nowhere the
// check cannot follow: only to a function of this package or a function
// literal, whose parameter is then held too, or to a server that serves it.
// A hook the tests need is a field of assemble.Deps.
func TestTheHarnessIsTheAgentsAssembly(t *testing.T) {
	const agent = "github.com/choigonyok/jarvis/agent/internal/"
	builds := []string{"module/", "claudecode", "adapter/", "core/", "tool", "suggest", "httpapi"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	assembled := false
	fset := token.NewFileSet()
	asts := map[string]*ast.File{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		asts[f] = parsed
	}
	funcs := packageFuncs(asts)
	held := assemblyNames(asts, funcs)
	for _, f := range slices.Sorted(maps.Keys(asts)) {
		parsed := asts[f]
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == agent+"module/jobs" {
				continue
			}
			for _, b := range builds {
				if strings.HasPrefix(path, agent+b) {
					t.Errorf("%s 가 %s 를 가져옵니다 - 에이전트를 이루는 것은 assemble 만 세웁니다", f, strings.TrimPrefix(path, agent))
				}
			}
		}
		for _, decl := range parsed.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			in := held[fn]
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					if params(n.Fun, funcs) != nil || serves(n.Fun) || measures(n.Fun) {
						break
					}
					for _, arg := range n.Args {
						if assembly(arg, in) {
							t.Errorf("%s:%d: 조립한 에이전트를 시험이 따라가지 못하는 곳에 넘깁니다. 하네스에 필요한 것은 assemble.Deps 로 넘깁니다", f, fset.Position(arg.Pos()).Line)
						}
					}
				case *ast.AssignStmt:
					if n.Tok == token.DEFINE {
						break
					}
					for _, lhs := range n.Lhs {
						switch lhs.(type) {
						case *ast.SelectorExpr, *ast.IndexExpr:
							if in[rootName(lhs)] {
								t.Errorf("%s:%d: 조립한 에이전트의 것을 덮어씁니다. 하네스에 필요한 것은 assemble.Deps 로 넘깁니다", f, fset.Position(n.Pos()).Line)
							}
						}
					}
				case *ast.SelectorExpr:
					if setter.MatchString(n.Sel.Name) && in[rootName(n.X)] {
						t.Errorf("%s:%d: %s - 조립한 에이전트의 연결을 바꿉니다. 하네스에 필요한 것은 assemble.Deps 로 넘깁니다", f, fset.Position(n.Pos()).Line, n.Sel.Name)
					}
					if x, ok := n.X.(*ast.Ident); ok {
						switch {
						case x.Name == "assemble" && n.Sel.Name == "Assemble":
							assembled = true
						case x.Name == "jobs" && n.Sel.Name != "BrowserServer":
							t.Errorf("%s: jobs.%s - 작업 모듈은 assemble 이 세웁니다", f, n.Sel.Name)
						}
					}
				}
				return true
			})
		}
	}
	if !assembled {
		t.Fatal("하네스는 에이전트를 assemble.Assemble 로 세워야 합니다")
	}
}

// setter is a method that wires something in: an On or Set hook.
var setter = regexp.MustCompile(`^(On|Set)[A-Z]`)

// assemblyNames is the names the harness holds the assembly, or a part of
// it, under, in each function: what assemble.Assemble or wire returned, what
// is read off one of those (agent.Runner, agent.Boards[path]) or copied from
// one (a := *agent) - assigned or declared with var - and the parameter a
// function of this package, or a function literal, takes one in as. A name
// is held in its own function only: another function's h is another h.
func assemblyNames(files map[string]*ast.File, funcs map[string]*ast.FuncDecl) map[*ast.FuncDecl]map[string]bool {
	held := map[*ast.FuncDecl]map[string]bool{}
	grew := true
	hold := func(fn *ast.FuncDecl, name string) {
		if name == "" || name == "_" || held[fn][name] {
			return
		}
		if held[fn] == nil {
			held[fn] = map[string]bool{}
		}
		held[fn][name] = true
		grew = true
	}
	for grew {
		grew = false
		for _, file := range files {
			for _, decl := range file.Decls {
				fn, _ := decl.(*ast.FuncDecl)
				ast.Inspect(decl, func(n ast.Node) bool {
					var names, values []ast.Expr
					switch n := n.(type) {
					case *ast.CallExpr:
						// The callee's parameter: a literal's is in this
						// function, a package function's in that one.
						to := fn
						if callee, ok := bare(n.Fun).(*ast.Ident); ok && funcs[callee.Name] != nil {
							to = funcs[callee.Name]
						}
						taken := params(n.Fun, funcs)
						for i, arg := range n.Args {
							if assembly(arg, held[fn]) {
								hold(to, paramAt(taken, i))
							}
						}
						return true
					case *ast.AssignStmt:
						names, values = n.Lhs, n.Rhs
					case *ast.ValueSpec:
						for _, name := range n.Names {
							names = append(names, name)
						}
						values = n.Values
					default:
						return true
					}
					if len(names) == 0 {
						return true
					}
					// One value for several names is a call's results: the
					// first is what it returned (agent, err := wire(...)).
					for i, value := range values {
						to := names[0]
						if len(values) == len(names) {
							to = names[i]
						}
						if name, ok := to.(*ast.Ident); ok && assembly(value, held[fn]) {
							hold(fn, name.Name)
						}
					}
					return true
				})
			}
		}
	}
	return held
}

// packageFuncs is the package's own functions, by name.
func packageFuncs(files map[string]*ast.File) map[string]*ast.FuncDecl {
	funcs := map[string]*ast.FuncDecl{}
	for _, file := range files {
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				funcs[fn.Name.Name] = fn
			}
		}
	}
	return funcs
}

// params is the parameters of what a call calls, when the check can follow
// it there: a function of this package, or a function literal.
func params(fun ast.Expr, funcs map[string]*ast.FuncDecl) *ast.FieldList {
	switch fn := bare(fun).(type) {
	case *ast.Ident:
		if decl, ok := funcs[fn.Name]; ok {
			return decl.Type.Params
		}
	case *ast.FuncLit:
		return fn.Type.Params
	}
	return nil
}

// paramAt is the name of the parameter the i-th argument lands in; the last
// one takes what is left over (variadic).
func paramAt(list *ast.FieldList, i int) string {
	if list == nil {
		return ""
	}
	var names []string
	for _, field := range list.List {
		if len(field.Names) == 0 {
			names = append(names, "")
		}
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return names[min(i, len(names)-1)]
}

// serves is whether a call hands what it is given to a server to serve - a
// handler put on a mux or a test server - which cannot rewire it.
func serves(fun ast.Expr) bool {
	sel, ok := bare(fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && (x.Name == "mux" && sel.Sel.Name == "Handle" || x.Name == "httptest" && sel.Sel.Name == "NewServer")
}

// measures is whether a call only measures what it is given (len, cap).
func measures(fun ast.Expr) bool {
	id, ok := bare(fun).(*ast.Ident)
	return ok && (id.Name == "len" || id.Name == "cap")
}

// assembly is whether e is the assembly, read off it or a copy of it.
func assembly(e ast.Expr, held map[string]bool) bool {
	e = bare(e)
	if call, ok := e.(*ast.CallExpr); ok {
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "wire" {
				return true
			}
		case *ast.SelectorExpr:
			if x, ok := fn.X.(*ast.Ident); ok && x.Name == "assemble" && fn.Sel.Name == "Assemble" {
				return true
			}
		}
	}
	switch x := e.(type) {
	case *ast.Ident:
		return held[x.Name]
	case *ast.SelectorExpr, *ast.IndexExpr:
		return held[rootName(e)]
	}
	return false
}

// bare is e without the parentheses, dereference or address-of around it.
func bare(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != token.AND {
				return e
			}
			e = x.X
		default:
			return e
		}
	}
}

// rootName is the variable an expression is read off: agent for
// agent.Boards[path].Stand(answer).
func rootName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.CallExpr:
			e = x.Fun
		case *ast.ParenExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.TypeAssertExpr:
			e = x.X
		default:
			return ""
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
