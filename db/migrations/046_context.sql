-- context-svc: 지금 어디에 있고 어떤 상태인지. 폰의 OwnTracks(위치·영역 진입/이탈), iOS 단축어
-- (CarPlay·집중 모드·충전…), 맥 수집기(앞에 있는 앱·클립보드 패턴)가 보낸 것.
--
-- 원본은 context_raw 에 그대로 남지만 공용 raw_events 가 아니다: 좌표 원본은 오래 두지 않기로
-- 했고(기본 30일, CONTEXT_RAW_DAYS), raw_events 는 추가 전용이라 지울 수 없다. 체류(visits)와
-- 현재 상태는 원본에서 만든 파생이고 오래 둔다.
create table if not exists context_raw (
  id          bigserial primary key,
  source      text not null,               -- owntracks | shortcut | mac | homeassistant
  kind        text not null,               -- location | transition | device.carplay …
  at          timestamptz not null,        -- 기기가 말한 시각
  received_at timestamptz not null default now(),
  body        jsonb not null
);
create index if not exists context_raw_at on context_raw (at);

-- 영역: 집·회사·마트·캠핑장 … 반경 안이면 그 영역에 있다.
create table if not exists context_regions (
  name     text primary key,
  kind     text not null default 'other',  -- home | work | camp | shop | food | other
  lat      double precision not null,
  lon      double precision not null,
  radius_m integer not null default 150,
  area     text not null default ''        -- 동네 (일정의 동네와 맞출 때)
);

-- 체류: 영역에 들어와서 나갈 때까지. 나가지 않았으면 left_at 이 비어 있다.
create table if not exists context_visits (
  id         bigserial primary key,
  region     text not null,
  entered_at timestamptz not null,
  left_at    timestamptz,
  source     text not null default ''      -- transition | inregions | fix
);
create index if not exists context_visits_open on context_visits (region) where left_at is null;
create index if not exists context_visits_entered on context_visits (entered_at);

-- 현재 상태 한 장: 키마다 최근 값 (location, owntracks.mode, device.carplay …).
create table if not exists context_state (
  key   text primary key,
  value jsonb not null,
  at    timestamptz not null
);
