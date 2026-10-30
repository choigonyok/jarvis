-- 가계부가 mind 로 녹는다(to-be 4e). 카드 알림은 스트림의 message.received 에서 구조화 레인이 읽고, 고치는 일은 명령으로
-- 들어와 결정적 레인이 한다. 표는 그대로 옮긴다. 옛 내역은 이 표가 원본이고, 새 내역은 기록에서 다시 만들 수 있다.
alter table public.card_transactions set schema mind;
alter table public.card_transaction_items set schema mind;
alter table public.spending_unparsed set schema mind;
alter table public.merchant_rules set schema mind;
alter table public.spending_budgets set schema mind;

grant select, insert, update, delete on mind.card_transactions, mind.card_transaction_items, mind.spending_unparsed,
  mind.merchant_rules, mind.spending_budgets to svc_mind;
grant usage on sequence mind.card_transactions_id_seq, mind.card_transaction_items_id_seq to svc_mind;

-- 마지막 카드 알림 시각은 이제 mind.latest 에 둔다. 상태 탭이 새 알림이 올 때까지 "받은 적 없음"으로 보이지 않게 옮겨 둔다.
insert into mind.latest (kind, payload, at)
select 'spending.alert', '{}', max(occurred_at) from public.raw_events
 where source = 'kakaotalk' and type = 'card.alert'
having max(occurred_at) is not null
on conflict (kind) do nothing;

delete from public.settings where scope = 'spending' and key = 'kakao_cursor';

drop owned by svc_spending;
drop role svc_spending;
