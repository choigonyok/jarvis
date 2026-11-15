import { NextResponse, type NextRequest } from "next/server";
import { accessIdentity } from "@/lib/access";
import { SESSION_COOKIE, valid } from "@/lib/auth";
import { DENIED_PARAM, ROLE_HEADER, type Role, guestMay, roleFor } from "@/lib/role";
import { SIGNED_OUT_HEADER } from "@/lib/signed-out";
import { BAR_COLOR, THEME_COOKIE, type Theme, themeOf } from "@/lib/theme-name";

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
        ? NextResponse.json({ error: "이 계정에는 권한이 없어요." }, { status: 403 })
        : refused(identity, themeOf(request.cookies.get(THEME_COOKIE)?.value));
    }
  } else if (await valid(token)) {
    role = "owner";
  }

  if (role) {
    if (role === "guest" && !guestMay(request.nextUrl.pathname)) {
      // To the conversation, which says why: it opened without a word, and
      // nobody knew why they were there.
      return isApi
        ? NextResponse.json({ error: "이 계정에서는 열 수 없어요." }, { status: 403 })
        : NextResponse.redirect(new URL(`/?${DENIED_PARAM}=1`, request.url));
    }
    // Overwritten on every request, so a client cannot send its own.
    const headers = new Headers(request.headers);
    headers.set(ROLE_HEADER, role);
    return NextResponse.next({ request: { headers } });
  }

  // An expired session inside a fetch should read as 401, not as a login page
  // arriving where JSON was expected - and say it is this 401, which the page
  // follows to the login (lib/signed-out.ts), and not one from further in.
  if (isApi) {
    return NextResponse.json({ error: "로그인이 필요해요." }, { status: 401, headers: { [SIGNED_OUT_HEADER]: "1" } });
  }

  const login = new URL("/login", request.url);
  const from = request.nextUrl.pathname + request.nextUrl.search;
  if (from !== "/") login.searchParams.set("from", from);
  const response = NextResponse.redirect(login);
  // A cookie that failed to verify is worse than none: it would keep failing.
  if (token) response.cookies.delete(SESSION_COOKIE);
  return response;
}

/** The console's words and ground (globals.css), for the one page drawn here. */
const INK: Record<Theme, { foreground: string; dim: string; faint: string; reject: string; glass: string; onForeground: string }> = {
  dark: {
    foreground: "oklch(0.945 0.002 265)",
    dim: "oklch(0.672 0.004 265)",
    faint: "oklch(0.58 0.005 265)",
    reject: "oklch(0.687 0.176 25)",
    glass: "oklch(1 0 0 / 3.5%)",
    onForeground: "oklch(0.18 0.004 265)",
  },
  light: {
    foreground: "oklch(0.2 0.006 265)",
    dim: "oklch(0.45 0.006 265)",
    faint: "oklch(0.52 0.006 265)",
    reject: "oklch(0.55 0.19 25)",
    glass: "oklch(0 0 0 / 3.5%)",
    onForeground: "oklch(0.985 0.002 265)",
  },
};

/**
 * What an account Access let through and neither list names is shown: which
 * account it is, and Access's sign-out, the way to choose another. It was a
 * line of bare text on white - and a home-screen app has no address bar to
 * leave that by. Drawn here as the login is drawn rather than as a page of
 * the app: the app's frame around any page reads the API, which refuses this
 * account at every turn.
 */
function refused(email: string, theme: Theme): NextResponse {
  const c = INK[theme];
  const who = email.replace(/[&<>"']/g, (ch) => `&#${ch.charCodeAt(0)};`);
  const html = `<!doctype html>
<html lang="ko">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="theme-color" content="${BAR_COLOR[theme]}">
<title>Jarvis</title>
<style>
html,body{margin:0;background:${BAR_COLOR[theme]};color-scheme:${theme}}
body{font-family:system-ui,-apple-system,"Apple SD Gothic Neo",sans-serif;-webkit-text-size-adjust:100%}
main{box-sizing:border-box;display:flex;justify-content:center;padding:18vh max(20px,env(safe-area-inset-right)) 0 max(20px,env(safe-area-inset-left))}
section{box-sizing:border-box;width:100%;max-width:20rem;border-radius:16px;background:${c.glass};padding:20px}
h1{margin:0;font-size:15px;font-weight:600;letter-spacing:-0.01em;color:${c.foreground}}
p{margin:16px 0 0;border-left:2px solid ${c.reject};padding-left:12px;font-size:13.5px;line-height:1.625;color:${c.dim}}
small{display:block;font-size:12.5px;color:${c.faint};overflow-wrap:anywhere}
a{display:flex;align-items:center;justify-content:center;height:48px;margin-top:24px;border-radius:8px;background:${c.foreground};color:${c.onForeground};font-size:14.5px;text-decoration:none}
@media (min-width:640px){main{min-height:100vh;align-items:center;padding-top:0}section{padding:24px}}
@media (min-width:640px) and (pointer:fine){a{height:36px;font-size:13.5px}}
</style>
</head>
<body>
<main><section>
<h1>Jarvis</h1>
<p>이 계정으로는 들어올 수 없어요.<small>${who}</small></p>
<a href="/cdn-cgi/access/logout">다른 계정으로 로그인</a>
</section></main>
</body>
</html>`;
  return new NextResponse(html, {
    status: 403,
    headers: { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" },
  });
}

export const config = {
  matcher: [
    // Everything except the login surface, the auth endpoints, and the assets
    // the login page itself needs to render.
    // The home-screen manifest and icons too: the phone fetches them on its
    // own, and a guest's role would otherwise turn them into a redirect.
    // The service worker too: the browser fetches it on its own to update it.
    "/((?!login|api/auth/|_next/static|_next/image|favicon.ico|manifest.webmanifest|icons/|sw\\.js).*)",
  ],
};
