/**
 * The session running out while the console is open: the console's own
 * cookie lasts two weeks from the login (lib/auth.ts), Cloudflare Access's a
 * month. Every read then failed, every screen said 불러오지 못했어요 · 다시
 * 시도 - which no number of tries could mend - and a home-screen app has no
 * reload to reach the login with (components/shell/session-watch.tsx).
 *
 * The middleware's own 401 says so in this header. A 401 from further in -
 * the gateway refusing a token - comes back through the proxy with the same
 * status, and taking it for this would send a signed-in console to the login
 * page and straight back again, round and round.
 */
export const SIGNED_OUT_HEADER = "x-jarvis-signed-out";
