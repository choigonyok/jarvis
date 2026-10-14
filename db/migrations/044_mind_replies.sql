-- 답장 상황의 색인: "오랜만에 온 연락(공백 60일 넘게)에 내가 한 첫 답장" 같은 상황을 메시지가
-- 들어올 때 쌓아 둔다. 원문은 mind_messages 에 봉해진 채 있고 여기는 ref 만 둔다. 초안을 쓸 때
-- 비슷한 상황의 내 실제 답장을 예시로 꺼내려고 둔다 - 과거 대화 전체를 훑지 않으려고.
-- mind_messages 에서 다시 만들 수 있다(POST /api/replies/rebuild).
create table if not exists mind_reply_examples (
  id        bigserial primary key,
  kind      text not null,               -- reconnect
  room      text not null,
  in_ref    text not null,               -- 상대의 메시지
  out_ref   text not null default '',    -- 그다음 내 첫 답장 (아직 없으면 '')
  gap_days  integer not null,
  at        timestamptz not null,
  unique (kind, in_ref)
);
create index if not exists mind_reply_examples_open on mind_reply_examples (room) where out_ref = '';
create index if not exists mind_reply_examples_kind on mind_reply_examples (kind, at desc) where out_ref <> '';
