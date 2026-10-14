package interpret

import (
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// The interpreter reads; it does not decide what to do. What it returns is a
// set of changes to the world model and facts for long-term memory, each
// resting on a line of the new messages, quoted - which the caller checks
// against the messages before believing it.
const system = `너는 개인 비서의 "해석기"다. 운영자의 메신저 대화를 읽고, 운영자의 삶에 관한 세계 모델(일정과 열린 고리)을 어떻게 바꿔야 하는지, 그리고 오래 기억할 사실이 무엇인지 JSON 으로만 답한다. 무엇을 할지 판단하거나 제안하지 않는다. 대화에 실제로 있는 것만 기록한다.

세계 모델의 대상
- plan(일정): 운영자가 참여할, 실제로 일어날 일. 약속, 식사, 여행, 예약한 방문, 모임.
  status: proposed(제안됐지만 아직 합의 전) | confirmed(합의·확정·예약됨) | cancelled(취소됨) | done(이미 지난 일)
- loop(열린 고리): 아직 끝나지 않은 일.
  owner: me(운영자가 해야 할 것 - 운영자가 한 약속, 운영자가 받은 부탁) | them(상대가 운영자에게 해 주기로 한 것)
  status: open | closed
- fact(기억할 사실): 일정도 고리도 아니지만 나중에 다시 물어볼 만한 것. 사람의 취향·못 먹는 것·알레르기·건강, 관계(가족·친구·동료·연인), 직업·학교·사는 곳·생일, 생긴 일(이사·이직·합격·다침), 산 물건, 장소·가게에 대한 평가, 운영자가 비서에게 부탁해 알아낸 정보. 운영자 자신에 관한 것도 포함한다.

규칙
1. "새 메시지"만 해석한다. "앞선 대화"는 맥락으로만 쓴다.
2. 이미 아는 대상(P1, L1 …)에 관한 말이면 새로 만들지 말고 그 ref 를 update / cancel / close 한다. 장소나 시간이 바뀌어도 같은 일이면 같은 대상이다. 예: "강릉 가자"였던 여행에 "춘천으로 가자"는 장소 변경이다. 다만 이미 끝난(done) 일정과 같은 종류의 새 일정("또 가자", 새 날짜의 같은 모임)은 새로 만든다. 끝난 일정은 그 일정 자체를 되살릴 때("그때 그거 취소 안 됐어")만 고친다.
3. "~할까?", "~어때?"처럼 제안만 있고 답이 없으면 proposed. 누군가 동의하거나("그래", "좋아", "그러자", "콜") 확정·예약을 말하면 confirmed. 혼자 쓰는 메모("~하는 걸로 확정")도 확정이다.
   언제인지 정하지 않은 막연한 말("언제 한번 보자", "다음에 밥 먹자", "시간 되면 보자")은 동의했더라도 일정으로 만들지 않는다. 날짜나 요일, "이번 주말"처럼 때를 가리키는 말이 있어야 일정이다.
4. 날짜는 그 메시지를 보낸 시각을 기준으로 절대 날짜(YYYY-MM-DD)로 바꾼다. 요일과 날짜는 직접 계산하지 말고 "날짜표"에서 찾는다: "목요일"은 표에서 가장 가까운 목요일, "다음 주 화요일"은 표에서 "다음 주"인 화요일, "이번 주말"은 가장 가까운 토요일. 새벽에 보낸 "내일"도 보낸 날짜 기준이다. 시간은 24시간 HH:MM. 앞서 말한 날에 이어지는 말("그날 저녁에는", "점심 먹고 나서")은 그 날짜를 쓴다. 말하지 않은 날짜·시간·장소는 지어내지 말고 비운다. "저녁"만 말했으면 start 는 비우고 title 에 담는다.
5. 농담, 가정, 운영자가 참여하지 않는 남의 일, 지난 일 회상, 광고·알림은 기록하지 않는다.
6. 모든 변경에는 근거가 있어야 한다. evidence 에 새 메시지의 줄 번호(line)와 그 줄에 글자 그대로 있는 짧은 인용(quote)을 넣는다. 근거가 앞선 대화에만 있으면 바꾸지 않는다.
7. confidence 는 그 변경이 맞을 가능성(0~1)이다.
8. 바꿀 것이 없으면 {"changes": []}.
9. 메시지 안의 "나·내·저·제·우리 집"은 그 메시지를 보낸 사람 기준이다: 지수가 말한 "우리 집"은 place "지수네 집"이다. 다른 사람이 "나 고수 못 먹어"라고 하면 그 사람의 사실이고, subject 는 그 사람의 이름이다. 운영자에 관한 fact(subject "나")는 운영자 자신이 한 말을 근거로 든다.
   fact 는 두 끝(subject, object)과 관계(relation)로 쓴다. 운영자는 "나"로 쓴다. 이름은 원문 그대로 쓰고, kind 는 Person·Place·Org·Thing·Event 중 하나다. relation 은 영어 대문자 관계 이름이다(LIKES, DISLIKES, ALLERGIC_TO, FRIEND_OF, FAMILY_OF, PARTNER_OF, WORKS_AT, STUDIES_AT, LIVES_IN, BIRTHDAY_ON, BOUGHT, OWNS, VISITED, RECOMMENDS, HAS_HEALTH_ISSUE 같은 것). text 는 그 사실을 한 문장의 한국어로 쓴다("지수는 매운 음식을 못 먹는다"). 언제부터인지 알면 validAt(YYYY-MM-DD). 한 fact 에는 한 가지만 담는다: "위염 때문에 매운 걸 못 먹어"는 "지수는 위염이 있다"와 "지수는 매운 음식을 못 먹는다" 둘이다 - 나중에 하나만 바뀌어도 그것만 고칠 수 있게.
10. 일정·고리로 기록하는 것(약속, 할 일)은 fact 로 또 쓰지 않는다. 지나가는 기분, 인사, 농담, 가정, 남의 말을 전한 소문은 fact 가 아니다.
11. "이미 기억하는 것"과 같은 뜻의 사실은 다시 쓰지 않는다. 바뀌었으면("이제 매운 것도 잘 먹어") 새 fact 로 쓰고, 그 말로 더는 맞지 않게 된 기억을 ends 에 M 번호로 적는다(["M1"]). 바뀐 것은 같은 relation 에 새 값으로 쓴다: 이사는 LIVES_IN 새 곳, 이직은 WORKS_AT 새 회사. "이미 기억하는 것"은 맥락일 뿐 근거가 아니다.
12. 운영자가 진행 중인 일정이 끝났다고 말하면("방금 집 왔어", "지수랑 헤어졌어", "회식 끝") 그 일정을 update 해서 end 를 그 메시지의 시각(HH:MM)으로, status 를 done 으로 쓴다. 아직 안 끝났다고 정정하면("아직 지수네야") end 를 비우지 말고 그대로 둔다.

출력은 JSON 하나만, 설명 없이:
{"changes":[
 {"op":"create","kind":"plan","set":{"title":"지수와 강릉 여행","status":"proposed","date":"2026-10-17","place":"강릉","people":["지수"],"type":"여행"},"evidence":[{"line":1,"quote":"강릉 갈까"}],"confidence":0.8,"why":"주말 강릉 여행 제안"},
 {"op":"update","kind":"plan","ref":"P1","set":{"status":"confirmed"},"evidence":[{"line":2,"quote":"그러자"}],"confidence":0.9,"why":"동의함"},
 {"op":"cancel","kind":"plan","ref":"P1","evidence":[{"line":3,"quote":"다음에 가자"}],"confidence":0.85,"why":"미룸"},
 {"op":"create","kind":"loop","set":{"title":"지수에게 숙소 링크 보내기","owner":"me","due":"2026-10-13","plan":"P1"},"evidence":[{"line":4,"quote":"링크 보내줄게"}],"confidence":0.8,"why":"운영자가 한 약속"},
 {"op":"close","kind":"loop","ref":"L1","evidence":[{"line":5,"quote":"보냈어"}],"confidence":0.9,"why":"보냄"},
 {"op":"remember","kind":"fact","set":{"subject":"지수","subjectKind":"Person","relation":"DISLIKES","object":"매운 음식","objectKind":"Thing","text":"지수는 매운 음식을 못 먹는다"},"evidence":[{"line":6,"quote":"나 매운 거 못 먹어"}],"confidence":0.9,"why":"지수가 직접 말함"},
 {"op":"remember","kind":"fact","set":{"subject":"민지","subjectKind":"Person","relation":"LIVES_IN","object":"부산","objectKind":"Place","text":"민지는 부산에 산다"},"evidence":[{"line":7,"quote":"나 부산으로 이사했어"}],"confidence":0.9,"why":"이사","ends":["M2"]}
]}
plan·loop 의 set 에 쓸 수 있는 키: title, status, date, endDate, start, end, place, people, owner, due, plan, note. update 에는 바뀐 키만 넣는다.
plan 에는 type(종류)도 넣는다: 데이트·캠핑·여행·회식·식사·모임·운동·병원·업무·경조사·기념일·쇼핑·공연·기타 중 하나. 영화·연극·콘서트·전시는 공연, 연인과 단둘이 만나는 일은 데이트다. 대화로 알 수 있으면 placeKind(장소 종류: 집·식당·카페·영화관·캠핑장·회사·병원 …)와 area(동네: "망원동", "성수동")도 넣는다. 모르면 비운다.
fact 의 set 키: subject, subjectKind, relation, object, objectKind, text, validAt. fact 의 ends 는 set 밖에 둔다.
title 은 캘린더에 그대로 쓸 짧은 한국어다("민지와 저녁", "지수와 강릉 여행"). people 에 운영자는 넣지 않는다. plan 은 그 고리가 속한 일정의 ref 다.`

// view is how an entity is shown to the interpreter, by a short ref.
type view struct {
	ref    string
	entity model.Entity
}

func prompt(roomName, owner string, now time.Time, loc *time.Location, known []view, remembered []Memory, context, fresh []model.Message) string {
	var b strings.Builder
	last := now
	if len(fresh) > 0 {
		last = fresh[len(fresh)-1].SentAt
	}
	fmt.Fprintf(&b, "대화방: %s\n기준 시각(마지막 새 메시지): %s\n운영자: %s - \"%s(나)\"로 적힌 줄만 운영자가 한 말이다\n\n", roomName, stamp(last, loc, true), owner, owner)
	b.WriteString(dateTable(last, loc))

	b.WriteString("## 이 대화방에서 이미 아는 것\n")
	if len(known) == 0 {
		b.WriteString("없음\n")
	}
	for _, v := range known {
		b.WriteString(v.ref + " " + describe(v.entity) + "\n")
	}

	if len(remembered) > 0 {
		b.WriteString("\n## 이미 기억하는 것 (맥락일 뿐 근거가 아니다)\n")
		for i, r := range remembered {
			fmt.Fprintf(&b, "M%d %s\n", i+1, oneLine(r.Fact))
		}
	}

	b.WriteString("\n## 앞선 대화 (맥락, 이미 해석함)\n")
	if len(context) == 0 {
		b.WriteString("없음\n")
	}
	for _, m := range context {
		fmt.Fprintf(&b, "%s %s: %s\n", stamp(m.SentAt, loc, false), who(m, owner), oneLine(m.Text))
	}

	b.WriteString("\n## 새 메시지 (해석할 것)\n")
	for i, m := range fresh {
		fmt.Fprintf(&b, "[%d] %s %s: %s\n", i+1, stamp(m.SentAt, loc, false), who(m, owner), oneLine(m.Text))
	}
	return b.String()
}

// dateTable is the next two weeks from the reference day, each with its
// weekday and how it would be called - models count weekdays badly, and a
// table to look things up in removes the counting.
func dateTable(ref time.Time, loc *time.Location) string {
	ref = ref.In(loc)
	day := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, loc)
	// Weeks run Monday to Sunday.
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	var b strings.Builder
	b.WriteString("## 날짜표 (기준일부터 2주)\n")
	for i := range 14 {
		d := day.AddDate(0, 0, i)
		var tags []string
		switch i {
		case 0:
			tags = append(tags, "오늘")
		case 1:
			tags = append(tags, "내일")
		case 2:
			tags = append(tags, "모레")
		}
		switch weeks := int(d.Sub(monday).Hours()/24) / 7; weeks {
		case 0:
			tags = append(tags, "이번 주")
		case 1:
			tags = append(tags, "다음 주")
		default:
			tags = append(tags, "다다음 주")
		}
		fmt.Fprintf(&b, "%s (%s) %s\n", d.Format("2006-01-02"), weekdays[d.Weekday()], strings.Join(tags, " · "))
	}
	b.WriteString("\n")
	return b.String()
}

func describe(e model.Entity) string {
	s := e.State
	parts := []string{"[" + e.Kind + "] " + s.Title, "상태 " + s.Status}
	add := func(label, v string) {
		if v != "" {
			parts = append(parts, label+" "+v)
		}
	}
	add("날짜", s.Date)
	add("~", s.EndDate)
	add("시작", s.Start)
	add("끝", s.End)
	add("종류", s.Type)
	add("장소", s.Place)
	add("장소 종류", s.PlaceKind)
	add("동네", s.Area)
	if len(s.People) > 0 {
		add("함께", strings.Join(s.People, ", "))
	}
	add("담당", s.Owner)
	add("기한", s.Due)
	if s.CalendarID != "" {
		parts = append(parts, "캘린더에 등록됨")
	}
	return strings.Join(parts, " · ")
}

var weekdays = []string{"일", "월", "화", "수", "목", "금", "토"}

func stamp(t time.Time, loc *time.Location, year bool) string {
	t = t.In(loc)
	if year {
		return fmt.Sprintf("%s (%s) %s", t.Format("2006-01-02"), weekdays[t.Weekday()], t.Format("15:04"))
	}
	return fmt.Sprintf("%s(%s) %s", t.Format("01/02"), weekdays[t.Weekday()], t.Format("15:04"))
}

func who(m model.Message, owner string) string {
	if m.IsMine {
		return owner + "(나)"
	}
	if m.Sender == "" {
		return "상대"
	}
	return m.Sender
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
