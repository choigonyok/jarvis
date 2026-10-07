"""browser-use의 stdio MCP 서버를 인증된 HTTP로 중계한다.

jarvis 에이전트는 컨테이너 안에서 돈다. 컨테이너에는 화면도 Chrome도 없고,
browser-use MCP는 stdio로만 말한다. 그래서 브라우저는 이 맥에서 띄우고,
컨테이너가 붙을 수 있게 이 프로세스가 HTTP로 열어준다.

업스트림 세션은 이 프로세스가 사는 동안 하나로 유지된다. 브라우저 탭과
로그인 상태가 요청마다 날아가지 않아야 하기 때문이다.
"""

import contextlib
import os
import secrets
import sys

import uvicorn
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.server.lowlevel import Server
from mcp.types import (
    CallToolRequestParams,
    CallToolResult,
    ListToolsResult,
    PaginatedRequestParams,
)
from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse
from starlette.routing import Mount

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
    return ListToolsResult(tools=[t for t in tools if t.name not in HIDDEN])


async def handle_call_tool(_ctx, params: CallToolRequestParams) -> CallToolResult:
    if params.name in HIDDEN:
        return CallToolResult(
            content=[{"type": "text", "text": f"{params.name} is not available here"}],
            isError=True,
        )
    return await _upstream.call_tool(params.name, params.arguments or {})


app.add_request_handler("tools/list", PaginatedRequestParams, handle_list_tools)
app.add_request_handler("tools/call", CallToolRequestParams, handle_call_tool)


class RequireToken(BaseHTTPMiddleware):
    async def dispatch(self, request, call_next):
        scheme, _, value = request.headers.get("authorization", "").partition(" ")
        if scheme.lower() != "bearer" or not secrets.compare_digest(value, TOKEN):
            return JSONResponse({"error": "unauthorized"}, status_code=401)
        return await call_next(request)


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
        routes=[Mount("/", app=handle)],
        middleware=[Middleware(RequireToken)] if TOKEN else [],
        lifespan=lifespan,
    )


if __name__ == "__main__":
    uvicorn.run(build_app(), host=HOST, port=PORT)
