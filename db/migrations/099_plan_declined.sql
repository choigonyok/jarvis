-- 일정 카드를 반려하면 일정에 그 말이 남는다(state.declined). 그 전에 반려한, 아직 캘린더에 없는 일정도 같이 적어 둔다.
update mind_entities e
set state = e.state || jsonb_build_object('declined', coalesce(nullif(i.body->>'error', ''), '반려'))
from (
  select distinct on (entity_id) entity_id, status, body
  from mind_intents
  where kind = 'calendar.create' and entity_id <> '' and status in ('rejected', 'undone', 'executed')
  order by entity_id, created_at desc
) i
where e.id = i.entity_id
  and i.status <> 'executed'
  and e.kind = 'plan'
  and e.state->>'status' in ('proposed', 'confirmed')
  and coalesce(e.state->>'calendarId', '') = '';
