"""작업 실행에 전할 말 - 에이전트가 넣고(bridge.py 의 POST /notices), 작업이 도는 동안 다음 브라우저
도구 결과의 끝에 한 번 붙는다.

가드가 막은 페이지 자리에는 안내 화면이 뜨지만, 실행은 그 화면을 보지 않을 수 있다: 막힌 것이 다른
탭(결제 팝업)이거나, 주소의 resultCode=0000 만 읽고 결제가 끝났다고 여긴다. 도구 결과에 붙은 말은
실행이 다음에 무엇을 하든 읽는다.

작업이 도는 동안(jobsites 의 제한이 있는 동안)에만 건넨다. 실행이 끝나 제한이 풀리면 남은 말은
버린다 - 다음에 브라우저를 쓰는 것은 대화일 수 있다.
"""

import time

# 넣은 지 이만큼 지난 말은 버린다: 그 사이 다른 실행이 시작됐을 수 있다.
TTL = 600
# 한 번에 붙이는 말의 수. 같은 말은 한 번만 둔다.
KEEP = 5
PREFIX = "[자비스 가드] "

_items: list[tuple[float, str]] = []


def add(text: str, now: float | None = None) -> bool:
    """말을 넣는다. 비었거나 이미 기다리는 말이면 넣지 않는다."""
    text = " ".join(str(text or "").split())[:1000]
    if not text or any(t == text for _, t in _items):
        return False
    _items.append((now or time.time(), text))
    del _items[:-KEEP]
    return True


def take(now: float | None = None) -> list[str]:
    """기다리는 말을 넣은 차례로 꺼내고 비운다(시간이 지난 것은 버린다)."""
    now = now or time.time()
    out = [t for at, t in _items if now - at < TTL]
    _items.clear()
    return out


def clear() -> None:
    _items.clear()


def render(texts: list[str]) -> str:
    """도구 결과 끝에 붙일 글."""
    return "\n".join(PREFIX + t for t in texts)
