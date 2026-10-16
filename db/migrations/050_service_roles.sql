-- 서비스마다 Postgres 역할을 따로 둔다(D-3). 지금까지는 모든 서비스가 소유자(jarvis)로
-- 붙어, 한 서비스의 SQL 이 다른 서비스의 표를 지우거나 고칠 수 있었다.
--
-- 역할은 여기서 비밀번호 없이 만든다(공개 저장소). 비밀번호는 scripts/db-roles 가 맥미니
-- .env 의 JARVIS_DB_PASSWORD_<서비스> 로 정해 넣는다 - 넣기 전에는 그 역할로 로그인할 수 없다.
-- 마이그레이션(db-migrate)은 계속 소유자로 돈다. 새 표를 만드는 마이그레이션은 그 표의
-- 권한도 같은 파일에서 준다.
--
-- 권한은 각 서비스 코드의 SQL 에서 뽑았다: 자기 표는 읽고 쓰고, 남의 표는 읽기만(status 의
-- 점검, notify·agent·mind 의 이벤트 읽기), 이벤트는 outbox 에 쓰기만.

do $$
declare r text;
begin
  foreach r in array array[
    'svc_agent', 'svc_assets', 'svc_calendar', 'svc_chat', 'svc_context', 'svc_events', 'svc_jobs', 'svc_memory', 'svc_mind', 'svc_notify', 'svc_spending', 'svc_status', 'svc_workout'
  ] loop
    if not exists (select 1 from pg_roles where rolname = r) then
      execute format('create role %I login', r);
    end if;
    execute format('grant connect on database %I to %I', current_database(), r);
    execute format('grant usage on schema public to %I', r);
    execute format('grant usage, select on all sequences in schema public to %I', r);
  end loop;
end $$;

-- agent
grant insert on event_outbox to svc_agent;
grant select on events to svc_agent;

-- assets
grant insert on event_outbox to svc_assets;
grant execute on function upsert_snapshot to svc_assets;
grant select, insert, update, delete on kis_cash_daily to svc_assets;
grant select, insert, update, delete on kis_tokens to svc_assets;
grant select, insert, update, delete on portfolio_holdings to svc_assets;
grant select, insert, update, delete on portfolio_snapshots to svc_assets;
grant select, insert, update, delete on price_closes to svc_assets;
grant select, insert, update, delete on principal_flows to svc_assets;

-- calendar
grant select, insert, update, delete on commitments to svc_calendar;
grant insert on event_outbox to svc_calendar;
grant execute on function conflicts_for to svc_calendar;
grant select, insert, update, delete on uniple_session to svc_calendar;

-- chat
grant select, insert, update, delete on turns to svc_chat;

-- context
grant select, insert, update, delete on context_raw to svc_context;
grant select, insert, update, delete on context_regions to svc_context;
grant select, insert, update, delete on context_state to svc_context;
grant select, insert, update, delete on context_visits to svc_context;
grant insert on event_outbox to svc_context;

-- events
grant select, insert, update, delete on event_outbox to svc_events;
grant select, insert, update, delete on event_subscriptions to svc_events;
grant select, insert, update, delete on events to svc_events;

-- jobs
grant select, insert, update, delete on job_events to svc_jobs;
grant select, insert, update, delete on job_inbox to svc_jobs;
grant select, insert, update, delete on job_memory to svc_jobs;
grant select, insert, update, delete on job_records to svc_jobs;
grant select, insert, update, delete on job_runs to svc_jobs;
grant select, insert, update, delete on job_sites to svc_jobs;
grant select, insert, update, delete on jobs to svc_jobs;

-- memory
grant insert on event_outbox to svc_memory;
grant select, insert, update, delete on memory_diary to svc_memory;
grant select, insert, update, delete on memory_done to svc_memory;
grant select, insert, update, delete on memory_facts to svc_memory;
grant select, insert, update, delete on memory_state to svc_memory;
grant select, insert, update, delete on memory_usage to svc_memory;
grant select, insert, update, delete on voyage_usage to svc_memory;

-- mind
grant insert on event_outbox to svc_mind;
grant select on events to svc_mind;
grant select, insert, update, delete on mind_aliases to svc_mind;
grant select, insert, update, delete on mind_changes to svc_mind;
grant select, insert, update, delete on mind_cursors to svc_mind;
grant select, insert, update, delete on mind_entities to svc_mind;
grant select, insert, update, delete on mind_expectations to svc_mind;
grant select, insert, update, delete on mind_facts to svc_mind;
grant select, insert, update, delete on mind_intents to svc_mind;
grant select, insert, update, delete on mind_link_cache to svc_mind;
grant select, insert, update, delete on mind_links to svc_mind;
grant select, insert, update, delete on mind_llm_usage to svc_mind;
grant select, insert, update, delete on mind_messages to svc_mind;
grant select, insert, update, delete on mind_note_versions to svc_mind;
grant select, insert, update, delete on mind_notes to svc_mind;
grant select, insert, update, delete on mind_observations to svc_mind;
grant select, insert, update, delete on mind_occurrences to svc_mind;
grant select, insert, update, delete on mind_pattern_counts to svc_mind;
grant select, insert, update, delete on mind_policy_stats to svc_mind;
grant select, insert, update, delete on mind_predictions to svc_mind;
grant select, insert, update, delete on mind_preferences to svc_mind;
grant select, insert, update, delete on mind_records to svc_mind;
grant select, insert, update, delete on mind_reply_examples to svc_mind;
grant select, insert, update, delete on mind_rooms to svc_mind;
grant select, insert, update, delete on mind_thing_classes to svc_mind;
grant select, insert, update, delete on mind_timers to svc_mind;

-- notify
grant select on events to svc_notify;
grant select, insert, update, delete on notifications to svc_notify;
grant select, insert, update, delete on notify_settings to svc_notify;
grant select, insert, update, delete on push_subscriptions to svc_notify;

-- spending
grant select, insert, update, delete on card_transaction_items to svc_spending;
grant select, insert, update, delete on card_transactions to svc_spending;
grant insert on event_outbox to svc_spending;
grant execute on function set_setting to svc_spending;
grant select, insert, update, delete on merchant_rules to svc_spending;
grant select, insert, update, delete on raw_events to svc_spending;
grant select, insert, update, delete on settings to svc_spending;
grant select, insert, update, delete on spending_budgets to svc_spending;
grant select, insert, update, delete on spending_unparsed to svc_spending;

-- status
grant select on card_transactions to svc_status;
grant insert on event_outbox to svc_status;
grant select on portfolio_snapshots to svc_status;
grant select on uniple_session to svc_status;

-- workout
grant execute on function set_setting to svc_workout;
grant execute on function volume_by_group to svc_workout;
grant execute on function workout_gap_days to svc_workout;
grant select, insert, update, delete on routines to svc_workout;
grant select, insert, update, delete on settings to svc_workout;
grant select, insert, update, delete on workout_exercises to svc_workout;
grant select, insert, update, delete on workout_sessions to svc_workout;
grant select, insert, update, delete on workout_sets to svc_workout;
