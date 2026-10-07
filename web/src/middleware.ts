import { NextResponse, type NextRequest } from "next/server";
import { accessIdentity } from "@/lib/access";
import { SESSION_COOKIE, valid } from "@/lib/auth";
import { ROLE_HEADER, type Role, guestMay, roleFor } from "@/lib/role";

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
  const identity = await accessIdentity(assertion);
  const token = request.cookies.get(SESSION_COOKIE)?.value;
  const isApi = request.nextUrl.pathname.startsWith("/api/");

  let role: Role | null = null;
  if (identity !== null) {
    role = roleFor(identity);
    if (!role) {
      // Through Access, but on neither list: the lists are the narrower answer.
      return isApi
        ? NextResponse.json({ error: "이 계정에는 권한이 없습니다." }, { status: 403 })
        : new NextResponse("이 계정에는 권한이 없습니다.", { status: 403 });
    }
  } else if (await valid(token)) {
    role = "owner";
  }

  if (role) {
    if (role === "guest" && !guestMay(request.nextUrl.pathname)) {
      return isApi
        ? NextResponse.json({ error: "이 계정에서는 열 수 없습니다." }, { status: 403 })
        : NextResponse.redirect(new URL("/", request.url));
    }
    // Overwritten on every request, so a client cannot send its own.
    const headers = new Headers(request.headers);
    headers.set(ROLE_HEADER, role);
    return NextResponse.next({ request: { headers } });
  }

  // An expired session inside a fetch should read as 401, not as a login page
  // arriving where JSON was expected.
  if (isApi) {
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
    // The home-screen manifest and icons too: the phone fetches them on its
    // own, and a guest's role would otherwise turn them into a redirect.
    "/((?!login|api/auth/|_next/static|_next/image|favicon.ico|manifest.webmanifest|icons/).*)",
  ],
};
