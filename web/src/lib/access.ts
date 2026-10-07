/**
 * Cloudflare Access in front of the console.
 *
 * When jarvis.choigonyok.com is reached through Access, every request carries
 * a JWT that Cloudflare signed after the person signed in (Google, one
 * allowed account). Verifying it here - signature against the team's public
 * keys, this app's audience tag, expiry, issuer - is what lets the console
 * skip its own password screen: the same question has already been answered,
 * by something stricter.
 *
 * Without ACCESS_TEAM_DOMAIN and ACCESS_AUD (a local stack, the Docker image
 * on a LAN) nothing here is trusted and the password login stays the gate. A
 * header that merely looks like Access's is worthless without the signature.
 */

const TEAM = process.env.ACCESS_TEAM_DOMAIN ?? ""; // choigonyok.cloudflareaccess.com
const AUD = process.env.ACCESS_AUD ?? "";

type Jwk = JsonWebKey & { kid: string };
let keys: { at: number; byKid: Map<string, CryptoKey> } | null = null;

async function key(kid: string): Promise<CryptoKey | undefined> {
  // The team rotates its keys; ten minutes keeps one fetch from serving
  // every request without holding a retired key for long.
  if (!keys || Date.now() - keys.at > 10 * 60_000 || !keys.byKid.has(kid)) {
    const res = await fetch(`https://${TEAM}/cdn-cgi/access/certs`);
    if (!res.ok) return undefined;
    const { keys: jwks } = (await res.json()) as { keys: Jwk[] };
    const byKid = new Map<string, CryptoKey>();
    for (const jwk of jwks) {
      byKid.set(
        jwk.kid,
        await crypto.subtle.importKey("jwk", jwk, { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" }, false, ["verify"]),
      );
    }
    keys = { at: Date.now(), byKid };
  }
  return keys.byKid.get(kid);
}

function decode(part: string): Uint8Array<ArrayBuffer> {
  const b64 = part.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(part.length / 4) * 4, "=");
  const bin = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** The signed-in email when the request came through Access, otherwise null. */
export async function accessIdentity(
  assertion: string | null | undefined,
  now = Date.now(),
): Promise<string | null> {
  if (!TEAM || !AUD || !assertion) return null;
  try {
    const [h, p, s] = assertion.split(".");
    if (!h || !p || !s) return null;
    const header = JSON.parse(new TextDecoder().decode(decode(h))) as { alg?: string; kid?: string };
    if (header.alg !== "RS256" || !header.kid) return null;
    const k = await key(header.kid);
    if (!k) return null;
    const ok = await crypto.subtle.verify(
      "RSASSA-PKCS1-v1_5", k, decode(s), new TextEncoder().encode(`${h}.${p}`),
    );
    if (!ok) return null;
    const claims = JSON.parse(new TextDecoder().decode(decode(p))) as {
      aud?: string | string[]; exp?: number; nbf?: number; iss?: string; email?: string;
    };
    const aud = Array.isArray(claims.aud) ? claims.aud : [claims.aud];
    const sec = now / 1000;
    if (!aud.includes(AUD)) return null;
    if (claims.iss !== `https://${TEAM}`) return null;
    if (!claims.exp || claims.exp < sec) return null;
    if (claims.nbf && claims.nbf > sec + 60) return null;
    return claims.email ?? "";
  } catch {
    return null;
  }
}

export const accessEnabled = () => Boolean(TEAM && AUD);
