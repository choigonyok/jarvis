import { cookies } from "next/headers";
import { SESSION_COOKIE, SESSION_MAX_AGE, check, credentials, issue } from "@/lib/auth";

export const dynamic = "force-dynamic";

/** A wrong guess should not be free to retry a thousand times a second. */
const DELAY_MS = 400;

export async function POST(request: Request) {
  const body = (await request.json().catch(() => null)) as {
    id?: unknown;
    password?: unknown;
  } | null;

  // Saying so is not a leak: it is true for every visitor alike, and "wrong
  // password" would send the operator hunting for a typo that is not there.
  if (!credentials()) {
    return Response.json(
      { error: "로그인이 설정되지 않았습니다. JARVIS_AUTH_ID 와 JARVIS_AUTH_PASSWORD 를 넣으세요." },
      { status: 503 },
    );
  }

  const id = typeof body?.id === "string" ? body.id : "";
  const password = typeof body?.password === "string" ? body.password : "";

  if (!(await check(id, password))) {
    await new Promise((resolve) => setTimeout(resolve, DELAY_MS));
    return Response.json(
      { error: "아이디 또는 비밀번호가 맞지 않습니다." },
      { status: 401 },
    );
  }

  // Secure is decided by how this request actually arrived, not by NODE_ENV:
  // the same production image serves https behind Cloudflare and plain http on
  // a LAN address, and a Secure cookie would silently never arrive on the latter.
  const https =
    request.headers.get("x-forwarded-proto") === "https" ||
    new URL(request.url).protocol === "https:";

  const jar = await cookies();
  jar.set(SESSION_COOKIE, await issue(), {
    httpOnly: true,
    sameSite: "lax",
    secure: https,
    path: "/",
    maxAge: SESSION_MAX_AGE,
  });

  return Response.json({ ok: true });
}
