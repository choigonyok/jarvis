-- 날씨는 context-svc 가 읽어 기록으로 넘긴다(to-be 4d). mind 는 바깥에 나가지 않는다.
update input.consumers set kinds = kinds || array['weather.forecast'] where name = 'mind-structured';
