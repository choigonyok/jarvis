package run

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The modules and the HTTP layer reach the runner through this package
// alone: none of them, nor their tests, imports the runner itself.
func TestNothingButTheRunnerKnowsIt(t *testing.T) {
	const runner = "github.com/choigonyok/jarvis/agent/internal/claudecode"
	var files []string
	for _, glob := range []string{"../../module/*/*.go", "../../httpapi/*.go"} {
		found, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, found...)
	}
	if len(files) < 20 {
		t.Fatalf("읽을 파일이 %d개뿐입니다", len(files))
	}
	fset := token.NewFileSet()
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == runner {
				t.Errorf("%s 가 실행기(claudecode)를 직접 가져옵니다 - core/run 의 약속만 보세요", strings.TrimPrefix(f, "../../"))
			}
		}
	}
}
