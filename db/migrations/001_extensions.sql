-- vector:    회수에 쓰는 임베딩. memory_chunks 하나에만 쓰인다.
-- btree_gist: 등호 비교(subject with =)를 범위 배제 제약에 섞기 위해 필요하다.
--             routine_facts 가 "한 주제에 한 시점 하나의 사실"을 이걸로 지킨다.
-- pg_trgm:   같은 사람을 가리키는 다른 표기("민수", "김민수", "민수형")를
--            찾기 위한 것. 정확 일치만으로는 카톡 표시이름을 따라갈 수 없다.
create extension if not exists vector;
create extension if not exists btree_gist;
create extension if not exists pg_trgm;
