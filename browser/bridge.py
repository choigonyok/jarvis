"""browser-use의 stdio MCP 서버를 인증된 HTTP로 중계한다.

jarvis 에이전트는 컨테이너 안에서 돈다. 컨테이너에는 화면도 Chrome도 없고,
browser-use MCP는 stdio로만 말한다. 그래서 브라우저는 이 맥에서 띄우고,
컨테이너가 붙을 수 있게 이 프로세스가 HTTP로 열어준다.

업스트림 세션은 이 프로세스가 사는 동안 하나로 유지된다. 브라우저 탭과
로그인 상태가 요청마다 날아가지 않아야 하기 때문이다.
"""

import contextlib
import json
import os
import secrets
import sys
import time

import uvicorn
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.server.lowlevel import Server
from mcp.types import (
    CallToolRequestParams,
    CallToolResult,
    ListToolsResult,
    PaginatedRequestParams,
    Tool,
)
from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse
from starlette.routing import Mount, Route

import jobsites
import login
import pin
import upload

HOST = os.environ.get("MCP_BROWSER_HOST", "0.0.0.0")
PORT = int(os.environ.get("MCP_BROWSER_PORT", "8931"))
TOKEN = os.environ.get("MCP_BROWSER_TOKEN", "")

# 루프백이 아닌 곳에 열면서 토큰이 없으면 시작하지 않는다. 조용히 뜨는
# 무인증 리스너는 아예 안 뜨는 것보다 나쁘다.
if not TOKEN and HOST not in ("127.0.0.1", "localhost", "::1"):
    sys.exit(
        f"MCP_BROWSER_TOKEN is required to bind {HOST}; "
        "without it anyone who can reach this port drives the browser"
    )

# 이 도구는 자체 LLM 에이전트를 돌린다 - API 키가 필요하고, 한 번 호출에
# 여러 동작을 스스로 수행한다. jarvis는 구독으로만 과금하고 동작마다
# 사람이 보는 것을 전제하므로, 모델에게 아예 보여주지 않는다.
HIDDEN = {
    "retry_with_browser_use_agent",
    # 397KB짜리 원본 HTML을 뱉는다. CLI가 컨텍스트에 못 넣고 파일로 쏟으면
    # 모델이 그걸 읽으려 Bash로 파싱 코드를 짜고, 그때마다 승인 카드가 뜬다.
    # 구조가 필요하면 browser_get_state가 0.2초에 준다.
    "browser_get_html",
}

app = Server("mcp-browser")
_upstream: ClientSession | None = None


async def handle_list_tools(_ctx, _params: PaginatedRequestParams | None) -> ListToolsResult:
    tools = (await _upstream.list_tools()).tools
    # browser-use에 없는 도구를 여기서 더한다: 파일 선택 칸 채우기, 쿠팡 결제 비밀번호 넣기,
    # 저장된 로그인 정보 넣기.
    return ListToolsResult(
        tools=[t for t in tools if t.name not in HIDDEN] + [Tool(**upload.TOOL), Tool(**pin.TOOL), Tool(**login.TOOL)]
    )


async def handle_call_tool(_ctx, params: CallToolRequestParams) -> CallToolResult:
    if params.name in HIDDEN:
        return CallToolResult(
            content=[{"type": "text", "text": f"{params.name} is not available here"}],
            isError=True,
        )
    if params.name == upload.TOOL["name"]:
        args = params.arguments or {}
        try:
            text = await upload.upload(list(args.get("paths") or []), args.get("selector"))
        except Exception as e:
            return CallToolResult(content=[{"type": "text", "text": str(e)}], isError=True)
        return CallToolResult(content=[{"type": "text", "text": text}])
    if params.name == pin.TOOL["name"]:
        try:
            text = await pin.enter()
        except Exception as e:
            return CallToolResult(content=[{"type": "text", "text": str(e)}], isError=True)
        return CallToolResult(content=[{"type": "text", "text": text}])
    if params.name == login.TOOL["name"]:
        try:
            text = await login.fill()
        except Exception as e:
            return CallToolResult(content=[{"type": "text", "text": str(e)}], isError=True)
        return CallToolResult(content=[{"type": "text", "text": text}])
    return await _upstream.call_tool(params.name, params.arguments or {})


app.add_request_handler("tools/list", PaginatedRequestParams, handle_list_tools)
app.add_request_handler("tools/call", CallToolRequestParams, handle_call_tool)


class RequireToken(BaseHTTPMiddleware):
    async def dispatch(self, request, call_next):
        scheme, _, value = request.headers.get("authorization", "").partition(" ")
        if scheme.lower() != "bearer" or not secrets.compare_digest(value, TOKEN):
            return JSONResponse({"error": "unauthorized"}, status_code=401)
        return await call_next(request)


async def job_sites(request):
    """작업 한 번이 도는 동안 브라우저를 그 작업의 사이트 안에 묶고(POST), 끝나면
    푼다(DELETE). 지키는 것은 guard.py 다 - 이 파일을 읽어 그 밖으로 가는 페이지
    이동을 막는다. 에이전트가 풀지 못하고 죽어도 seconds 가 지나면 풀린다."""
    if request.method == "DELETE":
        with contextlib.suppress(FileNotFoundError):
            os.remove(jobsites.FILE)
        return JSONResponse({"ok": True})
    try:
        body = await request.json()
        sites = [str(x).strip().lower() for x in body.get("sites") or [] if str(x).strip()]
        seconds = max(60, min(int(body.get("seconds") or 0), 6 * 3600))
    except (ValueError, TypeError, AttributeError):
        return JSONResponse({"error": "sites, seconds 가 필요합니다"}, status_code=400)
    tmp = jobsites.FILE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump({"sites": sites, "until": time.time() + seconds}, f)
    os.replace(tmp, jobsites.FILE)
    return JSONResponse({"ok": True, "sites": sites})


def build_app() -> Starlette:
    manager = StreamableHTTPSessionManager(app=app, stateless=True)

    async def handle(scope, receive, send):
        await manager.handle_request(scope, receive, send)

    @contextlib.asynccontextmanager
    async def lifespan(_):
        global _upstream
        params = StdioServerParameters(
            command=sys.executable, args=["-m", "browser_use.mcp.server"], env=os.environ.copy()
        )
        async with stdio_client(params) as (r, w):
            async with ClientSession(r, w) as session:
                await session.initialize()
                _upstream = session
                async with manager.run():
                    yield
        _upstream = None

    # "/"에 문다. "/mcp"에 물리면 Starlette이 "/mcp/"로 307을 보내고,
    # POST 리다이렉트는 클라이언트마다 따라가는 방식이 다르다.
    return Starlette(
        routes=[Route("/job-sites", job_sites, methods=["POST", "DELETE"]), Mount("/", app=handle)],
        middleware=[Middleware(RequireToken)] if TOKEN else [],
        lifespan=lifespan,
    )


if __name__ == "__main__":
    uvicorn.run(build_app(), host=HOST, port=PORT)
