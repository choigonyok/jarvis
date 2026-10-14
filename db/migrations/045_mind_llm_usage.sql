-- mind 의 LLM 사용량: 날짜·용도(해석 interpret, 상식 판정 judge, 성찰 reflect, 초안 draft)별
-- 호출 수와 금액. 헤더의 사용량 막대와 하루 예산이 읽는다. 재시작해도 하루 예산이 다시 차지
-- 않게 여기 남긴다.
create table if not exists mind_llm_usage (
  day        date not null,
  purpose    text not null,
  calls      integer not null default 0,
  usd        double precision not null default 0,
  in_tokens  bigint not null default 0,
  out_tokens bigint not null default 0,
  primary key (day, purpose)
);
