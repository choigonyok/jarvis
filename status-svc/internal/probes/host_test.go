package probes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

// The Mac mini's prerequisites, as scripts/host-check writes them.
func TestHostProbes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "host.json")
	e := &env{Config: Config{HostFile: file}}
	run := func(p check.Probe) check.State { return p.Run(context.Background()).State }

	if run(e.hostFileVault()) != check.Unknown {
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
	if run(e.hostFileVault()) != check.Warn || run(e.hostTimeMachine()) != check.Warn || run(e.hostMirror()) != check.Warn || run(e.hostBackup()) != check.OK {
		t.Fatal("꺼짐·없음·SSH 는 경고, 오늘 백업은 정상")
	}
	write(`{"checkedAt":"` + now + `","filevault":"On","timemachine":true,"mirror":"binary","lastBackup":"2026-01-01"}`)
	if run(e.hostFileVault()) != check.OK || run(e.hostTimeMachine()) != check.OK || run(e.hostMirror()) != check.OK || run(e.hostBackup()) != check.Fail {
		t.Fatal("켜짐·있음·바이너리는 정상, 오래된 백업은 이상")
	}
	write(`{"checkedAt":"` + now + `","falkordbAOF":"no"}`)
	if run(e.hostFalkorAOF()) != check.Fail {
		t.Fatal("AOF 가 꺼졌으면 이상")
	}
	write(`{"checkedAt":"` + now + `","falkordbAOF":"yes"}`)
	if run(e.hostFalkorAOF()) != check.OK {
		t.Fatal("AOF 가 켜져 있으면 정상")
	}
	write(`{"checkedAt":"` + now + `","filevault":"Off"}`)
	e.FileVaultOffAccepted = true
	if v := e.hostFileVault().Run(context.Background()); v.State != check.OK || v.Summary == "" {
		t.Fatalf("운영자가 끄기로 했으면 결정으로 보여 줍니다: %+v", v)
	}
	e.FileVaultOffAccepted = false
	write(`{"checkedAt":"2026-01-01T00:00:00Z","filevault":"On"}`)
	if run(e.hostFileVault()) != check.Unknown {
		t.Fatal("점검이 멈췄으면 모름")
	}
}

// The KakaoTalk relogin program's record beside host.json: a logout behind a
// locked screen cannot be fixed by it and is pushed; a login behind one is
// only waiting to become that.
func TestKakaoLoginLockedScreen(t *testing.T) {
	dir := t.TempDir()
	e := &env{Config: Config{HostFile: filepath.Join(dir, "host.json")}}
	now := time.Now().Format(time.RFC3339)
	for state, want := range map[string]check.State{
		"logged-in": check.OK, "locked": check.Fail, "locked-in": check.Warn,
		"locked-unknown": check.Warn, "unreadable": check.Warn,
	} {
		body := `{"checkedAt":"` + now + `","state":"` + state + `"}`
		if err := os.WriteFile(filepath.Join(dir, "kakaotalk-login.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if v := e.kakaoLogin(context.Background()); v.State != want || v.Summary == "" {
			t.Fatalf("%s 는 %v 여야 합니다: %+v", state, want, v)
		}
	}
}
