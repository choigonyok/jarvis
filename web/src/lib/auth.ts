// Session is a signed cookie, not a server-side table: this console runs as one
// process for one person, and a session store would be one more thing to move
// when the stack moves. Web Crypto only - this code runs in middleware, which
// has no Node APIs on Cloudflare.

export const SESSION_COOKIE = "jarvis_session";
/** Two weeks. Long enough not to nag, short enough that a lost laptop expires. */
export const SESSION_MAX_AGE = 60 * 60 * 24 * 14;

/**
 * Compose passes an unset variable through as the empty string, which `??`
 * would happily accept as a password. Only a non-empty value counts.
 */
function env(name: string): string | undefined {
  const value = process.env[name];
  return value && value.length > 0 ? value : undefined;
}

/**
 * The single operator, from JARVIS_AUTH_ID and JARVIS_AUTH_PASSWORD. There is
 * no default: a password written here would be a password in a public
 * repository. Unset means nobody can sign in - the console fails closed.
 */
export function credentials(): { id: string; password: string } | null {
  const id = env("JARVIS_AUTH_ID");
  const password = env("JARVIS_AUTH_PASSWORD");
  return id && password ? { id, password } : null;
}

/**
 * The signing key. Falls back to the password so a stack brought up without
 * JARVIS_AUTH_SECRET still issues valid sessions - changing the password then
 * invalidates every outstanding one, which is the behaviour you want anyway.
 */
function secret(): string | null {
  const creds = credentials();
  return env("JARVIS_AUTH_SECRET") ?? (creds ? `jarvis:${creds.password}` : null);
}

const encoder = new TextEncoder();

async function key(): Promise<CryptoKey> {
  const s = secret();
  if (!s) throw new Error("JARVIS_AUTH_ID / JARVIS_AUTH_PASSWORD 가 설정되지 않았습니다");
  return crypto.subtle.importKey(
    "raw",
    encoder.encode(s),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
}

function base64url(bytes: ArrayBuffer): string {
  let binary = "";
  for (const byte of new Uint8Array(bytes)) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function sign(payload: string): Promise<string> {
  return base64url(
    await crypto.subtle.sign("HMAC", await key(), encoder.encode(payload)),
  );
}

/** Comparison that does not leak how far it got. */
function equal(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i += 1) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

/** `<expiry epoch seconds>.<signature>` - there is only one subject to encode. */
export async function issue(now = Date.now()): Promise<string> {
  const expires = Math.floor(now / 1000) + SESSION_MAX_AGE;
  return `${expires}.${await sign(String(expires))}`;
}

export async function valid(
  token: string | undefined,
  now = Date.now(),
): Promise<boolean> {
  if (!token) return false;
  const [expires, signature] = token.split(".");
  if (!expires || !signature) return false;
  if (!/^\d+$/.test(expires)) return false;
  if (Number(expires) * 1000 <= now) return false;
  if (!secret()) return false;
  return equal(signature, await sign(expires));
}

export async function check(id: string, password: string): Promise<boolean> {
  const want = credentials();
  if (!want) return false;
  // Both are compared regardless of the first result, so a wrong id and a
  // wrong password take the same time.
  const okId = equal(id, want.id);
  const okPassword = equal(password, want.password);
  return okId && okPassword;
}
