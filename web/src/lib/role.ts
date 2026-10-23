/**
 * Who is at the console, and what they may open.
 *
 * Two people sign in through Cloudflare Access. The operator sees everything.
 * The guest gets the conversation (their own - a separate agent), the workout
 * log, the shared calendar, their own notifications (notify-svc keeps each
 * person's apart), their own voice for 음성 mode (tts keeps each person's
 * recording apart, by token), the image editor (PhotoCraft, which runs in
 * their own browser and keeps nothing here) and, by the operator's choice,
 * where the two of them went (the 위치 screen: the operator's day read only,
 * and the guest's own phone's setup - never the operator's phone setup, which
 * holds its credentials), and nothing that is the operator's alone: assets,
 * spending, KakaoTalk, the agent's browser, the approval ledger.
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
export const GUEST_PAGES = new Set(["/", "/jobs", "/workout", "/calendar", "/edit", "/location", "/transit"]);
/** API routes a guest may call: the route itself, or anything beneath it - never a sibling with a longer name. */
const GUEST_API = [
  "/api/agent",
  // The guest's own jobs: the gateway opens only the 작업 tab's routes on them,
  // with the guest's token, so jobs-svc finds no other.
  "/api/jobs",
  "/api/workout",
  "/api/notify",
  "/api/tts",
  "/api/version",
  "/api/context/track",
  "/api/context/owntracks/setup/guest",
  "/api/context/owntracks/regions/guest",
  "/api/context/transit/guest",
  "/api/context/transit/lines",
  "/api/map",
];
/** Static files a guest may load: the editor the 편집 tab frames (scripts/photocraft.mjs). */
const GUEST_FILES = ["/photocraft/"];

export function guestMay(pathname: string): boolean {
  if (GUEST_PAGES.has(pathname)) return true;
  if (GUEST_FILES.some((p) => pathname.startsWith(p))) return true;
  return GUEST_API.some((p) => pathname === p || pathname.startsWith(`${p}/`));
}
