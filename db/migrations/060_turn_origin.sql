-- 턴이 어디서 왔는가. 대화 세션이 아니라 백그라운드 작업(job)의 실행이 남긴
-- 소식이면 origin 에 {"kind":"job","job":<id>,"run":<id>,"title":...} 가 든다.
--
-- 콘솔은 이 턴을 접힌 카드로 그리고(대화 흐름에 끼어드는 글이 아니라 옆에
-- 쌓이는 소식), 같은 실행의 후속 소식은 새 턴이 아니라 그 턴을 갱신한다
-- (chat-svc PUT /turns/{id}). 대화 모델은 이 턴을 세션 안에서 본 적이 없으므로
-- 에이전트가 다음 턴 앞에 "작업 소식"으로 붙인다.
alter table turns add column if not exists origin jsonb;

create index if not exists turns_origin_run_idx
  on turns ((origin->>'kind'), ((origin->>'run')::bigint)) where origin is not null;
