"""작업이 도는 동안 열어도 되는 사이트 - bridge.py 가 쓰고 guard.py 가 읽는다.

작업은 아무도 보지 않는 채로, 운영자의 쇼핑몰에 로그인된 이 브라우저에서 돈다.
그래서 작업 한 번이 도는 동안에는 그 작업의 사이트(와 같은 도메인의 다른 주소)
밖으로 가는 페이지 이동을 막는다 - 읽은 페이지가 어디로 가라고 하든.
"""

import json
import os
import time
from urllib.parse import urlparse

from publicsuffixlist import PublicSuffixList

FILE = os.environ.get("JOB_SITES_FILE", "/tmp/jarvis-job-sites.json")

# 사이트마다 쓰는 로그인 화면. 작업 지시가 "네이버 로그인 버튼으로" 가라고 한다.
LOGIN_HOSTS = [h.strip().lower() for h in os.environ.get("JOB_LOGIN_HOSTS", "nid.naver.com").split(",") if h.strip()]

# 공개 접미사 목록(co.kr, seoul.kr, github.io): 이 밑의 이름은 누구나 사서 쓰는 자리라, 한 단계 위로
# 올라가면 남의 도메인까지 열린다. 목록을 여기서 관리하지 않는다 - Go 쪽(gocommon/site)과 같은 PSL 이다.
_PSL = PublicSuffixList()


def _host(site: str) -> str:
    site = site.strip().lower()
    if "://" in site:
        site = urlparse(site).hostname or ""
    return site.split("/")[0].split(":")[0].strip(".")


def _domain(host: str) -> str:
    """www.coupang.com → coupang.com, web.joongna.com → joongna.com. 공개 접미사는 넘지 않는다."""
    parts = host.split(".")
    if len(parts) >= 3 and not _PSL.is_public(".".join(parts[1:])):
        return ".".join(parts[1:])
    return host


def allowed(url: str, sites: list[str]) -> bool:
    """작업 중 이 주소로 가도 되는가. http(s) 가 아닌 것(about:blank 등)은 막지 않는다."""
    u = urlparse(url)
    if u.scheme not in ("http", "https"):
        return True
    host = (u.hostname or "").lower()
    for site in [*sites, *LOGIN_HOSTS]:
        h = _host(site)
        if not h:
            continue
        for d in {h, _domain(h)}:
            if host == d or host.endswith("." + d):
                return True
    return False


_cache: dict = {"mtime": None, "value": None}


def current(now: float | None = None) -> list[str] | None:
    """지금 도는 작업의 사이트. 작업이 없거나 시간이 지났으면 None(막지 않음)."""
    try:
        mtime = os.stat(FILE).st_mtime
    except FileNotFoundError:
        return None
    if _cache["mtime"] != mtime:
        try:
            with open(FILE, encoding="utf-8") as f:
                data = json.load(f)
            _cache.update(mtime=mtime, value=data)
        except (OSError, ValueError):
            # 읽을 수 없는 제한은 가장 좁게: 아무 사이트도 열지 않는다.
            _cache.update(mtime=mtime, value={"sites": [], "until": float("inf")})
    data = _cache["value"]
    if (now or time.time()) > float(data.get("until", 0)):
        return None
    return list(data.get("sites") or [])
