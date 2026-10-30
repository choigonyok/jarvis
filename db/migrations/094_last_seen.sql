-- 상태 탭이 읽는 것(to-be 6). 소스별 마지막 기록과 서비스가 남긴 점검의 최신 값이다.
-- 기록을 고른 투영이라 원천이 아니고, 판단이 없다. status-svc 는 기능별 코드 없이 이것을 읽는다.
create table input.last_seen (
  kind   text not null,
  source text not null,
  at     timestamptz not null,
  -- 같은 내용을 다시 말한 것은 소식이 아니다: 어댑터는 재시작하면 첫 읽기를 한 번 더 낸다.
  sum    text not null default '',
  primary key (kind, source)
);
create table input.checks (
  id   text primary key,
  at   timestamptz not null,
  body jsonb not null
);
grant select, insert, update on input.last_seen, input.checks to svc_input;

insert into input.last_seen (kind, source, at)
select kind, source, max(received_at) from input.records group by 1, 2;

-- 상태 서비스는 이제 표를 읽지 않는다. 판정은 그 일을 볼 수 있는 서비스가 기록으로 남긴다.
revoke all on mind.card_transactions from svc_status;
revoke all on portfolio_snapshots from svc_status;
revoke select (id, updated_at) on uniple_session from svc_status;
