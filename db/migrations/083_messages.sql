-- 수집기가 메시지를 봉인해서 넘긴다(to-be 4d). 기록이 먼저 쌓여도 mind 가 뜨면 차례로 읽도록 소비자에 미리 적어 둔다.
update input.consumers set kinds = kinds || array['message.received'] where name = 'mind-structured';
