package probes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

// hostFacts is what scripts/host-check writes on the Mac mini every ten
// minutes (~/.jarvis-host/host.json, mounted here read-only).
type hostFacts struct {
	CheckedAt   time.Time `json:"checkedAt"`
	FileVault   string    `json:"filevault"`   // On | Off | what fdesetup said
	TimeMachine bool      `json:"timemachine"` // a destination is set
	Mirror      string    `json:"mirror"`      // binary | ssh | ""
	LastBackup  string    `json:"lastBackup"`  // YYYY-MM-DD of the newest backup, "" = none
	FalkorAOF   string    `json:"falkordbAOF"` // CONFIG GET appendonly: yes | no | "" (not reached)
}

// readHost returns the facts, or the verdict to show when there are none to
// go on.
func (e *env) readHost() (hostFacts, *check.Verdict) {
	var h hostFacts
	raw, err := os.ReadFile(e.HostFile)
	if err != nil {
		return h, &check.Verdict{State: check.Unknown, Summary: "맥미니 점검 기록이 없어요. scripts/host-check install 이 필요해요."}
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, &check.Verdict{State: check.Unknown, Summary: "맥미니 점검 기록을 읽지 못했어요."}
	}
	if time.Since(h.CheckedAt) > time.Hour {
		at := h.CheckedAt
		return h, &check.Verdict{State: check.Unknown, Summary: "맥미니 점검이 한 시간 넘게 멈춰 있어요.", ObservedAt: &at, ObservedLabel: "마지막 점검"}
	}
	return h, nil
}

func (e *env) hostProbe(id, name, rule, fix string, judge func(hostFacts) check.Verdict) check.Probe {
	return check.Probe{
		ID: id, Group: "기반", Name: name, Rule: rule, Fix: fix,
		Every: 5 * time.Minute, MinGap: 10 * time.Second,
		Run: func(context.Context) check.Verdict {
			h, missing := e.readHost()
			if missing != nil {
				return *missing
			}
			v := judge(h)
			at := h.CheckedAt
			v.ObservedAt, v.ObservedLabel = &at, "맥미니에서 확인"
			return v
		},
	}
}

func (e *env) hostFileVault() check.Probe {
	return e.hostProbe("host.filevault", "맥미니 디스크 암호화",
		"FileVault 가 켜져 있으면 정상. 카톡·iMessage 수집기 DB, 미러, 백업은 평문 사본이라 디스크 암호화가 유일한 보호예요.",
		"맥미니의 시스템 설정 → 개인정보 보호 및 보안 → FileVault 를 켜세요. 복구 키는 맥미니 밖에 두세요. 켜면 정전 뒤 다시 켜질 때 맥미니 앞에서 암호를 넣어야 서비스가 떠요.",
		func(h hostFacts) check.Verdict {
			switch h.FileVault {
			case "On":
				return check.Verdict{State: check.OK, Summary: "켜져 있어요."}
			case "Off":
				if e.FileVaultOffAccepted {
					return check.Verdict{State: check.OK, Summary: "꺼져 있어요 - 무인 재시작을 위해 운영자가 끄기로 했어요(받아들인 위험).",
						Facts: []string{"평문 사본의 보호는 보존 기한(메시지 365·730일, 백업 14일)과 맥미니의 물리적 위치에 기대요."}}
				}
				return check.Verdict{State: check.Warn, Summary: "꺼져 있어요. 평문 사본과 백업이 암호화되지 않아요."}
			default:
				return check.Verdict{State: check.Warn, Summary: fmt.Sprintf("상태: %s", h.FileVault)}
			}
		})
}

func (e *env) hostTimeMachine() check.Probe {
	return e.hostProbe("host.timemachine", "백업의 바깥 사본(Time Machine)",
		"Time Machine 대상 디스크가 있으면 정상. 매일 백업은 맥미니 안(~/jarvis-backups)에 떠 두기만 해서, 맥미니 밖으로 옮기는 것은 Time Machine 이에요.",
		"맥미니에 외장 디스크를 연결하고 시스템 설정 → 일반 → Time Machine 에서 백업 디스크로 지정하세요.",
		func(h hostFacts) check.Verdict {
			if h.TimeMachine {
				return check.Verdict{State: check.OK, Summary: "대상 디스크가 있어요."}
			}
			return check.Verdict{State: check.Warn, Summary: "대상 디스크가 없어요. 백업이 맥미니 밖으로 나가지 않아요."}
		})
}

func (e *env) hostMirror() check.Probe {
	return e.hostProbe("host.mirror", "iMessage 미러 권한",
		"전용 바이너리가 직접 복사하면 정상. SSH 방식이면 원격 로그인의 전체 디스크 접근 권한에 기대고 있다는 뜻이에요.",
		"맥미니의 시스템 설정 → 개인정보 보호 및 보안 → 전체 디스크 접근 권한에 ~/.local/bin/jarvis-imessage-mirror 를 추가하세요. 5분 안에 저절로 바뀌어요. 그 뒤 sshd-keygen-wrapper 의 권한을 끄세요.",
		func(h hostFacts) check.Verdict {
			switch h.Mirror {
			case "binary":
				return check.Verdict{State: check.OK, Summary: "전용 바이너리가 직접 복사해요."}
			case "ssh":
				return check.Verdict{State: check.Warn, Summary: "아직 SSH 원격 로그인의 권한으로 복사해요."}
			default:
				return check.Verdict{State: check.Unknown, Summary: "미러 방식 기록이 없어요."}
			}
		})
}

func (e *env) hostBackup() check.Probe {
	return e.hostProbe("host.backup", "매일 백업",
		"오늘이나 어제 백업이 있으면 정상(매일 04:30).",
		"맥미니에서 scripts/backup status 로 LaunchAgent 와 로그를 보세요. 지금 뜨려면 scripts/backup run.",
		func(h hostFacts) check.Verdict {
			if h.LastBackup == "" {
				return check.Verdict{State: check.Fail, Summary: "백업이 없어요."}
			}
			day, err := time.ParseInLocation("2006-01-02", h.LastBackup, time.Local)
			if err != nil {
				return check.Verdict{State: check.Unknown, Summary: "백업 날짜를 읽지 못했어요."}
			}
			age := int(time.Since(day).Hours() / 24)
			if age <= 1 {
				return check.Verdict{State: check.OK, Summary: fmt.Sprintf("마지막 백업 %s.", h.LastBackup)}
			}
			return check.Verdict{State: check.Fail, Summary: fmt.Sprintf("마지막 백업이 %d일 전(%s)이에요.", age, h.LastBackup)}
		})
}

func (e *env) hostFalkorAOF() check.Probe {
	return e.hostProbe("host.falkordb", "기억 그래프 저장(AOF)",
		"FalkorDB 의 appendonly 가 yes 면 정상. 꺼져 있으면 마지막 스냅숏 뒤의 기억이 비정상 종료에 사라져요.",
		"docker-compose.yml 의 falkordb REDIS_ARGS(--appendonly yes)를 확인하고 falkordb 를 다시 띄우세요.",
		func(h hostFacts) check.Verdict {
			switch h.FalkorAOF {
			case "yes":
				return check.Verdict{State: check.OK, Summary: "켜져 있어요(매초 fsync)."}
			case "":
				return check.Verdict{State: check.Unknown, Summary: "FalkorDB 에 묻지 못했어요."}
			default:
				return check.Verdict{State: check.Fail, Summary: "꺼져 있어요. 마지막 스냅숏 뒤의 쓰기가 비정상 종료에 사라져요."}
			}
		})
}
