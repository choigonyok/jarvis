package policy

import (
	"encoding/json"
	"testing"
)

// The guest's file tools reach its photos and its memory, and nothing else -
// not by a path that climbs out, not the folder's neighbour, not the CLI's
// own home, not a shell.
func TestFolders(t *testing.T) {
	f := Folders{"/uploads", "/home/jarvis/.claude/projects/-workspace/memory", ""}
	in := func(k, v string) json.RawMessage { b, _ := json.Marshal(map[string]string{k: v}); return b }
	for _, c := range []struct {
		tool         string
		input        json.RawMessage
		file, inside bool
	}{
		{"Read", in("file_path", "/uploads/0123456789abcdef0123456789abcdef.jpg"), true, true},
		{"Write", in("file_path", "/home/jarvis/.claude/projects/-workspace/memory/MEMORY.md"), true, true},
		{"Edit", in("file_path", "/home/jarvis/.claude/projects/-workspace/memory/style.md"), true, true},
		{"Read", in("file_path", "/proc/self/environ"), true, false},
		{"Read", in("file_path", "/uploads/../proc/self/environ"), true, false},
		{"Read", in("file_path", "/uploads"), true, false},
		{"Read", in("file_path", "/uploads-other/x.jpg"), true, false},
		{"Read", in("file_path", "/home/jarvis/.claude/.credentials.json"), true, false},
		{"Write", in("file_path", "/home/jarvis/.claude/settings.json"), true, false},
		{"Read", in("file_path", "uploads/x.jpg"), true, false},
		{"Read", json.RawMessage(`{}`), true, false},
		{"Grep", in("path", "/workspace"), true, false},
		{"Bash", in("command", "cat /uploads/x.jpg"), true, false},
		{"WebFetch", in("url", "https://example.com"), false, false},
		{"mcp__calendar__create_event", json.RawMessage(`{"title":"x"}`), false, false},
	} {
		if file, inside := f.Judge(c.tool, c.input); file != c.file || inside != c.inside {
			t.Errorf("%s %s: file=%v inside=%v, want %v %v", c.tool, c.input, file, inside, c.file, c.inside)
		}
	}
	if file, inside := (Folders{"/"}).Judge("Read", in("file_path", "/etc/passwd")); !file || inside {
		t.Fatal("루트 폴더는 폴더가 아님")
	}
}
