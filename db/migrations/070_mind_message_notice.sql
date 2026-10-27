-- jarvis 대화방에서 비서가 스스로 보낸 알림(작업 소식, 울린 리마인드, 띄운 체크리스트 - chat-svc 의
-- turns.origin 이 있는 턴)을 표시한다. 그동안 mind 의 chat 센서는 이 턴들을 'Jarvis(비서)'가
-- 대화에서 한 말로 읽어, mind 가 보낸 알림을 다시 해석기에 넣고 있었다(자기 출력 되읽기). 이제
-- 알림은 맥락으로만 보이고 근거로는 인용되지 않는다(해석기 규칙과 코드의 근거 검사).
alter table mind_messages add column if not exists notice boolean not null default false;

-- 이미 들어온 알림도 표시한다: 그 턴들은 chat-svc 의 turns 에 origin 이 있다.
update mind_messages m
   set notice = true, sender = 'Jarvis(알림)'
  from turns t
 where m.ref = 'chat:' || t.id and t.origin is not null and not m.notice;
