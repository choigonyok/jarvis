/**
 * Who is at the console, and what they may open.
 *
 * Two people sign in through Cloudflare Access. The operator sees everything.
 * The guest gets the conversation (their own - a separate agent), the workout
 * log and the shared calendar, and nothing that is the operator's alone:
 * assets, spending, KakaoTalk, the agent's browser, the approval ledger.
 *
 * The role comes from the email Access signed (see lib/access.ts), matched
 * against ACCESS_OWNERS and ACCESS_GUESTS. An Access-verified email on neither
 * list is refused - the Access policy and these lists must agree, and when
 * they do not, the narrower answer wins. Without Access (local, LAN), a
 * password session is the operator.
 *
 * This is enforced in the middleware for every page and API route, not only
 * by hiding tabs: a hidden tab is a link nobody shows you, not a locked door.
 */

export type Role = "owner" | "guest";

/** Set by the middleware on every request it lets through; overwritten, never trusted from the client. */
export const ROLE_HEADER = "x-jarvis-role";

const list = (v: string | undefined) =>
  (v ?? "").split(",").map((s) => s.trim().toLowerCase()).filter(Boolean);

export function roleFor(email: string): Role | null {
  const e = email.trim().toLowerCase();
  if (!e) return null;
  if (list(process.env.ACCESS_OWNERS).includes(e)) return "owner";
  if (list(process.env.ACCESS_GUESTS).includes(e)) return "guest";
  return null;
}

/** The pages a guest can open - also the tabs a guest is shown. */
export const GUEST_PAGES = new Set(["/", "/workout", "/calendar"]);
/** API routes a guest may call: the route itself, or anything beneath it - never a sibling with a longer name. */
const GUEST_API = ["/api/agent", "/api/workout"];

export function guestMay(pathname: string): boolean {
  if (GUEST_PAGES.has(pathname)) return true;
  return GUEST_API.some((p) => pathname === p || pathname.startsWith(`${p}/`));
}
