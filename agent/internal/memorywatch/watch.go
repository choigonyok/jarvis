// Package memorywatch tells the operator whenever Jarvis's own memory changes.
//
// The CLI lets its model write its memory folder without a card (see
// config.MemoryDir), and what is there is loaded into every later session.
// The operator chose to keep that free and be told instead: each change - a
// note added, edited or removed, a line of the MEMORY.md index - becomes one
// notification with the words that were written, so a note nobody asked for
// (a page talking the model into "remembering" an instruction) is seen the
// day it lands.
package memorywatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
)

// Notes is the memory folder as read once: each file's path under the folder
// and its text.
type Notes map[string]string

// index is the file the CLI hands every session; the others are the notes
// its lines point to.
const index = "MEMORY.md"

// maxRead bounds what is read of one file. Notes are a few hundred bytes; a
// file past this is still noticed, by its first part changing.
const maxRead = 256 << 10

// Read reads every file under dir. A folder not made yet is an empty one.
func Read(dir string) (Notes, error) {
	notes := Notes{}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return notes, nil
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		body, err := io.ReadAll(io.LimitReader(f, maxRead))
		if err != nil {
			return err
		}
		notes[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	return notes, err
}

// Entry is one file's change between two readings.
type Entry struct {
	Path string
	// New and Gone: the file appeared or went away. Neither: it was edited.
	New, Gone bool
	// Added and Removed are the lines that came and went - blank lines and
	// the header's bookkeeping (modified, originSessionId) left out.
	Added, Removed []string
	// Text is a new note's words, its header left out.
	Text string
}

// Compare lists what differs from before to after, the index first. A file
// whose only difference is bookkeeping is not listed.
func Compare(before, after Notes) []Entry {
	var out []Entry
	for _, path := range sortedPaths(before, after) {
		old, had := before[path]
		cur, has := after[path]
		if had && has && old == cur {
			continue
		}
		e := Entry{Path: path, New: !had, Gone: !has}
		e.Added, e.Removed = diff(lines(old), lines(cur))
		if e.New {
			e.Text = text(cur)
		}
		if !e.New && !e.Gone && len(e.Added) == 0 && len(e.Removed) == 0 {
			continue
		}
		out = append(out, e)
	}
	return out
}

func sortedPaths(a, b Notes) []string {
	paths := slices.Collect(maps.Keys(a))
	for p := range b {
		if _, ok := a[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.SortFunc(paths, func(x, y string) int {
		switch {
		case x == y:
			return 0
		case x == index:
			return -1
		case y == index:
			return 1
		}
		return strings.Compare(x, y)
	})
	return paths
}

// bookkeeping is what the CLI rewrites in a note's header on every save.
var bookkeeping = []string{"modified:", "originSessionId:"}

// lines is a file's lines that say something: not blank, not the header's
// fences, not its bookkeeping.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l == "---" || slices.ContainsFunc(bookkeeping, func(p string) bool { return strings.HasPrefix(l, p) }) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// diff is which lines are in cur and not old (added), and the other way
// round - a line written twice counts twice.
func diff(old, cur []string) (added, removed []string) {
	left := func(from, against []string) []string {
		count := map[string]int{}
		for _, l := range against {
			count[l]++
		}
		var out []string
		for _, l := range from {
			if count[l] > 0 {
				count[l]--
				continue
			}
			out = append(out, l)
		}
		return out
	}
	return left(cur, old), left(old, cur)
}

// text is a note's words without its header; the header's description if
// it has nothing else.
func text(note string) string {
	body := note
	var description string
	if rest, ok := strings.CutPrefix(note, "---\n"); ok {
		if header, after, ok := strings.Cut(rest, "\n---"); ok {
			body = after
			for _, l := range strings.Split(header, "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(l), "description:"); ok {
					description = strings.ReplaceAll(strings.Trim(strings.TrimSpace(v), `"`), `\"`, `"`)
				}
			}
		}
	}
	if body = plain(body); body == "" {
		body = description
	}
	return body
}

var link = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// plain is markdown as it reads in a notification: one line, links by their
// words, no bold marks or list dashes.
func plain(s string) string {
	s = link.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimPrefix(s, "- ")
}

// lineMax and linesMax keep a notification inside what the inbox keeps of a
// body (600 characters): four lines of 140 at most.
const (
	lineMax  = 140
	linesMax = 4
)

// Message is the notification for a change: what kind of change it is, and
// the words written - whether the operator asked for them is the point.
func Message(entries []Entry) (title, body string) {
	adds, removes := false, false
	var out []string
	for _, e := range entries {
		adds = adds || e.New || len(e.Added) > 0
		removes = removes || e.Gone || len(e.Removed) > 0
		out = append(out, e.lines()...)
	}
	switch {
	case !removes:
		title = "자비스가 새로 기억했어요"
	case !adds:
		title = "자비스가 기억을 지웠어요"
	default:
		title = "자비스가 기억을 고쳤어요"
	}
	if len(out) > linesMax {
		out = append(out[:linesMax-1], fmt.Sprintf("외 %d건", len(out)-linesMax+1))
	}
	return title, strings.Join(out, "\n")
}

func (e Entry) lines() []string {
	if e.Path == index {
		if e.Gone {
			return []string{"기억 목록(MEMORY.md)을 지움"}
		}
		var out []string
		for _, l := range e.Added {
			out = append(out, clip("목록에 추가: "+plain(l)))
		}
		for _, l := range e.Removed {
			out = append(out, clip("목록에서 삭제: "+plain(l)))
		}
		return out
	}
	name := strings.TrimSuffix(e.Path, ".md")
	switch {
	case e.Gone:
		return []string{"지운 기억 " + name}
	case e.New && e.Text == "":
		return []string{"새 기억 " + name}
	case e.New:
		return []string{clip("새 기억 " + name + ": " + e.Text)}
	case len(e.Removed) == 0:
		return []string{clip("고친 기억 " + name + ": " + plain(strings.Join(e.Added, " ")))}
	case len(e.Added) == 0:
		return []string{clip("고친 기억 " + name + ": 삭제 - " + plain(strings.Join(e.Removed, " ")))}
	}
	half := lineMax / 2
	return []string{clip("고친 기억 " + name + ": 추가 - " + clipTo(plain(strings.Join(e.Added, " ")), half) +
		" / 삭제 - " + clipTo(plain(strings.Join(e.Removed, " ")), half))}
}

func clip(s string) string { return clipTo(s, lineMax) }

func clipTo(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

// Watcher reads the memory folder every so often and tells what changed
// since it last told.
type Watcher struct {
	Dir string
	// SeenPath keeps the folder as last told, so a restart neither repeats a
	// change nor misses one made just before it. It is outside Dir: the model
	// writes Dir, and must not be able to write what Dir is compared with.
	SeenPath string
	Every    time.Duration
	// Tell hears each change once it has settled.
	Tell func(entries []Entry)
	Log  *slog.Logger
}

// Run watches until ctx ends. The first run ever takes the folder as it
// finds it: what was there before the watching began is not news.
//
// A change is told once two readings in a row agree: a note and its index
// line are written seconds apart, and are one change.
func (w *Watcher) Run(ctx context.Context) {
	var seen Notes
	if err := jsonfile.Load(w.SeenPath, &seen); err != nil {
		w.Log.Warn("기억 감시: 마지막으로 알린 상태를 읽지 못해 지금부터 봅니다", "err", err)
	}
	if seen == nil {
		now, err := Read(w.Dir)
		if err != nil {
			w.Log.Warn("기억 감시: 기억 폴더를 읽지 못했습니다", "dir", w.Dir, "err", err)
			now = Notes{}
		}
		seen = now
		w.save(seen)
	}
	tick := time.NewTicker(w.Every)
	defer tick.Stop()
	var last Notes
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now, err := Read(w.Dir)
		if err != nil {
			w.Log.Warn("기억 감시: 기억 폴더를 읽지 못했습니다", "dir", w.Dir, "err", err)
			continue
		}
		settled := last != nil && maps.Equal(now, last)
		last = now
		if !settled || maps.Equal(now, seen) {
			continue
		}
		if entries := Compare(seen, now); len(entries) > 0 {
			w.Tell(entries)
		}
		seen = now
		w.save(seen)
	}
}

func (w *Watcher) save(n Notes) {
	if err := jsonfile.Save(w.SeenPath, n); err != nil {
		w.Log.Warn("기억 감시: 상태를 저장하지 못했습니다(재시작 뒤 같은 알림이 다시 갈 수 있습니다)", "err", err)
	}
}
