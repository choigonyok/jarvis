-- 운영자가 자비스의 답에 남긴 표시(좋아요·틀렸어요와 이유). 원본은 기록 chat.feedback(지속층)이고, 이 칸은
-- 화면이 턴과 함께 읽는 마지막 표시다. input-svc 가 기록과 함께 한 트랜잭션에 쓴다(svc_input 은 이미 update 권한이 있다).
alter table input.turns add column if not exists feedback jsonb;
