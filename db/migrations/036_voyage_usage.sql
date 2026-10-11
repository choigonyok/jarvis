-- 검색용 Voyage API(MongoDB Atlas Embedding and Reranking API) 사용량.
-- Atlas 에는 키의 사용량을 읽는 API 가 없어서(화면과 CSV 뿐) 응답이 알려 주는
-- 토큰 수를 날짜·모델별로 센다. 금액은 memory-svc 가 정가로 계산한다.
create table if not exists voyage_usage (
  day    date   not null,
  model  text   not null,
  tokens bigint not null default 0,
  calls  int    not null default 0,
  primary key (day, model)
);
