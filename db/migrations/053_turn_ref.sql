-- 대화의 참조(2번): 운영자가 이전 메시지를 밀어서 참조를 걸고 보낸 말에는, 무엇을
-- 참조했는지(그 턴의 id·누가·언제·앞부분)를 함께 적는다. 화면은 말풍선 위에 인용으로
-- 보여 주고, 에이전트는 모델에게 그 메시지를 붙여 넘긴다.
-- {"id": "a-…", "role": "agent", "at": "22:31", "text": "…"}
alter table turns add column if not exists ref jsonb;
