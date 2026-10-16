-- status-svc 는 uniple 세션이 언제 마지막으로 갱신됐는지만 본다(probes.go: updated_at).
-- 표 전체를 읽을 수 있으면 refresh_token·seed 까지 읽힌다 - 캘린더 계정을 계속 쓸 수 있는
-- 값이다. 그래서 그 두 열만 남긴다. 토큰 표를 읽고 쓰는 것은 각자 한 서비스뿐이다:
-- kis_tokens 는 svc_assets, uniple_session 은 svc_calendar.
revoke select on uniple_session from svc_status;
grant select (id, updated_at) on uniple_session to svc_status;
