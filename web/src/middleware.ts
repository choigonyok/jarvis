import { NextResponse, type NextRequest } from "next/server";
import { accessIdentity } from "@/lib/access";
import { SESSION_COOKIE, valid } from "@/lib/auth";

/**
 * The gate. Everything behind it - the thread, the calendar, the live browser
 * screen, and the agent proxy that drives all three - is reachable only with a
 * session, so this is deliberately a denylist of nothing: the matcher below
 * lets through the login surface and the static bundle, and the rest must
 * present a cookie.
 */
export async function middleware(request: NextRequest) {
  // Through Cloudflare Access, the signed assertion is the session: the person
  // has already proven who they are, more strictly than a password here would.
  const assertion =
    request.headers.get("cf-access-jwt-assertion") ?? request.cookies.get("CF_Authorization")?.value;
  if ((await accessIdentity(assertion)) !== null) return NextResponse.next();

  const token = request.cookies.get(SESSION_COOKIE)?.value;
  if (await valid(token)) return NextResponse.next();

  // An expired session inside a fetch should read as 401, not as a login page
  // arriving where JSON was expected.
  if (request.nextUrl.pathname.startsWith("/api/")) {
    return NextResponse.json({ error: "인증이 필요합니다." }, { status: 401 });
  }

  const login = new URL("/login", request.url);
  const from = request.nextUrl.pathname + request.nextUrl.search;
  if (from !== "/") login.searchParams.set("from", from);
  const response = NextResponse.redirect(login);
  // A cookie that failed to verify is worse than none: it would keep failing.
  if (token) response.cookies.delete(SESSION_COOKIE);
  return response;
}

export const config = {
  matcher: [
    // Everything except the login surface, the auth endpoints, and the assets
    // the login page itself needs to render.
    "/((?!login|api/auth/|_next/static|_next/image|favicon.ico).*)",
  ],
};
