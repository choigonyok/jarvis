// 로그인: 비밀번호는 브라우저가 PBKDF2(60만 번)로 먼저 다진 값만 받는다.
// 서버는 그 값에 둥지마다 다른 소금을 쳐 SHA-256 으로 한 번 더 다져 둔다.
// 무거운 계산은 브라우저가 하고, 서버(Workers 무료 플랜의 10ms)는 가볍게 비교만 한다.

import type { Context } from "hono";
import { fail, newId, sameText, sha256hex } from "./util";
import type { AppEnv } from "./index";

export interface UserRow {
  id: string;
  login: string;
  name: string;
  /** 이름을 정했나(jarvis 로 처음 들어온 사람은 0: 화면이 먼저 묻는다) */
  named: number;
  couple_id: string;
}

const COOKIE = "dj";
const SESSION_DAYS = 90;
const FAIL_LIMIT = 10;
const FAIL_WINDOW = 15 * 60_000;

export const LOGIN_RE = /^[a-z0-9_]{3,20}$/;
const CLIENT_HASH_RE = /^[0-9a-f]{64}$/;

export function checkClientHash(v: unknown): string {
  if (typeof v !== "string" || !CLIENT_HASH_RE.test(v)) fail(400, "bad_input", "비밀번호를 다시 적어 주세요.");
  return v;
}

export async function hashForStore(clientHash: string, salt: string) {
  return sha256hex(`${salt}:${clientHash}`);
}

export async function startSession(c: Context<AppEnv>, userId: string) {
  const token = newId(32);
  const expires = Date.now() + SESSION_DAYS * 86_400_000;
  await c.env.DB.prepare("INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)")
    .bind(await sha256hex(token), userId, expires)
    .run();
  const secure = new URL(c.req.url).protocol === "https:" ? "; Secure" : "";
  c.header(
    "Set-Cookie",
    `${COOKIE}=${token}; Path=/; HttpOnly; SameSite=Lax; Max-Age=${SESSION_DAYS * 86_400}${secure}`,
  );
}

export async function endSession(c: Context<AppEnv>) {
  const token = readCookie(c.req.header("Cookie"));
  if (token) await c.env.DB.prepare("DELETE FROM sessions WHERE token_hash = ?").bind(await sha256hex(token)).run();
  c.header("Set-Cookie", `${COOKIE}=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0`);
}

export async function sessionUser(c: Context<AppEnv>): Promise<UserRow | null> {
  const token = readCookie(c.req.header("Cookie"));
  if (!token) return null;
  return c.env.DB.prepare(
    `SELECT u.id, u.login, u.name, u.named, u.couple_id FROM sessions s JOIN users u ON u.id = s.user_id
     WHERE s.token_hash = ? AND s.expires_at > ?`,
  )
    .bind(await sha256hex(token), Date.now())
    .first<UserRow>();
}

function readCookie(header: string | undefined) {
  if (!header) return null;
  for (const part of header.split(";")) {
    const [k, ...v] = part.trim().split("=");
    if (k === COOKIE) return v.join("=") || null;
  }
  return null;
}

/** 같은 아이디로 15분에 열 번 틀리면 잠시 막는다. */
export async function guardLogin(db: D1Database, login: string) {
  const row = await db.prepare("SELECT n, since FROM login_fails WHERE login = ?").bind(login).first<{ n: number; since: number }>();
  if (row && Date.now() - row.since < FAIL_WINDOW && row.n >= FAIL_LIMIT)
    fail(429, "too_many", "비밀번호를 여러 번 틀렸어요. 15분 뒤에 다시 해 주세요.");
}

export async function noteFail(db: D1Database, login: string) {
  const now = Date.now();
  await db
    .prepare(
      `INSERT INTO login_fails (login, n, since) VALUES (?, 1, ?)
       ON CONFLICT (login) DO UPDATE SET
         n = CASE WHEN ? - since > ? THEN 1 ELSE n + 1 END,
         since = CASE WHEN ? - since > ? THEN ? ELSE since END`,
    )
    .bind(login, now, now, FAIL_WINDOW, now, FAIL_WINDOW, now)
    .run();
}

export async function checkPassword(db: D1Database, login: string, clientHash: string) {
  const row = await db
    .prepare("SELECT id, pass_hash, pass_salt FROM users WHERE login = ?")
    .bind(login)
    .first<{ id: string; pass_hash: string; pass_salt: string }>();
  // 없는 아이디도 같은 만큼 계산해 걸린 시간으로 있는지 알 수 없게 한다.
  const stored = await hashForStore(clientHash, row?.pass_salt ?? "none");
  if (!row || !sameText(stored, row.pass_hash)) return null;
  return row.id;
}
