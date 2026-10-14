-- 날짜로 일정 찾기: 결제·사진 하나가 들어올 때 그날 근처 일정만 읽는다(일정 전체를
-- 훑지 않는다).
create index if not exists mind_entities_plan_date on mind_entities ((state->>'date')) where kind = 'plan';
