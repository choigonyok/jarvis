-- mind 가 기억(memory-svc)에 넣지 못한 사실을 열두 번(약 하루) 뒤 영구히 포기하던 것(failed)을
-- 그만둔다(gocommon/health 의 규칙). 기억이 꺼졌거나 예산이 다 된 것은 이제 사실의 시도로 세지
-- 않고, 사실 하나만 계속 실패하면 보류(held)로 두었다가 기억이 돌아오면 한 번 더 보낸다. 버리는
-- 것(dropped)은 사람만 한다. 보류한 사실도 생활 장부(기한·집안일·이사)에는 남는다.
--
-- 지금까지 포기한 사실은 대개 기억이 멈췄던 동안의 것이라, 처음처럼 한 번 더 보낸다.
update mind_facts
   set status   = 'pending',
       next_try = now(),
       body     = jsonb_set(jsonb_set(jsonb_set(body, '{status}', '"pending"'), '{tries}', '0'), '{nextTry}', to_jsonb(now()))
 where status = 'failed';
