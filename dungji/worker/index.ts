import { Hono, type Context } from "hono";
import {
  fishDef,
  itemDef,
  plantDef,
  STARTER,
  tankDef,
  themeDef,
} from "../shared/catalog";
import {
  MOODS,
  type DiaryComment,
  type DiaryDay,
  type DiaryEntry,
  type DiaryMonth,
  type GuestEntry,
  type LedgerRow,
  type Member,
  type Neighbor,
  type Reward,
  type Visitor,
} from "../shared/api";
import {
  dayKey,
  dirtSlot,
  expandPrice,
  findSpot,
  fitsFloor,
  fitsWall,
  harvestPlant,
  LIMIT,
  MESSY_AT,
  newPlant,
  plantView,
  REWARD,
  sellPrice,
  START_COINS,
  STORAGE_MAX,
  tickPlant,
  type PlantState,
  type TankState,
} from "../shared/rules";
import {
  checkClientHash,
  checkPassword,
  endSession,
  guardLogin,
  hashForStore,
  LOGIN_RE,
  noteFail,
  sessionUser,
  startSession,
  type UserRow,
} from "./auth";
import {
  estimateClean,
  getCouple,
  homeView,
  parse,
  placed,
  tick,
  type CoupleRow,
  type ItemRow,
} from "./home";
import { HomeLive } from "./live";
import { fail, hmacHex, HttpError, inviteCode, isDay, josa, newId, sameText, text } from "./util";

export { HomeLive };

export interface Env {
  DB: D1Database;
  LIVE: DurableObjectNamespace<HomeLive>;
  ASSETS: Fetcher;
  /** 직접 둔 TURN 서버(예: coturn): "turn:turn.example.com:3478,turns:turn.example.com:5349" */
  TURN_URLS?: string;
  TURN_USERNAME?: string;
  TURN_CREDENTIAL?: string;
  /** jarvis 웹과 나눠 가진 비밀: jarvis 가 서명한 입장권을 확인한다(api/sso/jarvis). */
  JARVIS_SSO_SECRET?: string;
  /** Cloudflare Realtime TURN 키가 있으면 통화마다 짧은 자격을 만든다. */
  CF_TURN_KEY_ID?: string;
  CF_TURN_KEY_TOKEN?: string;
}

export type AppEnv = { Bindings: Env; Variables: { user: UserRow | null } };
type C = Context<AppEnv>;

const app = new Hono<AppEnv>().basePath("/api");

app.onError((err, c) => {
  if (err instanceof HttpError) return c.json({ error: err.code, message: err.message }, err.status as 400);
  if (err instanceof SyntaxError) return c.json({ error: "bad_json", message: "보낸 내용을 읽지 못했어요." }, 400);
  console.error(err);
  return c.json({ error: "server", message: "잠깐 문제가 생겼어요. 다시 해 주세요." }, 500);
});

app.notFound((c) => c.json({ error: "not_found", message: "그런 주소가 없어요." }, 404));

app.use("*", async (c, next) => {
  // 다른 사이트가 쿠키를 싣고 쓰기를 보내지 못하게: 우리 화면만 이 머리글을 붙인다.
  if (c.req.method !== "GET" && c.req.method !== "HEAD" && c.req.header("x-dungji") !== "1")
    fail(403, "forbidden", "이 화면에서만 할 수 있어요.");
  c.set("user", await sessionUser(c));
  await next();
  c.header("Cache-Control", "no-store");
});

// ── 공통 ────────────────────────────────────────────────────────

const now = () => Date.now();

function requireUser(c: C): UserRow {
  const user = c.get("user");
  if (!user) fail(401, "login", "로그인해 주세요.");
  return user;
}

async function body<T>(c: C): Promise<Partial<T>> {
  const raw = await c.req.text();
  if (!raw) return {};
  const v = JSON.parse(raw);
  return v && typeof v === "object" ? (v as Partial<T>) : {};
}

async function homeBySlug(c: C, slug: string) {
  const couple = await getCouple(c.env.DB, { slug });
  if (!couple) fail(404, "no_home", "그런 둥지가 없어요.");
  return couple;
}

async function myHome(c: C) {
  const user = requireUser(c);
  const couple = await getCouple(c.env.DB, { id: user.couple_id });
  if (!couple) fail(404, "no_home", "둥지를 찾지 못했어요.");
  return { user, couple };
}

function coinStmts(db: D1Database, coupleId: string, delta: number, reason: string, by: string | null, at: number) {
  return [
    db.prepare("UPDATE couples SET coins = coins + ? WHERE id = ?").bind(delta, coupleId),
    db
      .prepare("INSERT INTO ledger (couple_id, delta, reason, by_id, at) VALUES (?, ?, ?, ?, ?)")
      .bind(coupleId, delta, reason, by, at),
  ];
}

/** 여러 쓰기를 한 번에. 창고 칸·깃털이 모자라 막히면 무엇이 모자란지 알려 준다. */
async function write(db: D1Database, stmts: D1PreparedStatement[]) {
  try {
    return await db.batch(stmts);
  } catch (e) {
    const m = String((e as Error)?.message ?? e);
    if (m.includes("storage_full"))
      fail(409, "storage_full", "창고가 꽉 찼어요. 창고를 넓히거나 물건을 방에 꺼내 두세요.");
    if (m.includes("home_full")) fail(409, "home_full", "이 둥지에는 이미 둘이 살고 있어요.");
    if (m.includes("CHECK constraint failed")) fail(409, "no_coins", "깃털이 모자라요.");
    throw e;
  }
}

/** 하루 limit 번까지만 받는 것을 하나 받는다. 받았으면 true. */
async function claim(db: D1Database, userId: string, day: string, kind: string, limit: number) {
  const r = await db
    .prepare(
      `INSERT INTO claims (user_id, day, kind, n) VALUES (?, ?, ?, 1)
       ON CONFLICT (user_id, day, kind) DO UPDATE SET n = n + 1 WHERE n < ?`,
    )
    .bind(userId, day, kind, limit)
    .run();
  return r.meta.changes > 0;
}

function liveStub(env: Env, coupleId: string) {
  return env.LIVE.get(env.LIVE.idFromName(coupleId));
}

async function onlineOf(env: Env, coupleId: string): Promise<string[]> {
  try {
    const r = await liveStub(env, coupleId).fetch("https://live/presence");
    return ((await r.json()) as { online: string[] }).online;
  } catch {
    return [];
  }
}

/** 그 둥지에 들어와 있는 둘에게 '바뀌었어요'를 알린다(응답을 기다리지 않는다). */
function notify(c: C, coupleId: string, what: "home" | "diary" | "guestbook") {
  const by = c.get("user")?.id ?? "";
  c.executionCtx.waitUntil(
    liveStub(c.env, coupleId)
      .fetch("https://live/notify", { method: "POST", body: JSON.stringify({ what, by }) })
      .catch(() => undefined),
  );
}

async function members(db: D1Database, coupleId: string, online: string[]): Promise<Member[]> {
  const rows = await db
    .prepare("SELECT id, name FROM users WHERE couple_id = ? ORDER BY created_at")
    .bind(coupleId)
    .all<{ id: string; name: string }>();
  return rows.results.map((r) => ({ id: r.id, name: r.name, online: online.includes(r.id) }));
}

/** 둥지를 지금 시각으로 맞춰 화면에 보낼 모양으로 답한다. */
async function respondHome(c: C, coupleId: string, rewards: Reward[] = [], added?: { id: string; place: "room" | "storage" }) {
  const user = requireUser(c);
  const db = c.env.DB;
  const t = now();
  const couple = (await getCouple(db, { id: coupleId }))!;
  const owner = user.couple_id === couple.id;
  const day = dayKey(t);
  const [state, online, visitsToday, recent, mine] = await Promise.all([
    tick(db, couple, t),
    onlineOf(c.env, couple.id),
    db.prepare("SELECT COUNT(*) AS n FROM visits WHERE home_id = ? AND day = ?").bind(couple.id, day).first<{ n: number }>(),
    db
      .prepare(
        `SELECT u.name, c.name AS home, c.slug FROM visits v
         JOIN users u ON u.id = v.visitor_id JOIN couples c ON c.id = u.couple_id
         WHERE v.home_id = ? AND v.day = ? ORDER BY v.at DESC LIMIT 8`,
      )
      .bind(couple.id, day)
      .all<Visitor>(),
    owner
      ? Promise.resolve(null)
      : db
          .prepare("SELECT helped FROM visits WHERE home_id = ? AND visitor_id = ? AND day = ?")
          .bind(couple.id, user.id, day)
          .first<{ helped: number }>(),
  ]);
  const view = homeView(couple, state, t, {
    owner,
    members: await members(db, couple.id, online),
    visits: { today: visitsToday?.n ?? 0, recent: recent.results },
    helpLeft: owner ? undefined : Math.max(0, LIMIT.helpClean - (mine?.helped ?? 0)),
  });
  if (owner) {
    // 화면이 '새 글' 점을 찍으려고 보는 마지막 때: 이웃의 방명록, 상대의 일기
    const [gb, diary] = await db.batch([
      db.prepare("SELECT MAX(created_at) AS at FROM guestbook WHERE home_id = ?").bind(couple.id),
      db.prepare("SELECT MAX(updated_at) AS at FROM diary WHERE couple_id = ? AND author_id <> ?").bind(couple.id, user.id),
    ]);
    view.news = {
      guestbook: (gb.results[0] as { at: number | null } | undefined)?.at ?? null,
      diary: (diary.results[0] as { at: number | null } | undefined)?.at ?? null,
    };
  }
  return c.json({ home: view, rewards, ...(added ? { added } : {}) });
}

/** 내 둥지와 그 안의 물건 하나(지금 시각으로 맞춘 뒤). */
async function myItem(c: C, id: string) {
  const { user, couple } = await myHome(c);
  const state = await tick(c.env.DB, couple, now());
  const item = state.items.find((i) => i.id === id);
  if (!item) fail(404, "no_item", "그 물건이 없어요.");
  return { user, couple, state, item };
}

function itemState(item: ItemRow, s: unknown, db: D1Database) {
  return db
    .prepare("UPDATE items SET state = ? WHERE id = ? AND state = ?")
    .bind(JSON.stringify(s), item.id, item.state);
}

/** 조건부 쓰기가 다른 요청에 밀렸으면(바뀐 줄 0) 다시 해 달라고 한다. */
function mustChange(result: D1Result | undefined) {
  if (!result || result.meta.changes === 0) fail(409, "busy", "동시에 다른 일이 있었어요. 한 번 더 눌러 주세요.");
}

// ── 로그인 ──────────────────────────────────────────────────────

/** 새 둥지 한 채(처음 깃털, 먼지 시계, 초대 코드). 그 다음에 사람과 처음 물건을 넣는다. */
function newHome(db: D1Database, coupleId: string, name: string, t: number) {
  return db
    .prepare(
      `INSERT INTO couples (id, slug, name, coins, dirt_slot, invite_code, active_at, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .bind(coupleId, newId(8), name, START_COINS, dirtSlot(t), inviteCode(), t, t);
}

/** 처음 둥지에 놓여 있는 물건들 */
function starterItems(db: D1Database, coupleId: string, t: number) {
  return STARTER.map((s, i) => {
    const def = itemDef(s.key)!;
    const state = def.kind === "plant" ? newPlant(t, plantDef(s.key)!, 1) : def.kind === "tank" ? { clarity: 100, t } : null;
    return db
      .prepare(
        `INSERT INTO items (id, couple_id, key, place, x, y, flip, wall, state, created_at)
         VALUES (?, ?, ?, 'room', ?, ?, ?, ?, ?, ?)`,
      )
      .bind(newId(), coupleId, s.key, s.x, s.y, s.flip ? 1 : 0, s.wall ?? null, state ? JSON.stringify(state) : null, t + i);
  });
}

app.post("/auth/signup", async (c) => {
  const b = await body<{ login: string; name: string; pw: string; mode: "create" | "join"; homeName: string; invite: string }>(c);
  const db = c.env.DB;
  // 한 주소에서 하루 스무 번까지만 가입을 시도한다(초대 코드 맞히기, 계정 찍어내기 막기).
  const ip = c.req.header("CF-Connecting-IP") ?? "local";
  if (!(await claim(db, `ip:${ip}`, dayKey(now()), "signup", 20)))
    fail(429, "too_many", "오늘은 이 인터넷에서 가입을 너무 많이 시도했어요. 내일 다시 해 주세요.");
  const login = String(b.login ?? "").trim().toLowerCase();
  if (!LOGIN_RE.test(login)) fail(400, "bad_login", "아이디는 영어 소문자, 숫자, 밑줄(_)로 3~20자예요.");
  const name = text(b.name, "이름", 1, 10);
  const pw = checkClientHash(b.pw);
  if (await db.prepare("SELECT 1 FROM users WHERE login = ?").bind(login).first())
    fail(409, "login_taken", "이미 누가 쓰는 아이디예요.");

  const t = now();
  const userId = newId();
  const salt = newId(16);
  const passHash = await hashForStore(pw, salt);
  const stmts: D1PreparedStatement[] = [];
  let coupleId: string;

  if (b.mode === "join") {
    const code = String(b.invite ?? "").trim().toUpperCase();
    const couple = code ? await db.prepare("SELECT * FROM couples WHERE invite_code = ?").bind(code).first<CoupleRow>() : null;
    if (!couple) fail(404, "bad_invite", "초대 코드를 찾지 못했어요. 상대에게 다시 받아 주세요.");
    coupleId = couple.id;
    stmts.push(
      db
        .prepare("INSERT INTO users (id, login, name, pass_hash, pass_salt, couple_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)")
        .bind(userId, login, name, passHash, salt, coupleId, t),
      db.prepare("UPDATE couples SET invite_code = NULL WHERE id = ?").bind(coupleId),
    );
  } else {
    const homeName = text(b.homeName, "둥지 이름", 1, 16);
    coupleId = newId();
    stmts.push(
      newHome(db, coupleId, homeName, t),
      db
        .prepare("INSERT INTO users (id, login, name, pass_hash, pass_salt, couple_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)")
        .bind(userId, login, name, passHash, salt, coupleId, t),
      ...starterItems(db, coupleId, t),
    );
  }

  await write(db, stmts);
  await startSession(c, userId);
  if (b.mode === "join") notify(c, coupleId, "home");
  return c.json({ me: await meOf(db, userId) });
});

app.post("/auth/login", async (c) => {
  const b = await body<{ login: string; pw: string }>(c);
  const db = c.env.DB;
  const login = String(b.login ?? "").trim().toLowerCase();
  if (!LOGIN_RE.test(login)) fail(401, "bad_login", "아이디나 비밀번호가 맞지 않아요.");
  const pw = checkClientHash(b.pw);
  await guardLogin(db, login);
  const userId = await checkPassword(db, login, pw);
  if (!userId) {
    await noteFail(db, login);
    fail(401, "bad_login", "아이디나 비밀번호가 맞지 않아요.");
  }
  await db.prepare("DELETE FROM login_fails WHERE login = ?").bind(login).run();
  await startSession(c, userId);
  return c.json({ me: await meOf(db, userId) });
});

app.post("/auth/logout", async (c) => {
  await endSession(c);
  return c.json({ ok: true });
});

async function meOf(db: D1Database, userId: string) {
  const me = await db
    .prepare(
      `SELECT u.id, u.login, u.name, u.named, u.couple_id, c.slug AS home,
              EXISTS (SELECT 1 FROM sso_links l WHERE l.user_id = u.id) AS linked
       FROM users u JOIN couples c ON c.id = u.couple_id WHERE u.id = ?`,
    )
    .bind(userId)
    .first<{ id: string; login: string; name: string; named: number; couple_id: string; home: string; linked: number }>();
  if (!me) return null;
  const partner = await db
    .prepare("SELECT id, name FROM users WHERE couple_id = ? AND id <> ?")
    .bind(me.couple_id, me.id)
    .first<{ id: string; name: string }>();
  return {
    id: me.id,
    login: me.login,
    name: me.name,
    home: me.home,
    partner: partner ?? null,
    named: Boolean(me.named),
    via: me.linked ? ("jarvis" as const) : null,
  };
}

// ── jarvis 에서 들어오기 ─────────────────────────────────────────
//
// jarvis 웹(jarvis.choigonyok.com)이 로그인한 사람의 자리(운영자 owner, 손님 guest)를
// JARVIS_SSO_SECRET 으로 서명한 1분짜리 입장권을 만들어 둥지 화면(iframe)을 이 주소로 연다.
//   v1.<who>.<만료 초>.<nonce>.<HMAC-SHA256("dungji:<who>:<만료>:<nonce>")>
// 처음 온 자리는 둥지 계정을 만들어 잇는다. 둘은 같은 둥지에 산다: 먼저 온 쪽이 둥지를 짓고,
// 나중에 온 쪽이 그 둥지로 들어간다. 이름은 처음 들어온 화면이 묻는다(named = 0).

function ssoPage(c: C, title: string, detail: string, status: 400 | 401 | 404) {
  return c.html(
    `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>둥지</title><body style="margin:0;display:grid;place-items:center;min-height:100vh;font-family:system-ui,sans-serif;background:#e9efed;color:#26302d;text-align:center;padding:24px;box-sizing:border-box">
<div><p style="font-size:19px;font-weight:700;margin:0 0 6px">${title}</p><p style="margin:0;color:#5e6b67">${detail}</p></div></body>`,
    status,
  );
}

async function jarvisUser(db: D1Database, who: "owner" | "guest") {
  const link = await db
    .prepare("SELECT user_id FROM sso_links WHERE provider = 'jarvis' AND subject = ?")
    .bind(who)
    .first<{ user_id: string }>();
  if (link) return link.user_id;

  const other = await db
    .prepare(
      `SELECT u.couple_id, (SELECT COUNT(*) FROM users m WHERE m.couple_id = u.couple_id) AS members
       FROM sso_links l JOIN users u ON u.id = l.user_id WHERE l.provider = 'jarvis' AND l.subject = ?`,
    )
    .bind(who === "owner" ? "guest" : "owner")
    .first<{ couple_id: string; members: number }>();
  const t = now();
  const userId = newId();
  const salt = newId(16);
  // 비밀번호로는 들어올 수 없다: 아무도 모르는 값을 다져 둔다.
  const passHash = await hashForStore(newId(32), salt);
  const stmts: D1PreparedStatement[] = [];
  let coupleId = other && other.members < 2 ? other.couple_id : null;
  if (!coupleId) {
    coupleId = newId();
    stmts.push(newHome(db, coupleId, "우리 둥지", t));
  }
  stmts.push(
    db
      .prepare(
        "INSERT INTO users (id, login, name, pass_hash, pass_salt, couple_id, named, created_at) VALUES (?, ?, ?, ?, ?, ?, 0, ?)",
      )
      .bind(userId, `j_${who}_${newId(6)}`, who === "owner" ? "나" : "상대", passHash, salt, coupleId, t),
    db.prepare("INSERT INTO sso_links (provider, subject, user_id) VALUES ('jarvis', ?, ?)").bind(who, userId),
  );
  if (other && coupleId === other.couple_id) stmts.push(db.prepare("UPDATE couples SET invite_code = NULL WHERE id = ?").bind(coupleId));
  else stmts.push(...starterItems(db, coupleId, t));
  await write(db, stmts);
  return userId;
}

app.get("/sso/jarvis", async (c) => {
  const secret = c.env.JARVIS_SSO_SECRET;
  if (!secret) return ssoPage(c, "jarvis 로 들어오기가 꺼져 있어요", "둥지 Worker 에 JARVIS_SSO_SECRET 이 없어요.", 404);
  const parts = (c.req.query("ticket") ?? "").split(".");
  const [v, who, exp, nonce, sig] = parts;
  if (parts.length !== 5 || v !== "v1" || (who !== "owner" && who !== "guest") || !/^\d{9,12}$/.test(exp) || !/^[0-9a-f]{16,64}$/.test(nonce))
    return ssoPage(c, "입장권이 맞지 않아요", "jarvis 에서 둥지를 다시 열어 주세요.", 400);
  if (Number(exp) * 1000 < now()) return ssoPage(c, "입장권이 만료됐어요", "jarvis 에서 둥지를 다시 열어 주세요.", 401);
  if (!sameText(await hmacHex(secret, `dungji:${who}:${exp}:${nonce}`), sig ?? ""))
    return ssoPage(c, "입장권이 맞지 않아요", "jarvis 에서 둥지를 다시 열어 주세요.", 401);
  const db = c.env.DB;
  const used = await db
    .prepare("INSERT OR IGNORE INTO sso_used (nonce, expires_at) VALUES (?, ?)")
    .bind(nonce, Number(exp) * 1000)
    .run();
  if (!used.meta.changes) return ssoPage(c, "이미 쓴 입장권이에요", "jarvis 에서 둥지를 다시 열어 주세요.", 401);
  c.executionCtx.waitUntil(db.prepare("DELETE FROM sso_used WHERE expires_at < ?").bind(now() - 3_600_000).run());
  const userId = await jarvisUser(db, who);
  await startSession(c, userId);
  const me = (await meOf(db, userId))!;
  // 콘솔이 지금 쓰는 테마(밤·낮)와 홈 화면 앱으로 떠 있는지를 화면에 넘긴다(src/lib/theme.ts, src/lib/embed.ts).
  const theme = c.req.query("theme");
  const pass = new URLSearchParams();
  if (theme === "dark" || theme === "light") pass.set("theme", theme);
  if (c.req.query("app") === "1") pass.set("app", "1");
  return c.redirect(`/h/${me.home}${pass.size ? `?${pass}` : ""}`, 302);
});

app.get("/me", async (c) => {
  const user = c.get("user");
  return c.json({ me: user ? await meOf(c.env.DB, user.id) : null });
});

app.patch("/me", async (c) => {
  const user = requireUser(c);
  const b = await body<{ name: string }>(c);
  const name = text(b.name, "이름", 1, 10);
  await c.env.DB.prepare("UPDATE users SET name = ?, named = 1 WHERE id = ?").bind(name, user.id).run();
  notify(c, user.couple_id, "home");
  return c.json({ me: await meOf(c.env.DB, user.id) });
});

// ── 둥지 ────────────────────────────────────────────────────────

app.get("/homes/:slug", async (c) => {
  const user = requireUser(c);
  const db = c.env.DB;
  const couple = await homeBySlug(c, c.req.param("slug"));
  const t = now();
  const day = dayKey(t);
  const rewards: Reward[] = [];
  if (user.couple_id === couple.id) {
    if (await claim(db, user.id, day, "attend", 1)) {
      await write(db, coinStmts(db, couple.id, REWARD.attend, "오늘 첫 방문", user.id, t));
      rewards.push({ delta: REWARD.attend, reason: "오늘 첫 방문" });
    }
    if (t - couple.active_at > 5 * 60_000)
      await db.prepare("UPDATE couples SET active_at = ? WHERE id = ?").bind(t, couple.id).run();
  } else {
    const r = await db
      .prepare("INSERT OR IGNORE INTO visits (home_id, visitor_id, day, at) VALUES (?, ?, ?, ?)")
      .bind(couple.id, user.id, day, t)
      .run();
    if (r.meta.changes > 0) {
      await db.prepare("UPDATE couples SET visits_total = visits_total + 1 WHERE id = ?").bind(couple.id).run();
      notify(c, couple.id, "home");
    } else {
      await db.prepare("UPDATE visits SET at = ? WHERE home_id = ? AND visitor_id = ? AND day = ?").bind(t, couple.id, user.id, day).run();
    }
  }
  return respondHome(c, couple.id, rewards);
});

app.patch("/home", async (c) => {
  const { couple } = await myHome(c);
  const b = await body<{ name: string; intro: string; since: string | null; listed: boolean }>(c);
  const next = {
    name: b.name !== undefined ? text(b.name, "둥지 이름", 1, 16) : couple.name,
    intro: b.intro !== undefined ? text(b.intro, "한 줄 소개", 0, 60) : couple.intro,
    since: couple.since,
    listed: b.listed !== undefined ? (b.listed ? 1 : 0) : couple.listed,
  };
  if (b.since !== undefined) {
    if (b.since !== null && b.since !== "" && (!isDay(b.since) || b.since > dayKey(now())))
      fail(400, "bad_input", "처음 만난 날을 다시 골라 주세요.");
    next.since = b.since || null;
  }
  await c.env.DB.prepare("UPDATE couples SET name = ?, intro = ?, since = ?, listed = ? WHERE id = ?")
    .bind(next.name, next.intro, next.since, next.listed, couple.id)
    .run();
  notify(c, couple.id, "home");
  return respondHome(c, couple.id);
});

app.post("/home/invite", async (c) => {
  const { couple } = await myHome(c);
  const count = await c.env.DB.prepare("SELECT COUNT(*) AS n FROM users WHERE couple_id = ?").bind(couple.id).first<{ n: number }>();
  if ((count?.n ?? 0) >= 2) fail(409, "home_full", "이미 둘이 함께 살고 있어요.");
  await c.env.DB.prepare("UPDATE couples SET invite_code = ? WHERE id = ?").bind(inviteCode(), couple.id).run();
  return respondHome(c, couple.id);
});

app.get("/ledger", async (c) => {
  const { couple } = await myHome(c);
  const rows = await c.env.DB.prepare(
    `SELECT l.delta, l.reason, u.name AS by, l.at FROM ledger l LEFT JOIN users u ON u.id = l.by_id
     WHERE l.couple_id = ? ORDER BY l.at DESC, l.id DESC LIMIT 40`,
  )
    .bind(couple.id)
    .all<LedgerRow>();
  return c.json({ rows: rows.results });
});

// ── 방 꾸미기·창고 ───────────────────────────────────────────────

app.post("/items/:id/move", async (c) => {
  const { couple, state, item } = await myItem(c, c.req.param("id"));
  if (item.place !== "room") fail(409, "not_in_room", "창고에 든 물건은 먼저 방에 꺼내 주세요.");
  const def = itemDef(item.key)!;
  const b = await body<{ x: number; y: number; flip: boolean; wall: "left" | "right" }>(c);
  const x = Number(b.x);
  const y = Number(b.y ?? 0);
  const flip = Boolean(b.flip);
  if (!Number.isInteger(x) || !Number.isInteger(y)) fail(400, "bad_input", "놓을 자리를 다시 골라 주세요.");
  const room = placed(state.items);
  if (def.kind === "wall") {
    const wall = b.wall === "left" ? "left" : "right";
    if (!fitsWall(room, def, wall, x, item.id)) fail(409, "no_room", "그 자리에는 다른 장식이 있어요.");
    await c.env.DB.prepare("UPDATE items SET x = ?, y = 0, flip = 0, wall = ? WHERE id = ?").bind(x, wall, item.id).run();
  } else {
    if (!fitsFloor(room, def, x, y, flip, item.id)) fail(409, "no_room", "그 자리에는 놓을 수 없어요.");
    await c.env.DB.prepare("UPDATE items SET x = ?, y = ?, flip = ? WHERE id = ?").bind(x, y, flip ? 1 : 0, item.id).run();
  }
  notify(c, couple.id, "home");
  return respondHome(c, couple.id);
});

app.post("/items/:id/store", async (c) => {
  const { couple, item } = await myItem(c, c.req.param("id"));
  if (item.place === "storage") return respondHome(c, couple.id);
  await write(c.env.DB, [
    c.env.DB.prepare("UPDATE items SET place = 'storage', stored_at = ? WHERE id = ? AND place = 'room'").bind(now(), item.id),
  ]);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id);
});

app.post("/items/:id/place", async (c) => {
  const { couple, state, item } = await myItem(c, c.req.param("id"));
  if (item.place === "room") return respondHome(c, couple.id);
  const def = itemDef(item.key)!;
  const b = await body<{ x: number; y: number; flip: boolean; wall: "left" | "right" }>(c);
  const room = placed(state.items);
  let spot = findSpot(room, def);
  if (b.x !== undefined) {
    const x = Number(b.x);
    const y = Number(b.y ?? 0);
    const flip = Boolean(b.flip);
    const wall = def.kind === "wall" ? (b.wall === "left" ? "left" : "right") : null;
    const ok = wall ? fitsWall(room, def, wall, x) : fitsFloor(room, def, x, y, flip);
    spot = ok ? { x, y, flip, wall } : null;
  }
  if (!spot) fail(409, "no_room", "방에 빈 자리가 없어요. 다른 물건을 창고에 넣어 주세요.");
  const t = now();
  const stmts = [
    c.env.DB.prepare("UPDATE items SET place = 'room', x = ?, y = ?, flip = ?, wall = ?, stored_at = NULL WHERE id = ?").bind(
      spot.x,
      spot.y,
      spot.flip ? 1 : 0,
      spot.wall,
      item.id,
    ),
  ];
  // 창고에 있던 동안은 시간이 멈춰 있었다: 꺼낸 때부터 다시 흐른다.
  if (item.state) {
    const s = parse<{ t: number }>(item.state);
    stmts.push(c.env.DB.prepare("UPDATE items SET state = ? WHERE id = ?").bind(JSON.stringify({ ...s, t }), item.id));
  }
  if (def.kind === "tank") stmts.push(c.env.DB.prepare("UPDATE fish SET t = ? WHERE tank_id = ?").bind(t, item.id));
  await write(c.env.DB, stmts);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id);
});

app.post("/items/:id/sell", async (c) => {
  const { user, couple, item } = await myItem(c, c.req.param("id"));
  if (item.place !== "storage") fail(409, "not_in_storage", "창고에 넣은 물건만 팔 수 있어요.");
  const def = itemDef(item.key)!;
  const t = now();
  const price = sellPrice(def.price);
  await write(c.env.DB, [
    c.env.DB.prepare("DELETE FROM fish WHERE tank_id = ?").bind(item.id),
    c.env.DB.prepare("DELETE FROM items WHERE id = ?").bind(item.id),
    ...coinStmts(c.env.DB, couple.id, price, `${def.name} 팔기`, user.id, t),
  ]);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, [{ delta: price, reason: `${def.name} 팔기` }]);
});

app.post("/storage/expand", async (c) => {
  const { user, couple } = await myHome(c);
  if (couple.storage_slots >= STORAGE_MAX) fail(409, "max", `창고는 ${STORAGE_MAX}칸까지 넓힐 수 있어요.`);
  const price = expandPrice(couple.storage_slots);
  if (couple.coins < price) fail(409, "no_coins", `깃털이 ${price - couple.coins}개 모자라요.`);
  const t = now();
  const r = await write(c.env.DB, [
    c.env.DB.prepare("UPDATE couples SET storage_slots = storage_slots + 1 WHERE id = ? AND storage_slots = ?").bind(
      couple.id,
      couple.storage_slots,
    ),
    ...coinStmts(c.env.DB, couple.id, -price, "창고 한 칸 넓히기", user.id, t),
  ]);
  mustChange(r[0]);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, [{ delta: -price, reason: "창고 한 칸 넓히기" }]);
});

// ── 상점 ────────────────────────────────────────────────────────

app.post("/shop/item", async (c) => {
  const { user, couple } = await myHome(c);
  const b = await body<{ key: string }>(c);
  const def = itemDef(String(b.key ?? ""));
  if (!def) fail(404, "no_item", "그런 물건은 팔지 않아요.");
  if (couple.coins < def.price) fail(409, "no_coins", `깃털이 ${def.price - couple.coins}개 모자라요.`);
  const t = now();
  const state = await tick(c.env.DB, couple, t);
  const stored = state.items.filter((i) => i.place === "storage").length;
  let where: { place: "room" | "storage"; x: number; y: number; flip: boolean; wall: "left" | "right" | null };
  // 산 물건은 방의 빈자리(벽 쪽부터, 아무것도 가리지 않는 곳)에 바로 놓는다. 방이 차면 창고로.
  const spot = findSpot(placed(state.items), def);
  if (spot) where = { place: "room", ...spot };
  else if (stored < couple.storage_slots) where = { place: "storage", x: 0, y: 0, flip: false, wall: null };
  else fail(409, "storage_full", "방도 창고도 꽉 찼어요. 창고를 넓히거나 물건을 팔아 주세요.");
  const newItemId = newId();
  const itemState =
    def.kind === "plant" ? newPlant(t, plantDef(def.key)!) : def.kind === "tank" ? ({ clarity: 100, t } satisfies TankState) : null;
  await write(c.env.DB, [
    ...coinStmts(c.env.DB, couple.id, -def.price, `${def.name} 사기`, user.id, t),
    c.env.DB.prepare(
      `INSERT INTO items (id, couple_id, key, place, x, y, flip, wall, state, stored_at, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    ).bind(
      newItemId,
      couple.id,
      def.key,
      where.place,
      where.x,
      where.y,
      where.flip ? 1 : 0,
      where.wall,
      itemState ? JSON.stringify(itemState) : null,
      where.place === "storage" ? t : null,
      t,
    ),
  ]);
  notify(c, couple.id, "home");
  return respondHome(
    c,
    couple.id,
    [{ delta: -def.price, reason: where.place === "room" ? `${josa(def.name, "을", "를")} 방에 놓았어요` : `방이 꽉 차서 ${josa(def.name, "을", "를")} 창고에 넣었어요` }],
    { id: newItemId, place: where.place },
  );
});

app.post("/shop/fish", async (c) => {
  const { user, couple } = await myHome(c);
  const b = await body<{ species: string; tankId: string }>(c);
  const fdef = fishDef(String(b.species ?? ""));
  if (!fdef) fail(404, "no_fish", "그런 물고기는 없어요.");
  if (couple.coins < fdef.price) fail(409, "no_coins", `깃털이 ${fdef.price - couple.coins}개 모자라요.`);
  const t = now();
  const state = await tick(c.env.DB, couple, t);
  const tank = state.items.find((i) => i.id === b.tankId && i.place === "room" && itemDef(i.key)?.kind === "tank");
  if (!tank) fail(404, "no_tank", "방에 놓인 어항을 골라 주세요.");
  const cap = tankDef(tank.key)!.capacity;
  if (state.fish.filter((f) => f.tank_id === tank.id).length >= cap) fail(409, "tank_full", `이 어항에는 ${cap}마리까지 살 수 있어요.`);
  await write(c.env.DB, [
    ...coinStmts(c.env.DB, couple.id, -fdef.price, `${fdef.name} 들이기`, user.id, t),
    c.env.DB.prepare(
      "INSERT INTO fish (id, couple_id, tank_id, species, name, hunger, growth, hungry_at, t, created_at) VALUES (?, ?, ?, ?, ?, 100, 0, NULL, ?, ?)",
    ).bind(newId(), couple.id, tank.id, fdef.key, fdef.name, t, t),
  ]);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, [{ delta: -fdef.price, reason: `${fdef.name} 들이기` }]);
});

app.post("/shop/theme", async (c) => {
  const { user, couple } = await myHome(c);
  const b = await body<{ key: string }>(c);
  const def = themeDef(String(b.key ?? ""));
  if (!def) fail(404, "no_theme", "그런 벽지나 바닥은 없어요.");
  const owned = JSON.parse(couple.themes) as string[];
  const column = def.kind === "wallpaper" ? "wallpaper" : "floor";
  const t = now();
  const rewards: Reward[] = [];
  const stmts: D1PreparedStatement[] = [];
  if (!owned.includes(def.key)) {
    if (couple.coins < def.price) fail(409, "no_coins", `깃털이 ${def.price - couple.coins}개 모자라요.`);
    stmts.push(
      ...coinStmts(c.env.DB, couple.id, -def.price, `${def.name} 사기`, user.id, t),
      c.env.DB.prepare("UPDATE couples SET themes = ? WHERE id = ?").bind(JSON.stringify([...owned, def.key]), couple.id),
    );
    rewards.push({ delta: -def.price, reason: `${def.name} 사기` });
  }
  stmts.push(c.env.DB.prepare(`UPDATE couples SET ${column} = ? WHERE id = ?`).bind(def.key, couple.id));
  await write(c.env.DB, stmts);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, rewards);
});

// ── 화분·어항 돌보기 ─────────────────────────────────────────────

app.post("/items/:id/water", async (c) => {
  const { user, couple, state, item } = await myItem(c, c.req.param("id"));
  const pdef = plantDef(item.key);
  if (!pdef || item.place !== "room") fail(409, "not_plant", "방에 놓인 화분에만 물을 줄 수 있어요.");
  const s = parse<PlantState>(item.state);
  if (s.water >= 70) fail(409, "wet", "아직 흙이 촉촉해요.");
  const t = now();
  const next: PlantState = { ...tickPlant(pdef, s, t, state.dirt.length >= MESSY_AT), water: 100, dryAt: null };
  const r = await write(c.env.DB, [
    itemState(item, next, c.env.DB),
    ...coinStmts(c.env.DB, couple.id, REWARD.water, "물 주기", user.id, t),
  ]);
  mustChange(r[0]);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, [{ delta: REWARD.water, reason: "물 주기" }]);
});

app.post("/items/:id/harvest", async (c) => {
  const { user, couple, item } = await myItem(c, c.req.param("id"));
  const pdef = plantDef(item.key);
  if (!pdef || item.place !== "room") fail(409, "not_plant", "방에 놓인 화분만 거둘 수 있어요.");
  const s = parse<PlantState>(item.state);
  if (!plantView(pdef, s, now()).ready) fail(409, "not_ready", "아직 덜 자랐어요.");
  const t = now();
  const reason = `${pdef.harvest} 거두기`;
  const r = await write(c.env.DB, [
    itemState(item, harvestPlant(pdef, s), c.env.DB),
    ...coinStmts(c.env.DB, couple.id, pdef.reward, reason, user.id, t),
  ]);
  mustChange(r[0]);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, [{ delta: pdef.reward, reason }]);
});

app.post("/items/:id/feed", async (c) => {
  const { user, couple, state, item } = await myItem(c, c.req.param("id"));
  if (itemDef(item.key)?.kind !== "tank" || item.place !== "room") fail(409, "not_tank", "방에 놓인 어항을 골라 주세요.");
  const fish = state.fish.filter((f) => f.tank_id === item.id);
  if (!fish.length) fail(409, "no_fish", "어항에 물고기가 없어요. 상점에서 들여 주세요.");
  if (fish.every((f) => f.hunger >= 70)) fail(409, "full", "물고기들이 아직 배불러요.");
  const t = now();
  const stmts = fish.map((f) =>
    c.env.DB.prepare("UPDATE fish SET hunger = 100, hungry_at = NULL, t = ? WHERE id = ? AND t = ?").bind(t, f.id, f.t),
  );
  const r = await write(c.env.DB, [...stmts, ...coinStmts(c.env.DB, couple.id, REWARD.feed, "먹이 주기", user.id, t)]);
  mustChange(r[0]);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, [{ delta: REWARD.feed, reason: "먹이 주기" }]);
});

app.post("/items/:id/scrub", async (c) => {
  const { user, couple, item } = await myItem(c, c.req.param("id"));
  if (itemDef(item.key)?.kind !== "tank" || item.place !== "room") fail(409, "not_tank", "방에 놓인 어항을 골라 주세요.");
  const s = parse<TankState>(item.state);
  if (s.clarity >= 60) fail(409, "clear", "아직 물이 맑아요.");
  const t = now();
  const r = await write(c.env.DB, [
    itemState(item, { clarity: 100, t } satisfies TankState, c.env.DB),
    ...coinStmts(c.env.DB, couple.id, REWARD.scrub, "어항 물 갈기", user.id, t),
  ]);
  mustChange(r[0]);
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, [{ delta: REWARD.scrub, reason: "어항 물 갈기" }]);
});

app.patch("/fish/:id", async (c) => {
  const { couple } = await myHome(c);
  const b = await body<{ name: string }>(c);
  const name = text(b.name, "이름", 1, 8);
  const r = await c.env.DB.prepare("UPDATE fish SET name = ? WHERE id = ? AND couple_id = ?").bind(name, c.req.param("id"), couple.id).run();
  if (!r.meta.changes) fail(404, "no_fish", "그 물고기가 없어요.");
  notify(c, couple.id, "home");
  return respondHome(c, couple.id);
});

app.delete("/fish/:id", async (c) => {
  const { couple } = await myHome(c);
  const r = await c.env.DB.prepare("DELETE FROM fish WHERE id = ? AND couple_id = ?").bind(c.req.param("id"), couple.id).run();
  if (!r.meta.changes) fail(404, "no_fish", "그 물고기가 없어요.");
  notify(c, couple.id, "home");
  return respondHome(c, couple.id);
});

// ── 청소 ────────────────────────────────────────────────────────

app.post("/homes/:slug/dirt/:id/clean", async (c) => {
  const user = requireUser(c);
  const db = c.env.DB;
  const couple = await homeBySlug(c, c.req.param("slug"));
  const id = c.req.param("id");
  const t = now();
  const owner = user.couple_id === couple.id;
  if (owner) {
    const r = await write(db, [
      db.prepare("DELETE FROM dirt WHERE id = ? AND couple_id = ?").bind(id, couple.id),
      ...coinStmts(db, couple.id, REWARD.clean, "청소", user.id, t),
    ]);
    if (!r[0].meta.changes) fail(404, "gone", "이미 치워졌어요.");
    notify(c, couple.id, "home");
    return respondHome(c, couple.id, [{ delta: REWARD.clean, reason: "청소" }]);
  }
  // 이웃이 치워 주면: 하루에 그 방에서 세 개까지, 깃털은 치운 사람의 둥지로.
  const day = dayKey(t);
  await db.prepare("INSERT OR IGNORE INTO visits (home_id, visitor_id, day, at) VALUES (?, ?, ?, ?)").bind(couple.id, user.id, day, t).run();
  const counted = await db
    .prepare("UPDATE visits SET helped = helped + 1 WHERE home_id = ? AND visitor_id = ? AND day = ? AND helped < ?")
    .bind(couple.id, user.id, day, LIMIT.helpClean)
    .run();
  if (!counted.meta.changes) fail(409, "limit", `이웃 방은 하루에 ${LIMIT.helpClean}개까지 치워 줄 수 있어요.`);
  const gone = await db.prepare("DELETE FROM dirt WHERE id = ? AND couple_id = ?").bind(id, couple.id).run();
  if (!gone.meta.changes) {
    await db
      .prepare("UPDATE visits SET helped = helped - 1 WHERE home_id = ? AND visitor_id = ? AND day = ? AND helped > 0")
      .bind(couple.id, user.id, day)
      .run();
    fail(404, "gone", "이미 치워졌어요.");
  }
  await write(db, coinStmts(db, user.couple_id, REWARD.helpClean, `${couple.name} 청소 도와주기`, user.id, t));
  notify(c, couple.id, "home");
  return respondHome(c, couple.id, [{ delta: REWARD.helpClean, reason: "이웃 방 청소" }]);
});

// ── 커플 일기 ────────────────────────────────────────────────────

app.get("/diary", async (c) => {
  const { couple } = await myHome(c);
  const month = c.req.query("month") ?? dayKey(now()).slice(0, 7);
  if (!/^\d{4}-\d{2}$/.test(month)) fail(400, "bad_input", "달을 다시 골라 주세요.");
  const [entries, comments] = await c.env.DB.batch([
    c.env.DB.prepare("SELECT day, author_id FROM diary WHERE couple_id = ? AND day LIKE ?").bind(couple.id, `${month}-%`),
    c.env.DB.prepare("SELECT day, COUNT(*) AS n FROM diary_comments WHERE couple_id = ? AND day LIKE ? GROUP BY day").bind(
      couple.id,
      `${month}-%`,
    ),
  ]);
  const days: DiaryMonth["days"] = {};
  for (const r of entries.results as { day: string; author_id: string }[]) {
    days[r.day] ??= { authors: [], comments: 0 };
    days[r.day].authors.push(r.author_id);
  }
  for (const r of comments.results as { day: string; n: number }[]) {
    days[r.day] ??= { authors: [], comments: 0 };
    days[r.day].comments = r.n;
  }
  return c.json({ month, days } satisfies DiaryMonth);
});

async function diaryDay(db: D1Database, coupleId: string, day: string): Promise<DiaryDay> {
  const [entries, comments] = await db.batch([
    db
      .prepare(
        `SELECT d.author_id AS authorId, u.name AS author, d.mood, d.body, d.updated_at AS updatedAt
         FROM diary d JOIN users u ON u.id = d.author_id WHERE d.couple_id = ? AND d.day = ? ORDER BY d.created_at`,
      )
      .bind(coupleId, day),
    db
      .prepare(
        `SELECT m.id, m.author_id AS authorId, u.name AS author, m.body, m.created_at AS at
         FROM diary_comments m JOIN users u ON u.id = m.author_id WHERE m.couple_id = ? AND m.day = ? ORDER BY m.created_at`,
      )
      .bind(coupleId, day),
  ]);
  return { day, entries: entries.results as unknown as DiaryEntry[], comments: comments.results as unknown as DiaryComment[] };
}

function checkDiaryDay(day: string) {
  if (!isDay(day) || day > dayKey(now())) fail(400, "bad_input", "오늘이나 지난 날의 일기만 쓸 수 있어요.");
}

app.get("/diary/:day", async (c) => {
  const { couple } = await myHome(c);
  const day = c.req.param("day");
  checkDiaryDay(day);
  return c.json(await diaryDay(c.env.DB, couple.id, day));
});

app.put("/diary/:day", async (c) => {
  const { user, couple } = await myHome(c);
  const day = c.req.param("day");
  checkDiaryDay(day);
  const b = await body<{ mood: string; body: string }>(c);
  const mood = MOODS.includes(b.mood as (typeof MOODS)[number]) ? (b.mood as string) : "sun";
  const content = text(b.body, "일기", 1, 2000);
  const t = now();
  await c.env.DB.prepare(
    `INSERT INTO diary (couple_id, day, author_id, mood, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
     ON CONFLICT (couple_id, day, author_id) DO UPDATE SET mood = excluded.mood, body = excluded.body, updated_at = excluded.updated_at`,
  )
    .bind(couple.id, day, user.id, mood, content, t, t)
    .run();
  const rewards: Reward[] = [];
  if (day === dayKey(t) && (await claim(c.env.DB, user.id, day, "diary", 1))) {
    await write(c.env.DB, coinStmts(c.env.DB, couple.id, REWARD.diary, "오늘 일기", user.id, t));
    rewards.push({ delta: REWARD.diary, reason: "오늘 일기" });
  }
  notify(c, couple.id, "diary");
  return c.json({ ...(await diaryDay(c.env.DB, couple.id, day)), rewards });
});

app.delete("/diary/:day", async (c) => {
  const { user, couple } = await myHome(c);
  const day = c.req.param("day");
  checkDiaryDay(day);
  await c.env.DB.prepare("DELETE FROM diary WHERE couple_id = ? AND day = ? AND author_id = ?").bind(couple.id, day, user.id).run();
  notify(c, couple.id, "diary");
  return c.json(await diaryDay(c.env.DB, couple.id, day));
});

app.post("/diary/:day/comments", async (c) => {
  const { user, couple } = await myHome(c);
  const day = c.req.param("day");
  checkDiaryDay(day);
  const b = await body<{ body: string }>(c);
  const content = text(b.body, "댓글", 1, 300);
  await c.env.DB.prepare("INSERT INTO diary_comments (id, couple_id, day, author_id, body, created_at) VALUES (?, ?, ?, ?, ?, ?)")
    .bind(newId(), couple.id, day, user.id, content, now())
    .run();
  notify(c, couple.id, "diary");
  return c.json(await diaryDay(c.env.DB, couple.id, day));
});

app.delete("/diary/comments/:id", async (c) => {
  const { user, couple } = await myHome(c);
  const row = await c.env.DB.prepare("SELECT day FROM diary_comments WHERE id = ? AND couple_id = ? AND author_id = ?")
    .bind(c.req.param("id"), couple.id, user.id)
    .first<{ day: string }>();
  if (!row) fail(404, "no_comment", "지울 댓글이 없어요.");
  await c.env.DB.prepare("DELETE FROM diary_comments WHERE id = ?").bind(c.req.param("id")).run();
  notify(c, couple.id, "diary");
  return c.json(await diaryDay(c.env.DB, couple.id, row.day));
});

// ── 방명록 ──────────────────────────────────────────────────────

async function guestPage(db: D1Database, home: CoupleRow, viewer: UserRow, before: number) {
  const rows = await db
    .prepare(
      `SELECT g.id, g.author_id, g.body, g.secret, g.reply, g.reply_at, g.created_at,
              u.name AS author, c.name AS home, c.slug
       FROM guestbook g JOIN users u ON u.id = g.author_id JOIN couples c ON c.id = u.couple_id
       WHERE g.home_id = ? AND g.created_at < ? ORDER BY g.created_at DESC LIMIT 20`,
    )
    .bind(home.id, before)
    .all<{
      id: string;
      author_id: string;
      body: string;
      secret: number;
      reply: string | null;
      reply_at: number | null;
      created_at: number;
      author: string;
      home: string;
      slug: string;
    }>();
  const owner = viewer.couple_id === home.id;
  return rows.results.map((r): GuestEntry => {
    const mine = r.author_id === viewer.id;
    const hidden = Boolean(r.secret) && !owner && !mine;
    return {
      id: r.id,
      author: r.author,
      authorId: r.author_id,
      home: r.home,
      slug: r.slug,
      body: hidden ? "" : r.body,
      secret: Boolean(r.secret),
      hidden,
      reply: hidden ? null : r.reply,
      replyAt: hidden ? null : r.reply_at,
      at: r.created_at,
      canDelete: owner || mine,
    };
  });
}

app.get("/homes/:slug/guestbook", async (c) => {
  const user = requireUser(c);
  const home = await homeBySlug(c, c.req.param("slug"));
  const before = Number(c.req.query("before") ?? Number.MAX_SAFE_INTEGER) || Number.MAX_SAFE_INTEGER;
  return c.json({ entries: await guestPage(c.env.DB, home, user, before) });
});

app.post("/homes/:slug/guestbook", async (c) => {
  const user = requireUser(c);
  const home = await homeBySlug(c, c.req.param("slug"));
  if (user.couple_id === home.id) fail(409, "own_home", "우리 방명록에는 답글로 이야기해 주세요.");
  const b = await body<{ body: string; secret: boolean }>(c);
  const content = text(b.body, "방명록", 1, 300);
  const t = now();
  if (!(await claim(c.env.DB, user.id, dayKey(t), "guestbook-post", 30)))
    fail(429, "too_many", "오늘은 방명록을 충분히 남겼어요. 내일 또 남겨 주세요.");
  await c.env.DB.prepare("INSERT INTO guestbook (id, home_id, author_id, body, secret, created_at) VALUES (?, ?, ?, ?, ?, ?)")
    .bind(newId(), home.id, user.id, content, b.secret ? 1 : 0, t)
    .run();
  const rewards: Reward[] = [];
  if (await claim(c.env.DB, user.id, dayKey(t), "guestbook", LIMIT.guestbook)) {
    await write(c.env.DB, coinStmts(c.env.DB, user.couple_id, REWARD.guestbook, `${home.name} 방명록`, user.id, t));
    rewards.push({ delta: REWARD.guestbook, reason: "방명록 남기기" });
  }
  notify(c, home.id, "guestbook");
  return c.json({ entries: await guestPage(c.env.DB, home, user, Number.MAX_SAFE_INTEGER), rewards });
});

app.post("/guestbook/:id/reply", async (c) => {
  const user = requireUser(c);
  const entry = await c.env.DB.prepare("SELECT home_id FROM guestbook WHERE id = ?").bind(c.req.param("id")).first<{ home_id: string }>();
  if (!entry || entry.home_id !== user.couple_id) fail(404, "no_entry", "답글을 달 글이 없어요.");
  const b = await body<{ body: string }>(c);
  const content = text(b.body, "답글", 0, 300);
  await c.env.DB.prepare("UPDATE guestbook SET reply = ?, reply_by = ?, reply_at = ? WHERE id = ?")
    .bind(content || null, content ? user.id : null, content ? now() : null, c.req.param("id"))
    .run();
  const home = (await getCouple(c.env.DB, { id: user.couple_id }))!;
  notify(c, home.id, "guestbook");
  return c.json({ entries: await guestPage(c.env.DB, home, user, Number.MAX_SAFE_INTEGER) });
});

app.delete("/guestbook/:id", async (c) => {
  const user = requireUser(c);
  const entry = await c.env.DB.prepare("SELECT home_id, author_id FROM guestbook WHERE id = ?")
    .bind(c.req.param("id"))
    .first<{ home_id: string; author_id: string }>();
  if (!entry || (entry.home_id !== user.couple_id && entry.author_id !== user.id)) fail(404, "no_entry", "지울 글이 없어요.");
  await c.env.DB.prepare("DELETE FROM guestbook WHERE id = ?").bind(c.req.param("id")).run();
  const home = (await getCouple(c.env.DB, { id: entry.home_id }))!;
  notify(c, home.id, "guestbook");
  return c.json({ entries: await guestPage(c.env.DB, home, user, Number.MAX_SAFE_INTEGER) });
});

// ── 이웃 ────────────────────────────────────────────────────────

app.get("/neighbors", async (c) => {
  const user = requireUser(c);
  const q = (c.req.query("q") ?? "").trim().slice(0, 20);
  const like = `%${q.replace(/[\\%_]/g, (m) => `\\${m}`)}%`;
  const rows = await c.env.DB.prepare(
    `SELECT c.slug, c.name, c.intro, c.visits_total, c.active_at, c.dirt_slot,
            (SELECT COUNT(*) FROM dirt d WHERE d.couple_id = c.id) AS spots,
            (SELECT group_concat(u.name, char(10)) FROM users u WHERE u.couple_id = c.id) AS members
     FROM couples c
     WHERE c.listed = 1 AND c.id <> ?
       AND (? = '' OR c.name LIKE ? ESCAPE '\\' OR EXISTS (SELECT 1 FROM users u WHERE u.couple_id = c.id AND u.name LIKE ? ESCAPE '\\'))
     ORDER BY c.active_at DESC LIMIT 30`,
  )
    .bind(user.couple_id, q, like, like)
    .all<{ slug: string; name: string; intro: string; visits_total: number; active_at: number; dirt_slot: number; spots: number; members: string | null }>();
  const t = now();
  const list: Neighbor[] = rows.results.map((r) => ({
    slug: r.slug,
    name: r.name,
    intro: r.intro,
    members: (r.members ?? "").split("\n").filter(Boolean),
    cleanliness: estimateClean(r.spots, r.dirt_slot, t),
    visits: r.visits_total,
    activeAt: r.active_at,
  }));
  return c.json({ neighbors: list });
});

app.get("/neighbors/random", async (c) => {
  const user = requireUser(c);
  const row = await c.env.DB.prepare("SELECT slug FROM couples WHERE listed = 1 AND id <> ? ORDER BY RANDOM() LIMIT 1")
    .bind(user.couple_id)
    .first<{ slug: string }>();
  if (!row) fail(404, "alone", "아직 놀러 갈 이웃 둥지가 없어요.");
  return c.json({ slug: row.slug });
});

/** 최근 2주 동안 우리 둥지에 다녀간 이웃(둥지마다 한 번) */
app.get("/neighbors/visitors", async (c) => {
  const user = requireUser(c);
  const since = dayKey(now() - 14 * 86_400_000);
  const rows = await c.env.DB.prepare(
    `SELECT c.slug, c.name AS home, MAX(v.at) AS at, group_concat(DISTINCT u.name) AS names
     FROM visits v JOIN users u ON u.id = v.visitor_id JOIN couples c ON c.id = u.couple_id
     WHERE v.home_id = ? AND v.day >= ? GROUP BY c.id ORDER BY at DESC LIMIT 20`,
  )
    .bind(user.couple_id, since)
    .all<{ slug: string; home: string; at: number; names: string }>();
  return c.json({ visitors: rows.results.map((r) => ({ slug: r.slug, home: r.home, at: r.at, names: r.names.split(",") })) });
});

// ── 실시간·통화 ──────────────────────────────────────────────────

app.get("/live", async (c) => {
  const user = requireUser(c);
  if (c.req.header("Upgrade") !== "websocket") fail(426, "websocket", "웹소켓으로만 열 수 있어요.");
  const origin = c.req.header("Origin");
  if (origin && new URL(origin).host !== new URL(c.req.url).host) fail(403, "forbidden", "이 화면에서만 열 수 있어요.");
  const headers = new Headers(c.req.raw.headers);
  headers.set("x-user-id", user.id);
  headers.set("x-user-name", encodeURIComponent(user.name));
  return liveStub(c.env, user.couple_id).fetch(new Request("https://live/connect", { headers }));
});

interface IceServer {
  urls: string | string[];
  username?: string;
  credential?: string;
}

app.get("/call/ice", async (c) => {
  requireUser(c);
  const servers: IceServer[] = [{ urls: ["stun:stun.cloudflare.com:3478", "stun:stun.l.google.com:19302"] }];
  const env = c.env;
  if (env.CF_TURN_KEY_ID && env.CF_TURN_KEY_TOKEN) {
    try {
      const r = await fetch(`https://rtc.live.cloudflare.com/v1/turn/keys/${env.CF_TURN_KEY_ID}/credentials/generate`, {
        method: "POST",
        headers: { Authorization: `Bearer ${env.CF_TURN_KEY_TOKEN}`, "Content-Type": "application/json" },
        body: JSON.stringify({ ttl: 6 * 3600 }),
      });
      if (r.ok) {
        const j = (await r.json()) as { iceServers: IceServer | IceServer[] };
        servers.push(...(Array.isArray(j.iceServers) ? j.iceServers : [j.iceServers]));
      }
    } catch {
      // TURN 없이도 대부분의 망에서는 이어진다.
    }
  } else if (env.TURN_URLS) {
    servers.push({ urls: env.TURN_URLS.split(",").map((s) => s.trim()), username: env.TURN_USERNAME, credential: env.TURN_CREDENTIAL });
  }
  return c.json({ iceServers: servers });
});

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext) {
    const url = new URL(request.url);
    if (url.pathname === "/api" || url.pathname.startsWith("/api/")) return app.fetch(request, env, ctx);
    return env.ASSETS.fetch(request);
  },
} satisfies ExportedHandler<Env>;
