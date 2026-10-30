-- 화면의 쓰기도 입구로 들어온다(to-be 6). 입구는 명령을 기록으로 남기고 mind 의 결정적 레인이 한 결과를 읽어 답한다.
-- mind 가 내려가 있어도 쓰기는 기록으로 남고, 올라오면 처리된다.
grant usage on schema mind to svc_input;
grant select on mind.commands to svc_input;
