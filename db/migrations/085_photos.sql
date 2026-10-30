-- 사진은 photos-svc 가 읽어 기록으로 넘긴다(to-be 4d).
update input.consumers set kinds = kinds || array['photos.recent', 'photos.days'] where name = 'mind-structured';
