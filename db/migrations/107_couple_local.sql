-- The couple's space, kept here: the calendar and every block of the 일정 칸
-- (calendar-svc, CALENDAR_BACKEND=local). The tables have the shape uniple
-- gave them - the same names and columns - so calendar-svc's mapping from
-- rows to what the screens read stays one piece of code, and moving off
-- uniple (calendar-svc import-uniple) is a copy, table by table, ids and all:
-- what mind and the agent hold of an entry (calendarId) still points at it.
--
-- What uniple's server did on its own is done by calendar-svc: ids and
-- timestamps (defaults here), the news of what each did, the D-day's entries
-- on the calendar, the album's latest photo, the photos' bytes (couple.media).

create schema if not exists couple;

-- The two people. side says which of jarvis's two consoles each one is:
-- the operator's (owner) and the guest's.
create table couple.profiles (
  id         uuid primary key,
  space_id   uuid,
  nickname   text,
  created_at timestamptz not null default now()
);

create table couple.sides (
  side    text primary key check (side in ('owner', 'guest')),
  user_id uuid not null unique references couple.profiles (id)
);

create table couple.spaces (
  id               uuid primary key,
  anniversary_date date,
  week_start       text not null default 'sun',
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);

create table couple.blocks (
  id          uuid primary key default gen_random_uuid(),
  space_id    uuid not null references couple.spaces (id),
  type        text not null,
  title       text not null default '',
  order_index int not null default 0,
  config      jsonb,
  is_default  boolean not null default false,
  archived_at timestamptz,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create table couple.calendar_events (
  id                       uuid primary key default gen_random_uuid(),
  block_id                 uuid references couple.blocks (id) on delete cascade,
  space_id                 uuid,
  title                    text not null,
  start_date               date not null,
  end_date                 date,
  start_time               time,
  end_time                 time,
  memo                     text,
  created_by               uuid,
  owner_user_id            uuid,
  recurrence               text,
  recurrence_from          date,
  recurrence_until         date,
  recurrence_parent_id     uuid,
  recurrence_override_date date,
  recurrence_exdates       date[] not null default '{}',
  reminder_minutes         int,
  reminder_tz              text,
  -- origin is the block that wrote the entry (anniversary, milestone);
  -- origin_key the one thing of it the entry stands for, so writing them
  -- again keeps each entry's id.
  origin                   text,
  origin_key               text,
  created_at               timestamptz not null default now(),
  updated_at               timestamptz not null default now()
);
create index on couple.calendar_events (block_id, start_date);
create unique index on couple.calendar_events (space_id, origin_key) where origin_key is not null;

create table couple.dday_items (
  id               uuid primary key default gen_random_uuid(),
  block_id         uuid not null references couple.blocks (id) on delete cascade,
  space_id         uuid,
  title            text not null,
  date             date not null,
  is_start_date    boolean not null default false,
  recurrence       text,
  reminder_minutes int,
  reminder_tz      text,
  created_by       uuid,
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);

create table couple.bucket_items (
  id           uuid primary key default gen_random_uuid(),
  block_id     uuid not null references couple.blocks (id) on delete cascade,
  space_id     uuid,
  title        text not null,
  is_completed boolean not null default false,
  order_index  int not null default 0,
  created_by   uuid,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

create table couple.free_items (
  id          uuid primary key default gen_random_uuid(),
  block_id    uuid not null references couple.blocks (id) on delete cascade,
  space_id    uuid,
  content     text not null,
  order_index int not null default 0,
  created_by  uuid,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create table couple.workout_checks (
  id         uuid primary key default gen_random_uuid(),
  block_id   uuid not null references couple.blocks (id) on delete cascade,
  space_id   uuid,
  user_id    uuid not null,
  date       date not null,
  memo       text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (block_id, user_id, date)
);

create table couple.map_records (
  id          uuid primary key default gen_random_uuid(),
  block_id    uuid not null references couple.blocks (id) on delete cascade,
  space_id    uuid,
  region_code text not null,
  title       text,
  date_start  date,
  date_end    date,
  note        text,
  order_index int not null default 0,
  created_by  uuid,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create table couple.map_visits (
  id            uuid primary key default gen_random_uuid(),
  block_id      uuid not null references couple.blocks (id) on delete cascade,
  space_id      uuid,
  region_code   text not null,
  asset_id      uuid,
  clip_asset_id uuid,
  note          text,
  is_rep        boolean not null default false,
  rotation      double precision not null default 0,
  order_index   int not null default 0,
  record_id     uuid,
  created_by    uuid,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

create table couple.worldmap_records (
  id           uuid primary key default gen_random_uuid(),
  block_id     uuid not null references couple.blocks (id) on delete cascade,
  space_id     uuid,
  country_code text not null,
  city         text,
  date_start   date,
  date_end     date,
  note         text,
  order_index  int not null default 0,
  created_by   uuid,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

create table couple.worldmap_visits (
  id            uuid primary key default gen_random_uuid(),
  block_id      uuid not null references couple.blocks (id) on delete cascade,
  space_id      uuid,
  country_code  text not null,
  asset_id      uuid,
  clip_asset_id uuid,
  rotation      double precision not null default 0,
  order_index   int not null default 0,
  record_id     uuid,
  created_by    uuid,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

create table couple.album_photos (
  id          uuid primary key default gen_random_uuid(),
  block_id    uuid not null references couple.blocks (id) on delete cascade,
  space_id    uuid,
  folder_id   uuid,
  uploaded_by uuid,
  taken_at    timestamptz,
  sort_at     timestamptz not null default now(),
  width       int,
  height      int,
  media_type  text not null default 'photo',
  duration_ms int,
  asset_id    uuid,
  is_snap     boolean not null default false,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create table couple.calendar_day_photos (
  id         uuid primary key default gen_random_uuid(),
  block_id   uuid not null references couple.blocks (id) on delete cascade,
  space_id   uuid,
  date       date not null,
  asset_id   uuid not null,
  event_id   uuid,
  is_cover   boolean not null default false,
  sort_order int not null default 0,
  created_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index on couple.calendar_day_photos (block_id, date);

-- The blocks this console shows but does not write (문화·맛집 기록장, 일기장,
-- 데이트 가계부): kept as they came.
create table couple.log_entries (
  id              uuid primary key default gen_random_uuid(),
  block_id        uuid not null references couple.blocks (id) on delete cascade,
  space_id        uuid,
  category        text,
  title           text,
  rating          double precision,
  note            text,
  photo_asset_ids text[],
  card_asset      text,
  experienced_on  date,
  experienced_end date,
  created_by      uuid,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create table couple.food_entries (
  id              uuid primary key default gen_random_uuid(),
  block_id        uuid not null references couple.blocks (id) on delete cascade,
  space_id        uuid,
  meal_type       text,
  title           text,
  rating          double precision,
  note            text,
  tags            jsonb,
  photo_asset_ids text[],
  card_asset      text,
  experienced_on  date,
  created_by      uuid,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create table couple.diary_entries (
  id              uuid primary key default gen_random_uuid(),
  block_id        uuid not null references couple.blocks (id) on delete cascade,
  space_id        uuid,
  author_id       uuid,
  entry_at        timestamptz not null default now(),
  title           text,
  body            text,
  photo_asset_ids text[],
  comment_count   int not null default 0,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create table couple.diary_comments (
  id         uuid primary key default gen_random_uuid(),
  entry_id   uuid not null references couple.diary_entries (id) on delete cascade,
  author_id  uuid,
  body       text,
  created_at timestamptz not null default now()
);

create table couple.budget_dates (
  id            uuid primary key default gen_random_uuid(),
  block_id      uuid not null references couple.blocks (id) on delete cascade,
  space_id      uuid,
  title         text,
  start_on      date not null,
  end_on        date,
  total_amount  double precision,
  expense_count int not null default 0,
  top_category  text,
  memo          text,
  created_by    uuid,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

create table couple.budget_expenses (
  id         uuid primary key default gen_random_uuid(),
  date_id    uuid not null references couple.budget_dates (id) on delete cascade,
  amount     double precision,
  category   text,
  paid_by    text,
  memo       text,
  created_by uuid,
  created_at timestamptz not null default now()
);

-- What each did in the space, for the other to read (소식). actor_id is who did
-- it; each reader sees the other's as the partner's.
create table couple.notifications (
  id         uuid primary key default gen_random_uuid(),
  space_id   uuid,
  actor_id   uuid,
  block_id   uuid,
  block_type text,
  kind       text not null default '',
  item_id    text,
  event_time text,
  created_at timestamptz not null default now()
);
create index on couple.notifications (created_at desc);

-- Up to when each person has read the news: what came after is new, on every
-- device they read it on.
create table couple.news_seen (
  user_id uuid primary key,
  seen_at timestamptz not null
);

-- The photos' bytes. display is what was put (the screens send it at most 1080
-- on the long side); thumb is made from it for lists and grids.
create table couple.media (
  id         uuid primary key,
  kind       text not null default 'image',
  mime       text not null,
  display    bytea not null,
  thumb      bytea,
  thumb_mime text,
  width      int,
  height     int,
  created_by uuid,
  created_at timestamptz not null default now()
);

grant usage on schema couple to svc_calendar;
-- truncate: calendar-svc import-uniple clears the space before it copies it in.
grant select, insert, update, delete, truncate on all tables in schema couple to svc_calendar;
