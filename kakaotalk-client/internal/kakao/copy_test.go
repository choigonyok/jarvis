package kakao

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeWAL(t *testing.T, path string, salt byte, frames int) {
	t.Helper()
	hdr := make([]byte, 32)
	for i := 16; i < 24; i++ {
		hdr[i] = salt
	}
	body := append(hdr, make([]byte, frames*100)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// B-5: the main file is copied again only when it changed or the WAL's salt
// did (a checkpoint restarted the log, sometimes without main's mtime
// moving); a WAL that only grew is copied alone.
func TestCopyFollowsTheWALSalt(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.db")
	if err := os.WriteFile(main, []byte("main v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWAL(t, main+"-wal", 1, 1)
	r := &Reader{dbPath: main, scratch: filepath.Join(dir, "scratch")}
	if err := os.MkdirAll(r.scratch, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, recopied, err := r.copyLiveFiles(); err != nil || !recopied {
		t.Fatalf("처음에는 복사: %v %v", recopied, err)
	}
	if _, recopied, _ := r.copyLiveFiles(); recopied {
		t.Fatal("바뀐 것이 없으면 main 을 다시 복사하지 않습니다")
	}

	writeWAL(t, main+"-wal", 1, 3) // the log grew
	dst, recopied, _ := r.copyLiveFiles()
	if recopied {
		t.Fatal("WAL 이 자라기만 했으면 main 은 그대로")
	}
	if fi, _ := os.Stat(dst + "-wal"); fi == nil || fi.Size() != 32+300 {
		t.Fatalf("자란 WAL 은 복사돼야 합니다: %v", fi)
	}

	// A checkpoint: new salt, main rewritten but with its old mtime and size.
	st, _ := os.Stat(main)
	if err := os.WriteFile(main, []byte("main v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(main, time.Now(), st.ModTime())
	writeWAL(t, main+"-wal", 2, 1)
	if _, recopied, _ := r.copyLiveFiles(); !recopied {
		t.Fatal("salt 가 바뀌면 main 을 다시 복사해야 합니다")
	}
	if b, _ := os.ReadFile(filepath.Join(r.scratch, "work.db")); string(b) != "main v2" {
		t.Fatalf("새 main 이어야 합니다: %q", b)
	}
}
