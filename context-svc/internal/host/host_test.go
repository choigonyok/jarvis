package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
)

// The Mac mini's prerequisites, as scripts/host-check writes them.
func TestHostProbes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "host.json")
	e := &Watch{File: file}
	run := func(r row) string { return r.run(context.Background()).State }

	if run(e.hostFileVault()) != checks.Unknown {
		t.Fatal("기록이 없으면 모름")
	}
	write := func(body string) {
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Format(time.RFC3339)
	today := time.Now().Format("2006-01-02")
	write(`{"checkedAt":"` + now + `","filevault":"Off","timemachine":false,"mirror":"ssh","lastBackup":"` + today + `"}`)
	if run(e.hostFileVault()) != checks.Warn || run(e.hostTimeMachine()) != checks.Warn || run(e.hostMirror()) != checks.Warn || run(e.hostBackup()) != checks.OK {
		t.Fatal("꺼짐·없음·SSH 는 경고, 오늘 백업은 정상")
	}
	write(`{"checkedAt":"` + now + `","filevault":"On","timemachine":true,"mirror":"binary","lastBackup":"2026-01-01"}`)
	if run(e.hostFileVault()) != checks.OK || run(e.hostTimeMachine()) != checks.OK || run(e.hostMirror()) != checks.OK || run(e.hostBackup()) != checks.Fail {
		t.Fatal("켜짐·있음·바이너리는 정상, 오래된 백업은 이상")
	}
	// What the newest backup lacks (its manifest, through host-check): the
	// approval ledger missing is a warning, not a date that looks fine.
	write(`{"checkedAt":"` + now + `","lastBackup":"` + today + `","backupMissing":["jarvis-data.tar.gz:proposals.json"]}`)
	if v := e.hostBackup().run(context.Background()); v.State != checks.Warn || !strings.Contains(v.Summary, "proposals.json") {
		t.Fatalf("빠진 것이 있는 백업은 주의: %+v", v)
	}
	write(`{"checkedAt":"` + now + `","lastBackup":"` + today + `","backupMissing":[]}`)
	if v := e.hostBackup().run(context.Background()); v.State != checks.OK || !strings.Contains(v.Summary, "다 들었어요") {
		t.Fatalf("다 든 백업: %+v", v)
	}
	write(`{"checkedAt":"` + now + `","falkordbAOF":"no"}`)
	if run(e.hostFalkorAOF()) != checks.Fail {
		t.Fatal("AOF 가 꺼졌으면 이상")
	}
	write(`{"checkedAt":"` + now + `","falkordbAOF":"yes"}`)
	if run(e.hostFalkorAOF()) != checks.OK {
		t.Fatal("AOF 가 켜져 있으면 정상")
	}
	write(`{"checkedAt":"` + now + `","filevault":"Off"}`)
	e.FileVaultOffAccepted = true
	if v := e.hostFileVault().run(context.Background()); v.State != checks.OK || v.Summary == "" {
		t.Fatalf("운영자가 끄기로 했으면 결정으로 보여 줍니다: %+v", v)
	}
	e.FileVaultOffAccepted = false
	write(`{"checkedAt":"2026-01-01T00:00:00Z","filevault":"On"}`)
	if run(e.hostFileVault()) != checks.Unknown {
		t.Fatal("점검이 멈췄으면 모름")
	}
}

// The Mac mini's disk: how full, what fills it, and how fast it is growing -
// from host.json and the day's lines beside it, as host-check writes them.
func TestHostDisk(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "host.json")
	e := &Watch{File: file}
	now := time.Now()
	write := func(disk string) {
		body := `{"checkedAt":"` + now.Format(time.RFC3339) + `","disk":` + disk + `}`
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func() checks.Result { return e.hostDisk().run(context.Background()) }

	if v := run(); v.State != checks.Unknown {
		t.Fatalf("기록이 없으면 모름: %+v", v)
	}
	write(`null`)
	if v := run(); v.State != checks.Unknown || v.Meter != nil {
		t.Fatalf("옛 host-check: %+v", v)
	}

	// 245GB with 102GB free, as on the machine this watches.
	const fine = `{"totalKB":239362496,"freeKB":99895296,"dockerKB":70616428,"photosKB":17406464,"backupsKB":502272,
		"docker":[{"type":"Images","size":"28.96GB","reclaimable":"8.127GB (28%)"},{"type":"Containers","size":"75.2MB","reclaimable":"8.192kB (0%)"},
		{"type":"Local Volumes","size":"12.74GB","reclaimable":"3.402GB (26%)"},{"type":"Build Cache","size":"33.48GB","reclaimable":"21.44GB"}]}`
	write(fine)
	v := run()
	if v.State != checks.OK || v.Figure != "143 / 245GB" || v.Meter == nil || *v.Meter != 58 || v.Summary != "102GB 남았어요." {
		t.Fatalf("정상: %+v", v)
	}
	want := []string{"도커 72.3GB: 이미지 29GB, 빌드 캐시 33GB, 볼륨 13GB", "빌드 캐시 중 21GB는 지워도 돼요", "사진 17.8GB", "매일 백업 0.5GB", "시스템과 그 밖의 파일 52.2GB"}
	if strings.Join(v.Facts, "|") != strings.Join(want, "|") {
		t.Fatalf("무엇이 쓰나: %q", v.Facts)
	}
	if v.ObservedAt == nil {
		t.Fatal("맥미니에서 잰 때")
	}

	// A week of history: how fast, and when that reaches the warning.
	day := func(ago int, usedKB int64) string {
		return fmt.Sprintf(`{"day":"%s","usedKB":%d}`, now.AddDate(0, 0, -ago).Format("2006-01-02"), usedKB)
	}
	used := int64(239362496 - 99895296)
	history := func(lines ...string) {
		if err := os.WriteFile(filepath.Join(dir, "disk-history.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	last := func() string { f := run().Facts; return f[len(f)-1] }
	history(day(20, used-40_000_000), day(7, used-7_000_000), day(1, used))
	if got := last(); !strings.HasPrefix(got, "지난 7일 동안 7.2GB 늘었어요. 이대로면 ") || !strings.Contains(got, "일쯤 뒤 85%에 닿아요") {
		t.Fatalf("늘어나는 속도: %q", got)
	}
	history(day(5, used+5_000_000))
	if got := last(); got != "지난 5일 동안 5.1GB 줄었어요" {
		t.Fatalf("줄어듦: %q", got)
	}
	history(day(6, used-100))
	if got := last(); got != "지난 6일 동안 거의 그대로예요" {
		t.Fatalf("그대로: %q", got)
	}
	history(day(1, used-9_000_000))
	if got := last(); strings.HasPrefix(got, "지난") {
		t.Fatalf("며칠은 쌓여야 말함: %q", got)
	}

	// The two lines it is judged by; the docker table missing still measures.
	for _, c := range []struct {
		free  int64
		state string
	}{{35_000_000, checks.Warn}, {12_000_000, checks.Fail}, {38_000_000, checks.OK}} {
		write(fmt.Sprintf(`{"totalKB":239362496,"freeKB":%d,"docker":[]}`, c.free))
		if v := run(); v.State != c.state {
			t.Fatalf("남은 %d: %+v", c.free, v)
		}
	}
}

// The KakaoTalk relogin program's record beside host.json: a logout behind a
// locked screen cannot be fixed by it and is pushed; a login behind one is
// only waiting to become that.
func TestKakaoLoginLockedScreen(t *testing.T) {
	dir := t.TempDir()
	e := &Watch{File: filepath.Join(dir, "host.json")}
	now := time.Now().Format(time.RFC3339)
	for state, want := range map[string]string{
		"logged-in": checks.OK, "waking": checks.Warn, "locked": checks.Fail, "locked-in": checks.Warn,
		"locked-unknown": checks.Warn, "unreadable": checks.Warn, "unreadable-out": checks.Fail,
	} {
		body := `{"checkedAt":"` + now + `","state":"` + state + `"}`
		if err := os.WriteFile(filepath.Join(dir, "kakaotalk-login.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if v := e.judgeLogin(); v.State != want || v.Summary == "" {
			t.Fatalf("%s 는 %v 여야 합니다: %+v", state, want, v)
		}
	}
}

// The phone's last report: nothing yet is not a fault, a long silence is.
func TestThePhoneReporting(t *testing.T) {
	var at *time.Time
	w := &Watch{Location: func(context.Context) (*time.Time, string, error) { return at, "집", nil }}
	state := func() string { return w.location().run(context.Background()).State }
	if state() != checks.Unknown {
		t.Fatal("받은 적 없으면 모름")
	}
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{{time.Hour, checks.OK}, {40 * time.Hour, checks.Warn}, {80 * time.Hour, checks.Fail}} {
		then := time.Now().Add(-c.ago)
		at = &then
		if got := state(); got != c.want {
			t.Fatalf("%v: %s", c.ago, got)
		}
	}
}
