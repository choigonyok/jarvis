package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/convo"
)

// ConvoCommand runs the conversation's scenarios on the real CLI and model
// (internal/convo): `agent convo -scenarios dir -skills dir [-runs n] [-only re]`.
// The scenarios' backends are stand-ins; the subscription's model is not.
const ConvoCommand = "convo"

func convoMain(args []string) int {
	fs := flag.NewFlagSet(ConvoCommand, flag.ContinueOnError)
	dir := fs.String("scenarios", "internal/convo/scenarios", "시나리오 폴더")
	skills := fs.String("skills", "../workspace/.claude/skills", "작업 폴더의 스킬")
	runs := fs.Int("runs", 1, "시나리오마다 몇 번")
	only := fs.String("only", "", "파일 이름이 맞는 것만(정규식)")
	out := fs.String("json", "", "결과를 JSON 줄로 남길 파일")
	timeout := fs.Duration("timeout", 4*time.Minute, "한 턴의 제한")
	verbose := fs.Bool("v", false, "에이전트 로그도 보인다")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	scs, err := convo.Load(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if *only != "" {
		re, err := regexp.Compile(*only)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		scs = slices.DeleteFunc(scs, func(sc convo.Scenario) bool { return !re.MatchString(sc.File) })
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if *verbose {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	var sink io.Writer = io.Discard
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		defer f.Close()
		sink = f
	}
	ctx := context.Background()
	type tally struct{ pass, runs int }
	byFile := map[string]*tally{}
	var startup, first, total []int64
	failed := 0
	for _, sc := range scs {
		byFile[sc.File] = &tally{}
		for i := 0; i < *runs; i++ {
			o := convo.Run(ctx, convo.Options{Skills: *skills, Timeout: *timeout, Log: log}, sc)
			byFile[sc.File].runs++
			mark := "통과"
			if o.Pass {
				byFile[sc.File].pass++
			} else {
				mark = "실패"
				failed++
			}
			var calls []string
			for _, c := range o.Calls {
				calls = append(calls, c.String())
			}
			fmt.Printf("%s  %s · %s  [%s]  시작 %dms · 첫 글자 %dms · 끝 %dms\n", mark, sc.File, sc.Name, strings.Join(calls, " → "),
				o.StartupMS, o.FirstTextMS, o.TotalMS)
			for _, f := range o.Failures {
				fmt.Printf("      - %s\n", f)
			}
			if !o.Pass {
				fmt.Printf("      답: %s\n", oneLine(o.Answer, 300))
			}
			if o.StartupMS >= 0 {
				startup = append(startup, o.StartupMS)
			}
			if o.FirstTextMS >= 0 {
				first = append(first, o.FirstTextMS)
			}
			if o.TotalMS >= 0 {
				total = append(total, o.TotalMS)
			}
			line, _ := json.Marshal(o)
			fmt.Fprintln(sink, string(line))
		}
	}
	passed := 0
	for _, t := range byFile {
		if t.pass == t.runs {
			passed++
		}
	}
	fmt.Printf("\n시나리오 %d개 중 %d개가 매번 통과 · 실행 %d번 중 실패 %d번\n", len(scs), passed, len(scs)**runs, failed)
	fmt.Printf("중앙값: 세션 시작 %dms · 첫 글자 %dms · 끝 %dms\n", median(startup), median(first), median(total))
	if failed > 0 {
		return 1
	}
	return 0
}

func median(xs []int64) int64 {
	if len(xs) == 0 {
		return -1
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/2]
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
