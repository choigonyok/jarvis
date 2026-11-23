// 상대에게 알리기: 둘 중 한 사람이 우리 둥지에서 한 일이 상대의 jarvis 알림(게이트웨이의 Nest 문)이나
// 둥지 안의 한 줄로 가는지를 실제 Worker 런타임에서 본다(worker/live.ts, worker/tell.ts).
//
// jarvis 게이트웨이(jarvis-be) 자리에는 tell 을 받아 찍기만 하는 가짜를 함께 띄운다. 가짜는 저장소에 두지
// 않고 임시 폴더에 만든다: 이름이 jarvis-be 라서 잘못 배포하면 진짜 게이트웨이를 덮는다.
// 이 시험이 Worker 를 직접 띄운다(.wrangler-tell 에, 따로 띄운 wrangler dev 는 필요 없다). 떠난 뒤 기다리는
// 시간은 TELL_AFTER_MS 로 줄인다. 둥지 안의 한 줄은 크롬 화면에서 본다.
//
//   npm run build && node e2e/tell.mjs [포트, 기본 8790]

import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright-core";

const PORT = Number(process.argv[2] ?? 8790);
const BASE = `http://127.0.0.1:${PORT}`;
const PERSIST = ".wrangler-tell";
const GRACE = 1500;
const SSO_SECRET = process.env.JARVIS_SSO_SECRET ?? "local-test-secret-for-dungji";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let failed = 0;
let passed = 0;
function check(name, ok, detail) {
  if (ok) passed++;
  else failed++;
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${!ok && detail !== undefined ? `\n     ${JSON.stringify(detail)}` : ""}`);
}

// ── 가짜 게이트웨이와 Worker ──
const fake = mkdtempSync(join(tmpdir(), "dungji-tell-"));
writeFileSync(join(fake, "wrangler.jsonc"), JSON.stringify({ name: "jarvis-be", main: "fake.mjs", compatibility_date: "2025-09-01" }));
writeFileSync(
  join(fake, "fake.mjs"),
  `import { WorkerEntrypoint } from "cloudflare:workers";
export class Nest extends WorkerEntrypoint {
  async tell(n) {
    console.log("TOLD " + JSON.stringify(n));
    return { ok: true, status: 201 };
  }
}
export default { fetch: () => new Response("가짜 게이트웨이", { status: 404 }) };
`,
);
rmSync(PERSIST, { recursive: true, force: true });
mkdirSync(PERSIST);
execFileSync("npx", ["wrangler", "d1", "migrations", "apply", "dungji", "--local", "--persist-to", PERSIST], { stdio: "ignore" });

let log = "";
const dev = spawn(
  "npx",
  ["wrangler", "dev", "-c", "wrangler.jsonc", "-c", join(fake, "wrangler.jsonc"), "--port", String(PORT), "--persist-to", PERSIST, "--var", `TELL_AFTER_MS:${GRACE}`],
  { detached: true, stdio: ["ignore", "pipe", "pipe"], env: { ...process.env, NO_COLOR: "1" } },
);
dev.stdout.on("data", (b) => (log += b));
dev.stderr.on("data", (b) => (log += b));
let browser;
async function finish(code) {
  await browser?.close().catch(() => undefined);
  try {
    process.kill(-dev.pid, "SIGTERM");
  } catch {
    // 이미 끝났다.
  }
  rmSync(fake, { recursive: true, force: true });
  process.exit(code);
}
for (let i = 0; ; i++) {
  try {
    if ((await fetch(`${BASE}/api/me`)).ok) break;
  } catch {
    // 아직 뜨는 중
  }
  if (i > 120) {
    console.log(log);
    console.log("Worker 가 뜨지 않았습니다");
    await finish(1);
  }
  await sleep(500);
}

// ── 둘(운영자·손님)과 이웃 ──
class Client {
  constructor(cookie = "") {
    this.cookie = cookie;
  }
  async req(method, path, body) {
    const res = await fetch(BASE + "/api" + path, {
      method,
      headers: { "content-type": "application/json", "x-dungji": "1", cookie: this.cookie },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const set = res.headers.get("set-cookie");
    if (set) this.cookie = set.split(";")[0];
    const json = await res.json().catch(() => null);
    return { status: res.status, ...json };
  }
  get = (p) => this.req("GET", p);
  post = (p, b = {}) => this.req("POST", p, b);
  put = (p, b = {}) => this.req("PUT", p, b);
  patch = (p, b = {}) => this.req("PATCH", p, b);
}

const hex = (buf) => [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
/** 콘솔이 만드는 것과 같은 1분짜리 입장권(web/src/app/api/nest/ticket) */
async function ticket(who) {
  const exp = Math.floor(Date.now() / 1000) + 60;
  const nonce = hex(crypto.getRandomValues(new Uint8Array(16)));
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(SSO_SECRET), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = hex(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`dungji:${who}:${exp}:${nonce}`)));
  return `v1.${who}.${exp}.${nonce}.${sig}`;
}
async function enter(who) {
  const res = await fetch(`${BASE}/api/sso/jarvis?ticket=${encodeURIComponent(await ticket(who))}`, { redirect: "manual" });
  return new Client((res.headers.get("set-cookie") ?? "").split(";")[0]);
}

const owner = await enter("owner");
const guest = await enter("guest");
await owner.patch("/me", { name: "하늘" });
await guest.patch("/me", { name: "바다" });
const ownerMe = (await owner.get("/me")).me;
const guestMe = (await guest.get("/me")).me;
check("둘이 한 둥지에", ownerMe?.home && ownerMe.home === guestMe?.home, { ownerMe, guestMe });
const slug = ownerMe.home;
const today = new Date(Date.now() + 9 * 3_600_000).toISOString().slice(0, 10);

const toldAll = () => [...log.matchAll(/TOLD (\{.*\})/g)].map((m) => JSON.parse(m[1]));
let seen = 0;
/** 지금까지 본 뒤로 새로 간 알림(GRACE 와 넉넉한 여유만큼 기다린다) */
async function told(ms = GRACE + 3500) {
  const until = Date.now() + ms;
  while (Date.now() < until) {
    if (toldAll().length > seen) break;
    await sleep(200);
  }
  await sleep(300);
  const all = toldAll();
  const fresh = all.slice(seen);
  seen = all.length;
  return fresh;
}

function live(client) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(BASE.replace("http", "ws") + "/api/live", { headers: { cookie: client.cookie } });
    const inbox = [];
    ws.onmessage = (e) => inbox.push(JSON.parse(e.data));
    ws.onerror = () => reject(new Error("ws error"));
    ws.onopen = () =>
      resolve({
        inbox,
        seen: (on) => ws.send(JSON.stringify({ type: "seen", on })),
        close: () => ws.close(),
      });
  });
}

// ── 상대가 둥지에 없으면: 한 일을 모아 한 통 ──
await owner.put(`/diary/${today}`, { mood: "sun", body: "시험 일기" });
await owner.post(`/diary/${today}/comments`, { body: "시험 댓글" });
await owner.post("/shop/item", { key: "snack_box" });
let t = await told();
check("한 일 셋이 한 통으로 손님에게", t.length === 1 && t[0].to === "guest", t);
check("제목은 가장 중요한 일, 본문은 나머지", t[0]?.title === "하늘이 오늘 일기를 썼어요" && t[0]?.body === "둥지 · 일기에 댓글을 달았어요 · 간식 상자를 샀어요", t[0]);
check("같은 한 통의 키는 한 사람과 처음 한 때", new RegExp(`^${ownerMe.id}:\\d+$`).test(t[0]?.key ?? ""), t[0]?.key);
t = await told(2500);
check("보낸 것은 다시 보내지 않는다", t.length === 0, t);

// ── 상대가 보고 있으면: 둥지 안에만 ──
const guestLive = await live(guest);
guestLive.seen(true);
await sleep(300);
await owner.post(`/diary/${today}/comments`, { body: "또 댓글" });
await sleep(800);
const changed = guestLive.inbox.find((m) => m.type === "changed" && m.acts?.length);
check("보고 있는 상대의 창에 무엇을 했는지 간다", changed?.name === "하늘" && changed.acts[0]?.k === "comment", changed);
t = await told();
check("상대가 보고 있으면 jarvis 로는 보내지 않는다", t.length === 0, t);

// ── 상대 창이 보이지 않으면(콘솔의 다른 칸, 꺼진 화면): jarvis 로 ──
guestLive.seen(false);
await sleep(300);
await owner.post("/shop/item", { key: "cushion" });
t = await told();
check("상대 창이 숨으면 jarvis 로", t.length === 1 && t[0].to === "guest" && t[0].title === "하늘이 방석 두 개를 샀어요", t);
guestLive.close();

// ── 한 사람이 둥지에 있는 동안은 모으고, 떠나면 보낸다 ──
const ownerLive = await live(owner);
ownerLive.seen(true);
await sleep(300);
await owner.post("/shop/item", { key: "snack_box" });
await owner.put(`/diary/${today}`, { mood: "rain", body: "고친 일기" });
t = await told();
check("둥지에 머무는 동안은 보내지 않는다", t.length === 0, t);
ownerLive.seen(false);
t = await told();
check("떠나면(보이지 않으면) 그동안 한 일을 한 통으로", t.length === 1 && t[0].title === "하늘이 간식 상자를 샀어요" && t[0].body === "둥지 · 오늘 일기를 고쳤어요", t);
ownerLive.seen(true);
await sleep(300);
await owner.patch("/home", { intro: "시험 소개" });
ownerLive.close();
t = await told();
check("창을 닫아도 떠난 것", t.length === 1 && t[0].title === "하늘이 한 줄 소개를 바꿨어요", t);

// ── 반대로: 손님이 한 일은 운영자에게 ──
await guest.put(`/diary/${today}`, { mood: "sun", body: "손님 일기" });
t = await told();
check("손님이 한 일은 운영자의 jarvis 로", t.length === 1 && t[0].to === "owner" && t[0].title === "바다가 오늘 일기를 썼어요", t);

// ── 알리지 않는 것: 지운 것, 이웃이 한 일 ──
await guest.req("DELETE", `/diary/${today}`);
const neighbor = new Client();
const pw = hex(await crypto.subtle.digest("SHA-256", new TextEncoder().encode("이웃")));
await neighbor.post("/auth/signup", { login: `nb_${Date.now() % 100000}`, name: "이웃", pw, mode: "create", homeName: "이웃 둥지" });
await neighbor.get(`/homes/${slug}`);
await neighbor.post(`/homes/${slug}/guestbook`, { body: "놀러 왔어요" });
t = await told();
check("지운 것과 이웃이 한 일은 알리지 않는다", t.length === 0, t);

// ── 화면: 보고 있는 상대에게 한 줄 ──
process.on("unhandledRejection", async (e) => {
  console.log(`FAIL ${String(e)}`);
  await finish(1);
});
browser = await chromium.launch({ channel: "chrome" });
const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, timezoneId: "Asia/Seoul", locale: "ko-KR" });
await ctx.addCookies([{ name: "dj", value: guest.cookie.split("=")[1], url: BASE }]);
const page = await ctx.newPage();
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
await page.goto(`${BASE}/h/${slug}`);
await page.locator("h1").first().waitFor();
await sleep(800);
const plant = (await owner.get(`/homes/${slug}`)).home.items.find((i) => i.key.startsWith("plant_") && i.place === "room");
await owner.post(`/diary/${today}/comments`, { body: "화면 시험" });
const line = page.locator(".toast", { hasText: "하늘이 일기에 댓글을 달았어요" }).first();
check(
  "보고 있는 상대의 화면에 한 줄",
  await line.waitFor({ timeout: 5000 }).then(
    () => true,
    () => false,
  ),
);
mkdirSync("e2e/shots", { recursive: true });
await sleep(500); // 내려오는 움직임이 끝난 뒤
await page.screenshot({ path: "e2e/shots/tell-line.png" });
t = await told();
check("화면을 보고 있으면 jarvis 로는 보내지 않는다", t.length === 0, t);
await page.close();
await sleep(500);
if (plant) {
  await owner.post(`/items/${plant.id}/move`, { x: plant.x, y: plant.y, flip: !plant.flip });
  t = await told();
  check("화면을 닫은 상대에게는 jarvis 로", t.length === 1 && t[0].title === "하늘이 방을 꾸몄어요", t);
}

// ── 콘솔 안의 둥지: 콘솔이 다른 칸으로 가면 프레임은 숨은 채 살아 있다(web/src/components/nest/host.tsx) ──
// 콘솔 자리에는 프레임 하나만 띄우는 쪽을 둔다. 둘 다 localhost 라 같은 사이트(프레임 안의 쿠키가 남는다).
const HOST_PORT = PORT + 1;
const NEST = `http://localhost:${PORT}`;
let frameSrc = "";
const host = createServer((_, res) => {
  res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  res.end(`<!doctype html><meta charset="utf-8"><iframe id="nest" src="${frameSrc}" style="width:390px;height:700px;border:0"></iframe>`);
});
await new Promise((r) => host.listen(HOST_PORT, r));
frameSrc = `${NEST}/api/sso/jarvis?ticket=${encodeURIComponent(await ticket("guest"))}`;
const console2 = await (await browser.newContext({ timezoneId: "Asia/Seoul", locale: "ko-KR" })).newPage();
console2.on("pageerror", (e) => errors.push(e.message));
await console2.goto(`http://localhost:${HOST_PORT}/`);
await console2.frameLocator("#nest").locator("h1").first().waitFor();
await sleep(800);
const tellShown = (shown) =>
  console2.evaluate(([shown, origin]) => document.getElementById("nest").contentWindow.postMessage({ type: "dungji:shown", shown }, origin), [shown, NEST]);
await tellShown(false);
await sleep(500);
await owner.post(`/diary/${today}/comments`, { body: "숨은 프레임" });
t = await told();
check("콘솔의 다른 칸에 가 있으면(프레임이 숨으면) jarvis 로", t.length === 1 && t[0].to === "guest" && t[0].title === "하늘이 일기에 댓글을 달았어요", t);
await tellShown(true);
await sleep(500);
await owner.post(`/diary/${today}/comments`, { body: "보이는 프레임" });
t = await told();
check("둥지 칸으로 돌아오면 다시 둥지 안에만", t.length === 0, t);
host.close();
check("화면 오류 없음", errors.length === 0, errors);

console.log(`\n${passed} 통과, ${failed} 실패`);
if (failed) console.log(log.split("\n").filter((l) => /error|TOLD|상대에게/i.test(l)).slice(-30).join("\n"));
await finish(failed ? 1 : 0);
