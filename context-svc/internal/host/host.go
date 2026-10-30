// Package host says how the Mac mini stands: what scripts/host-check writes
// beside the stack (~/.jarvis-host, mounted here read-only), what the KakaoTalk
// relogin program saw, and whether the phone is reporting. A container cannot
// see these for itself, and status-svc has no code of its own for them: each is
// a check on the stream (gocommon/checks).
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
	"github.com/choigonyok/jarvis/gocommon/health"
)

// Watch looks at the host.
type Watch struct {
	// File is host.json.
	File string
	// FileVaultOffAccepted: the operator keeps it off.
	FileVaultOffAccepted bool
	// Location is when the phone last reported, and the state in a line.
	Location func(ctx context.Context) (*time.Time, string, error)
}

const every = time.Minute

// Run says how each thing stands, once a minute, until ctx ends.
func (w *Watch) Run(ctx context.Context, report *checks.Reporter, loops *health.Loops) {
	loops.Add("host", "맥미니 점검 읽기", 10*every)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for _, r := range w.rows() {
			report.Report(r.judged(ctx))
		}
		loops.OK("host")
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// row is one thing looked at.
type row struct {
	id, group, name, rule, fix string
	deps                       []string
	run                        func(ctx context.Context) checks.Result
}

func (r row) judged(ctx context.Context) checks.Result {
	v := r.run(ctx)
	v.ID, v.Group, v.Name, v.Rule, v.Fix, v.Deps = r.id, r.group, r.name, r.rule, r.fix, r.deps
	return v
}

func (w *Watch) rows() []row {
	return []row{w.location(), w.kakaoLogin(), w.hostDisk(), w.hostFileVault(), w.hostTimeMachine(), w.hostMirror(), w.hostBackup(), w.hostFalkorAOF()}
}

// location is the phone's OwnTracks: a phone that stops reporting says nothing
// of it.
func (w *Watch) location() row {
	return row{id: "context.location", group: "수집", name: "위치 수신",
		rule: "폰의 OwnTracks 가 보낸 마지막 위치가 36시간 안이면 정상, 36–72시간이면 주의, 72시간을 넘으면 이상. 한 번도 받지 않았으면 측정하지 않아요(설정 전). 크게 움직이지 않는 날은 위치가 드물게 와요.",
		fix:  "폰에서 OwnTracks 가 위치 권한 '항상 허용'인지, 모드가 Quiet 가 아닌지, 서버 주소·비밀번호가 맞는지 보세요. 배터리가 8% 아래면 일부러 조용히 둬요.",
		deps: []string{"svc.context"},
		run: func(ctx context.Context) checks.Result {
			if w.Location == nil {
				return checks.Result{State: checks.Unknown, Summary: "위치를 읽지 않아요."}
			}
			at, summary, err := w.Location(ctx)
			if err != nil {
				return checks.Result{State: checks.Fail, Summary: "위치 상태를 읽지 못했어요: " + clip(err.Error(), 120)}
			}
			v := checks.Result{ObservedAt: at, ObservedLabel: "마지막 위치", Facts: []string{summary}}
			if at == nil {
				v.State, v.Summary = checks.Unknown, "아직 위치를 받은 적이 없어요(OwnTracks 설정 전)."
				return v
			}
			switch age := time.Since(*at); {
			case age > 72*time.Hour:
				v.State, v.Summary = checks.Fail, fmt.Sprintf("%s째 위치가 오지 않아요.", span(age))
			case age > 36*time.Hour:
				v.State, v.Summary = checks.Warn, fmt.Sprintf("%s째 위치가 오지 않아요.", span(age))
			default:
				v.State, v.Summary = checks.OK, "위치를 받고 있어요."
			}
			return v
		}}
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// span renders a duration the way a person says it: "3일", "14시간".
func span(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d일", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%d시간", int(d.Hours()))
	default:
		return fmt.Sprintf("%d분", int(d.Minutes()))
	}
}

// hostFacts is what scripts/host-check writes on the Mac mini every ten
// minutes (~/.jarvis-host/host.json, mounted here read-only).
type hostFacts struct {
	CheckedAt   time.Time `json:"checkedAt"`
	FileVault   string    `json:"filevault"`   // On | Off | what fdesetup said
	TimeMachine bool      `json:"timemachine"` // a destination is set
	Mirror      string    `json:"mirror"`      // binary | ssh | ""
	LastBackup  string    `json:"lastBackup"`  // YYYY-MM-DD of the newest backup, "" = none
	// BackupMissing is what the newest backup lacks of what it must hold
	// (its manifest.json); nil when that backup has no manifest.
	BackupMissing *[]string `json:"backupMissing"`
	FalkorAOF     string    `json:"falkordbAOF"` // CONFIG GET appendonly: yes | no | "" (not reached)
	// Disk is the internal disk; nil from a host-check older than it.
	Disk *diskFacts `json:"disk"`
}

// diskFacts is the Mac mini's internal disk and what takes it up, in KiB. The
// volumes share one container, so what is used is the whole less what is free.
type diskFacts struct {
	TotalKB   int64 `json:"totalKB"`
	FreeKB    int64 `json:"freeKB"`
	DockerKB  int64 `json:"dockerKB"` // what Docker's virtual disk really takes up
	PhotosKB  int64 `json:"photosKB"`
	BackupsKB int64 `json:"backupsKB"`
	// Docker is `docker system df` as it reads: Images, Local Volumes, Build
	// Cache, with sizes like 28.96GB and 8.127GB (28%).
	Docker []struct {
		Type        string `json:"type"`
		Size        string `json:"size"`
		Reclaimable string `json:"reclaimable"`
	} `json:"docker"`
}

// readHost returns the facts, or the verdict to show when there are none to
// go on.
func (w *Watch) readHost() (hostFacts, *checks.Result) {
	var h hostFacts
	raw, err := os.ReadFile(w.File)
	if err != nil {
		return h, &checks.Result{State: checks.Unknown, Summary: "맥미니 점검 기록이 없어요. scripts/host-check install 이 필요해요."}
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, &checks.Result{State: checks.Unknown, Summary: "맥미니 점검 기록을 읽지 못했어요."}
	}
	if time.Since(h.CheckedAt) > time.Hour {
		at := h.CheckedAt
		return h, &checks.Result{State: checks.Unknown, Summary: "맥미니 점검이 한 시간 넘게 멈춰 있어요.", ObservedAt: &at, ObservedLabel: "마지막 점검"}
	}
	return h, nil
}

func (w *Watch) hostProbe(id, name, rule, fix string, judge func(hostFacts) checks.Result) row {
	return row{id: id, group: "기반", name: name, rule: rule, fix: fix,
		run: func(context.Context) checks.Result {
			h, missing := w.readHost()
			if missing != nil {
				return *missing
			}
			v := judge(h)
			at := h.CheckedAt
			v.ObservedAt, v.ObservedLabel = &at, "맥미니에서 확인"
			return v
		}}
}

// The share of the disk from which it is a warning, and a fault. Docker's
// virtual disk can grow past what is left, so it is the disk that runs out:
// when it does the database stops taking writes.
const (
	diskWarnPct = 85
	diskFailPct = 95
)

func (w *Watch) hostDisk() row {
	return w.hostProbe("host.disk", "맥미니 저장 공간",
		fmt.Sprintf("내장 디스크를 %d%% 아래로 쓰고 있으면 정상, %d%%부터 주의, %d%%부터 이상이에요. 도커(데이터베이스 포함)·사진·백업이 이 디스크를 함께 쓰고, 다 차면 데이터베이스가 쓰기를 멈춰요. 크기는 10분마다 재요.", diskWarnPct, diskWarnPct, diskFailPct),
		"맥미니에서 docker builder prune 으로 도커 빌드 캐시부터 지우세요(다음 배포가 조금 느려질 뿐이에요). 쓰지 않는 이미지는 docker image prune -a. 사진이 크면 외장 디스크로 옮기는 수밖에 없어요.",
		func(h hostFacts) checks.Result { return diskVerdict(h.Disk, w.diskHistory(), time.Now()) })
}

// diskDay is one line of disk-history.jsonl: a day's last measure.
type diskDay struct {
	Day    string `json:"day"`
	UsedKB int64  `json:"usedKB"`
}

// diskHistory reads what host-check keeps beside host.json; none is fine, the
// row only says less.
func (w *Watch) diskHistory() []diskDay {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(w.File), "disk-history.jsonl"))
	if err != nil {
		return nil
	}
	var days []diskDay
	for _, line := range strings.Split(string(raw), "\n") {
		var d diskDay
		if json.Unmarshal([]byte(line), &d) == nil && d.Day != "" {
			days = append(days, d)
		}
	}
	return days
}

// gb is KiB as a size people read: the decimal gigabytes of Finder and Docker.
func gb(kb int64) string {
	g := float64(kb) * 1024 / 1e9
	if g < 100 && math.Abs(g-math.Round(g)) >= 0.05 {
		return fmt.Sprintf("%.1fGB", g)
	}
	return fmt.Sprintf("%.0fGB", g)
}

// dockerSize is a size as `docker system df` writes it ("28.96GB", "8.127GB
// (28%)"), in GB.
func dockerSize(s string) float64 {
	s, _, _ = strings.Cut(strings.TrimSpace(s), " ")
	for _, u := range []struct {
		unit string
		gb   float64
	}{{"TB", 1000}, {"GB", 1}, {"MB", 1e-3}, {"kB", 1e-6}, {"B", 1e-9}} {
		if num, ok := strings.CutSuffix(s, u.unit); ok {
			n, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0
			}
			return n * u.gb
		}
	}
	return 0
}

// diskVerdict is the disk's row: how full it is, what fills it, how fast.
func diskVerdict(d *diskFacts, days []diskDay, now time.Time) checks.Result {
	if d == nil || d.TotalKB <= 0 {
		return checks.Result{State: checks.Unknown, Summary: "저장 공간 기록이 아직 없어요. 맥미니 점검이 한 번 돌면 채워져요."}
	}
	used := d.TotalKB - d.FreeKB
	pct := int(math.Round(float64(used) * 100 / float64(d.TotalKB)))
	v := checks.Result{State: checks.OK, Meter: &pct,
		Figure:  fmt.Sprintf("%.0f / %.0fGB", float64(used)*1024/1e9, float64(d.TotalKB)*1024/1e9),
		Summary: fmt.Sprintf("%s 남았어요.", gb(d.FreeKB))}
	switch {
	case pct >= diskFailPct:
		v.State = checks.Fail
		v.Summary = fmt.Sprintf("%s밖에 남지 않았어요. 다 차면 데이터베이스가 멈춰요.", gb(d.FreeKB))
	case pct >= diskWarnPct:
		v.State = checks.Warn
		v.Summary = fmt.Sprintf("%s 남았어요. 슬슬 정리할 때예요.", gb(d.FreeKB))
	}

	sizes := map[string]float64{}
	var dockerGB, reclaim float64
	for _, u := range d.Docker {
		sizes[u.Type] = dockerSize(u.Size)
		dockerGB += sizes[u.Type]
		if u.Type == "Build Cache" {
			reclaim = dockerSize(u.Reclaimable)
		}
	}
	dockerKB := d.DockerKB
	if dockerKB == 0 {
		dockerKB = int64(dockerGB * 1e9 / 1024)
	}
	if dockerKB > 0 {
		line := "도커 " + gb(dockerKB)
		if len(sizes) > 0 {
			line += fmt.Sprintf(": 이미지 %.0fGB, 빌드 캐시 %.0fGB, 볼륨 %.0fGB", sizes["Images"], sizes["Build Cache"], sizes["Local Volumes"])
		}
		v.Facts = append(v.Facts, line)
	}
	if reclaim >= 1 {
		v.Facts = append(v.Facts, fmt.Sprintf("빌드 캐시 중 %.0fGB는 지워도 돼요", reclaim))
	}
	if d.PhotosKB > 0 {
		v.Facts = append(v.Facts, "사진 "+gb(d.PhotosKB))
	}
	if d.BackupsKB > 0 {
		v.Facts = append(v.Facts, "매일 백업 "+gb(d.BackupsKB))
	}
	if rest := used - dockerKB - d.PhotosKB - d.BackupsKB; rest > 0 {
		v.Facts = append(v.Facts, "시스템과 그 밖의 파일 "+gb(rest))
	}
	if line := diskTrend(d, used, days, now); line != "" {
		v.Facts = append(v.Facts, line)
	}
	return v
}

// diskTrend is how much the disk grew over the last week of history, and when
// that pace reaches the warning - nothing until there are a few days to go on.
func diskTrend(d *diskFacts, used int64, days []diskDay, now time.Time) string {
	const span, least = 7, 3
	var from *diskDay
	var ago int
	for i := range days {
		on, err := time.ParseInLocation("2006-01-02", days[i].Day, now.Location())
		if err != nil {
			continue
		}
		if n := int(now.Sub(on).Hours() / 24); n <= span && n >= least {
			from, ago = &days[i], n
			break
		}
	}
	if from == nil {
		return ""
	}
	grew := used - from.UsedKB
	switch {
	case grew > 1<<20:
		line := fmt.Sprintf("지난 %d일 동안 %s 늘었어요", ago, gb(grew))
		if left := d.TotalKB*diskWarnPct/100 - used; left > 0 {
			if n := int(float64(left) / (float64(grew) / float64(ago))); n < 365 {
				line += fmt.Sprintf(". 이대로면 %d일쯤 뒤 %d%%에 닿아요", n, diskWarnPct)
			}
		}
		return line
	case grew < -(1 << 20):
		return fmt.Sprintf("지난 %d일 동안 %s 줄었어요", ago, gb(-grew))
	}
	return fmt.Sprintf("지난 %d일 동안 거의 그대로예요", ago)
}

func (w *Watch) hostFileVault() row {
	return w.hostProbe("host.filevault", "맥미니 디스크 암호화",
		"FileVault 가 켜져 있으면 정상.",
		"맥미니의 시스템 설정 → 개인정보 보호 및 보안 → FileVault 를 켜세요. 복구 키는 맥미니 밖에 두세요. 켜면 정전 뒤 다시 켜질 때 맥미니 앞에서 암호를 넣어야 서비스가 떠요.",
		func(h hostFacts) checks.Result {
			switch h.FileVault {
			case "On":
				return checks.Result{State: checks.OK, Summary: "켜져 있어요."}
			case "Off":
				if w.FileVaultOffAccepted {
					return checks.Result{State: checks.OK, Summary: "꺼져 있어요 - 운영자가 그렇게 두기로 했어요."}
				}
				return checks.Result{State: checks.Warn, Summary: "꺼져 있어요."}
			default:
				return checks.Result{State: checks.Warn, Summary: fmt.Sprintf("상태: %s", h.FileVault)}
			}
		})
}

func (w *Watch) hostTimeMachine() row {
	return w.hostProbe("host.timemachine", "백업의 바깥 사본(Time Machine)",
		"Time Machine 대상 디스크가 있으면 정상. 매일 백업은 맥미니 안(~/jarvis-backups)에 떠 두기만 해서, 맥미니 밖으로 옮기는 것은 Time Machine 이에요.",
		"맥미니에 외장 디스크를 연결하고 시스템 설정 → 일반 → Time Machine 에서 백업 디스크로 지정하세요.",
		func(h hostFacts) checks.Result {
			if h.TimeMachine {
				return checks.Result{State: checks.OK, Summary: "대상 디스크가 있어요."}
			}
			return checks.Result{State: checks.Warn, Summary: "대상 디스크가 없어요. 백업이 맥미니 밖으로 나가지 않아요."}
		})
}

func (w *Watch) hostMirror() row {
	return w.hostProbe("host.mirror", "iMessage 미러 권한",
		"전용 바이너리가 직접 복사하면 정상. SSH 방식이면 원격 로그인의 전체 디스크 접근 권한에 기대고 있다는 뜻이에요.",
		"맥미니의 시스템 설정 → 개인정보 보호 및 보안 → 전체 디스크 접근 권한에 ~/.local/bin/jarvis-imessage-mirror 를 추가하세요. 5분 안에 저절로 바뀌어요. 그 뒤 sshd-keygen-wrapper 의 권한을 끄세요.",
		func(h hostFacts) checks.Result {
			switch h.Mirror {
			case "binary":
				return checks.Result{State: checks.OK, Summary: "전용 바이너리가 직접 복사해요."}
			case "ssh":
				return checks.Result{State: checks.Warn, Summary: "아직 SSH 원격 로그인의 권한으로 복사해요."}
			default:
				return checks.Result{State: checks.Unknown, Summary: "미러 방식 기록이 없어요."}
			}
		})
}

func (w *Watch) hostBackup() row {
	return w.hostProbe("host.backup", "매일 백업",
		"오늘이나 어제 백업이 있고, 담아야 할 것(데이터베이스·기억 그래프·수집기·에이전트와 손님의 데이터 - 승인 장부까지 - ·브라우저 프로필)이 다 들었으면 정상(매일 04:30). 빠진 것이 있으면 주의, 이틀 넘게 백업이 없으면 이상.",
		"맥미니에서 scripts/backup status 로 LaunchAgent 와 로그를 보세요. 지금 뜨려면 scripts/backup run, 복원되는지는 scripts/backup restore-test.",
		func(h hostFacts) checks.Result {
			if h.LastBackup == "" {
				return checks.Result{State: checks.Fail, Summary: "백업이 없어요."}
			}
			day, err := time.ParseInLocation("2006-01-02", h.LastBackup, time.Local)
			if err != nil {
				return checks.Result{State: checks.Unknown, Summary: "백업 날짜를 읽지 못했어요."}
			}
			age := int(time.Since(day).Hours() / 24)
			if age > 1 {
				return checks.Result{State: checks.Fail, Summary: fmt.Sprintf("마지막 백업이 %d일 전(%s)이에요.", age, h.LastBackup)}
			}
			switch {
			case h.BackupMissing == nil:
				return checks.Result{State: checks.OK, Summary: fmt.Sprintf("마지막 백업 %s.", h.LastBackup),
					Facts: []string{"이 백업에는 담은 것의 목록(manifest)이 없어서 날짜만 봤어요. 다음 백업부터 내용도 봐요."}}
			case len(*h.BackupMissing) > 0:
				return checks.Result{State: checks.Warn, Summary: fmt.Sprintf("마지막 백업(%s)에 빠진 것이 있어요: %s", h.LastBackup, strings.Join(*h.BackupMissing, ", ")),
					Facts: []string{"빠진 것은 그날의 사본이 없어요. 맥미니의 ~/Library/Logs/jarvis-backup.log 에 그 부분의 오류가 있어요."}}
			}
			return checks.Result{State: checks.OK, Summary: fmt.Sprintf("마지막 백업 %s. 담아야 할 것이 다 들었어요.", h.LastBackup)}
		})
}

func (w *Watch) hostFalkorAOF() row {
	return w.hostProbe("host.falkordb", "기억 그래프 저장(AOF)",
		"FalkorDB 의 appendonly 가 yes 면 정상. 꺼져 있으면 마지막 스냅숏 뒤의 기억이 비정상 종료에 사라져요.",
		"docker-compose.yml 의 falkordb REDIS_ARGS(--appendonly yes)를 확인하고 falkordb 를 다시 띄우세요.",
		func(h hostFacts) checks.Result {
			switch h.FalkorAOF {
			case "yes":
				return checks.Result{State: checks.OK, Summary: "켜져 있어요(매초 fsync)."}
			case "":
				return checks.Result{State: checks.Unknown, Summary: "FalkorDB 에 묻지 못했어요."}
			default:
				return checks.Result{State: checks.Fail, Summary: "꺼져 있어요. 마지막 스냅숏 뒤의 쓰기가 비정상 종료에 사라져요."}
			}
		})
}

// kakaoLoginFacts is what the Mac mini's KakaoTalk relogin program last saw
// (kakaotalk-client/cmd/relogin, ~/.jarvis-host/kakaotalk-login.json beside
// host.json). It writes on every look, every few seconds.
type kakaoLoginFacts struct {
	CheckedAt  time.Time  `json:"checkedAt"`
	State      string     `json:"state"`
	Detail     string     `json:"detail"`
	Fails      int        `json:"fails"`
	ReloggedAt *time.Time `json:"reloggedAt"`
}

func (w *Watch) kakaoLogin() row {
	return row{id: "kakao.login", group: "수집", name: "카톡 로그인",
		rule: "맥미니 카톡이 로그인돼 있으면 정상 - 로그아웃돼도 자동 로그인 프로그램이 5초 안에 알아보고 인터넷이 돌아오는 대로 다시 로그인해요. 인터넷을 기다리거나 다시 시도하는 중이면 주의, 비밀번호가 맞지 않거나 휴대폰 확인이 필요하거나 권한·비밀번호가 없으면 이상. 프로그램을 설치하기 전에는 측정하지 않아요.",
		fix:  "scripts/kakaotalk-relogin status 로 상태와 로그를 보세요. 권한이 없으면 맥미니의 시스템 설정 → 개인정보 보호 및 보안 → 손쉬운 사용에서 'Jarvis 카톡 자동 로그인'을 켜고, 비밀번호가 없거나 틀렸으면 scripts/kakaotalk-relogin password. 휴대폰 확인으로 멈췄으면 맥미니에서 직접 로그인하면 다시 지켜봐요.",
		run:  func(context.Context) checks.Result { return w.judgeLogin() }}
}

func (w *Watch) judgeLogin() checks.Result {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(w.File), "kakaotalk-login.json"))
	if err != nil {
		return checks.Result{State: checks.Unknown, Summary: "자동 로그인 프로그램이 설치되지 않았어요(scripts/kakaotalk-relogin install)."}
	}
	var k kakaoLoginFacts
	if json.Unmarshal(raw, &k) != nil {
		return checks.Result{State: checks.Unknown, Summary: "자동 로그인 기록을 읽지 못했어요."}
	}
	at := k.CheckedAt
	v := checks.Result{ObservedAt: &at, ObservedLabel: "맥미니에서 확인"}
	if k.ReloggedAt != nil && time.Since(*k.ReloggedAt) < 24*time.Hour {
		v.Facts = append(v.Facts, fmt.Sprintf("%s 에 로그아웃돼 있던 것을 저절로 다시 로그인했어요", k.ReloggedAt.Local().Format("01-02 15:04")))
	}
	if k.Detail != "" {
		v.Facts = append(v.Facts, k.Detail)
	}
	if time.Since(k.CheckedAt) > 5*time.Minute {
		v.State, v.Summary = checks.Fail, "자동 로그인 프로그램이 5분 넘게 응답하지 않아요."
		return v
	}
	switch k.State {
	case "logged-in", "no-window", "relogged":
		v.State, v.Summary = checks.OK, "카톡이 로그인돼 있어요."
	case "offline":
		v.State, v.Summary = checks.Warn, "로그아웃돼 있어요. 인터넷이 돌아오면 다시 로그인해요."
	case "waiting", "failed":
		v.State, v.Summary = checks.Warn, fmt.Sprintf("로그아웃돼 있어요. 로그인을 %d번 실패해 곧 다시 해 봐요.", k.Fails)
	case "notice", "starting":
		v.State, v.Summary = checks.Warn, "로그아웃돼서 다시 로그인하는 중이에요."
	case "wrong-password", "gave-up":
		v.State, v.Summary = checks.Fail, "비밀번호가 맞지 않아 멈췄어요. scripts/kakaotalk-relogin password 로 다시 넣어 주세요."
	case "needs-person":
		v.State, v.Summary = checks.Fail, "휴대폰 확인처럼 사람이 해야 하는 화면이 떠서 멈췄어요."
	case "waking":
		v.State, v.Summary = checks.Warn, "로그아웃돼서 꺼진 화면을 깨워 다시 로그인하는 중이에요."
	case "locked":
		// While the Mac's screen is locked no app's window can be read. A
		// sleeping display is woken for that, but a lock with a password is
		// not opened by it, so a logout stays until a person unlocks it
		// (2026-10-23: eleven hours).
		v.State, v.Summary = checks.Fail, "카톡이 로그아웃됐는데 맥미니 화면이 비밀번호로 잠겨 있어 다시 로그인하지 못해요. 화면 잠금을 풀어 주세요."
	case "locked-in":
		v.State, v.Summary = checks.Warn, "카톡은 로그인돼 있지만 맥미니 잠금에 비밀번호가 걸려 있어요. 로그아웃되면 저절로 다시 로그인하지 못해요(시스템 설정 → 잠금 화면)."
	case "locked-unknown":
		v.State, v.Summary = checks.Warn, "맥미니 화면이 잠겨 있어 카톡 로그인 상태를 볼 수 없어요."
	case "unreadable":
		v.State, v.Summary = checks.Warn, "카톡 창을 읽지 못해요. 로그아웃되면 저절로 다시 로그인하지 못할 수 있어요."
	case "unreadable-out":
		v.State, v.Summary = checks.Fail, "카톡이 로그아웃됐는데 창을 읽지 못해 다시 로그인하지 못해요. 맥미니 화면을 한 번 깨워 주세요."
	case "no-access":
		v.State, v.Summary = checks.Fail, "자동 로그인 프로그램에 손쉬운 사용 권한이 없어요."
	case "no-password":
		v.State, v.Summary = checks.Fail, "자동 로그인에 쓸 비밀번호가 저장돼 있지 않아요."
	default:
		v.State, v.Summary = checks.Warn, "카톡 화면을 알아보지 못했어요."
	}
	return v
}
