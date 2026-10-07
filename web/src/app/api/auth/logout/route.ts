import { cookies } from "next/headers";
import { accessEnabled } from "@/lib/access";
import { SESSION_COOKIE } from "@/lib/auth";

export const dynamic = "force-dynamic";

/**
 * Behind Cloudflare Access, signing out of the console means signing out of
 * Access - clearing only the console cookie would leave the Access session
 * standing, and the next page load would let the person straight back in.
 */
export async function POST() {
  const jar = await cookies();
  jar.delete(SESSION_COOKIE);
  return Response.json({ ok: true, next: accessEnabled() ? "/cdn-cgi/access/logout" : "/login" });
}
