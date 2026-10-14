-- 발생(occurrence)과 링크. 구조화 소스(가계부·사진·운동·캘린더·이체)에서 규칙으로 읽은
-- "일어난 일"과, 일 사이의 관계(이 결제는 그 데이트 중에, 이 장작은 그 캠핑을 위해).
-- 원본은 각 서비스에 있다: 이 표들은 색인이고, ref 가 원본을 가리킨다. 원문 내용은 두지
-- 않는다(가맹점 이름, 상품 이름, 사진 장소 정도). 감지기를 처음부터 다시 돌리면 다시 생긴다.
create table if not exists mind_occurrences (
  id    text primary key,                -- spending:812, spending:812#1, photo:<asset> …
  kind  text not null,                   -- purchase | purchase.item | photo | workout | calendar | transfer
  at    timestamptz not null,
  body  jsonb not null                   -- model.Occurrence 전체
);
create index if not exists mind_occurrences_at on mind_occurrences (at);
create index if not exists mind_occurrences_kind on mind_occurrences (kind, at);

-- 링크: from 이 to 와 rel 관계다. 처음엔 estimated(추정), 증거가 쌓이면 오르고, 사람이
-- 확인하면 confirmed, 아니라고 하면 rejected. 제안의 근거로는 confirmed 이거나 반복으로
-- 지지되는 것만 쓴다.
create table if not exists mind_links (
  from_id    text not null,
  to_id      text not null,
  rel        text not null,              -- during | prep_for | used_during | with | after | replaces
  state      text not null,              -- estimated | confirmed | rejected
  body       jsonb not null,             -- model.Link 전체(신뢰도, 근거, 누가)
  updated_at timestamptz not null,
  primary key (from_id, to_id, rel)
);
create index if not exists mind_links_to on mind_links (to_id);
create index if not exists mind_links_updated on mind_links (updated_at);
