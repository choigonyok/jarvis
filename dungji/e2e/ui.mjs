// 실제 브라우저로 화면을 연다: 폰·패드·컴퓨터 크기, 낮·밤, 주인·이웃.
// 로컬 Worker 에 시험 둥지를 만들고, 시간이 지나야 생기는 상태는 로컬 D1 을 고쳐 만든다.
//
//   node --experimental-sqlite e2e/ui.mjs [http://127.0.0.1:8788] [.wrangler-test]

import { chromium, webkit } from "playwright-core";
import { DatabaseSync } from "node:sqlite";
import { mkdirSync, readdirSync } from "node:fs";
import { join } from "node:path";

const BASE = process.argv[2] ?? "http://127.0.0.1:8788";
const PERSIST = process.argv[3] ?? ".wrangler-test";
const OUT = "e2e/shots";
mkdirSync(OUT, { recursive: true });
const d1dir = join(PERSIST, "v3/d1/miniflare-D1DatabaseObject");
const db = new DatabaseSync(join(d1dir, readdirSync(d1dir).find((f) => f.endsWith(".sqlite") && f !== "metadata.sqlite")));
// 시험은 같은 주소에서 여러 번 가입한다: 하루 가입 시도 수를 비운다.
db.prepare("DELETE FROM claims WHERE kind = 'signup'").run();

async function pbkdf2(login, pw) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(pw), "PBKDF2", false, ["deriveBits"]);
  const bits = await crypto.subtle.deriveBits({ name: "PBKDF2", hash: "SHA-256", salt: new TextEncoder().encode(`dungji:v1:${login}`), iterations: 600_000 }, key, 256);
  return [...new Uint8Array(bits)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

async function call(cookie, method, path, body) {
  const res = await fetch(BASE + "/api" + path, { method, headers: { "content-type": "application/json", "x-dungji": "1", cookie }, body: body ? JSON.stringify(body) : undefined });
  const set = res.headers.get("set-cookie");
  return { json: await res.json(), cookie: set ? set.split(";")[0] : cookie };
}

const tag = Date.now().toString(36).slice(-4);
async function signup(login, name, extra) {
  const r = await call("", "POST", "/auth/signup", { login: `${login}_${tag}`, name, pw: await pbkdf2(`${login}_${tag}`, "password1"), ...extra });
  if (!r.json.me) throw new Error(JSON.stringify(r.json));
  return { cookie: r.cookie, me: r.json.me };
}

// ── 시험 둥지 ──
const yun = await signup("yun", "하늘", { mode: "create", homeName: "햇살 드는 둥지" });
const slug = yun.me.home;
let home = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
const eun = await signup("eun", "바다", { mode: "join", invite: home.invite });
const moon = await signup("moon", "달님", { mode: "create", homeName: "달빛 다락방" });
await signup("star", "별이", { mode: "create", homeName: "별 헤는 집" });

db.prepare("UPDATE couples SET coins = 3000, since = '2024-05-01', intro = '고양이 대신 물고기를 키워요' WHERE slug = ?").run(slug);
for (const key of ["sofa_two", "tank_small", "shelf_book", "plant_sunflower"]) await call(yun.cookie, "POST", "/shop/item", { key });
home = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
for (const i of home.items.filter((x) => x.place === "storage")) await call(yun.cookie, "POST", `/items/${i.id}/place`);
home = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
const id = (key) => home.items.find((i) => i.key === key)?.id;
await call(yun.cookie, "POST", `/items/${id("sofa_two")}/move`, { x: 3, y: 0, flip: false });
await call(yun.cookie, "POST", `/items/${id("shelf_book")}/move`, { x: 2, y: 0, flip: false });
await call(yun.cookie, "POST", `/items/${id("tank_small")}/move`, { x: 6, y: 2, flip: false });
await call(yun.cookie, "POST", `/items/${id("plant_sunflower")}/move`, { x: 6, y: 0, flip: false });
for (const species of ["fish_gold", "fish_neon", "fish_betta"]) await call(yun.cookie, "POST", "/shop/fish", { species, tankId: id("tank_small") });
for (const key of ["desk", "cat_tower"]) await call(yun.cookie, "POST", "/shop/item", { key }); // 창고에 둔다
// 해바라기는 다 폈고, 튤립은 목마르게
db.prepare("UPDATE items SET state = ? WHERE id = ?").run(JSON.stringify({ water: 80, growth: 96, dryAt: null, t: Date.now() }), id("plant_sunflower"));
db.prepare("UPDATE items SET state = ? WHERE id = ?").run(JSON.stringify({ water: 12, growth: 30, dryAt: null, t: Date.now() }), id("plant_tulip"));
db.prepare("UPDATE fish SET hunger = 20, growth = 40, t = ? WHERE tank_id = ?").run(Date.now(), id("tank_small"));
db.prepare("UPDATE couples SET dirt_slot = dirt_slot - 5 WHERE slug = ?").run(slug);
const today = new Date(Date.now() + 9 * 3600_000).toISOString().slice(0, 10);
await call(eun.cookie, "PUT", `/diary/${today}`, { mood: "heart", body: "퇴근길에 붕어빵을 사 왔다. 팥이 가득!" });
await call(moon.cookie, "GET", `/homes/${slug}`);
await call(moon.cookie, "POST", `/homes/${slug}/guestbook`, { body: "어항 물고기들 너무 귀여워요. 또 놀러 올게요!" });
await call(moon.cookie, "POST", `/homes/${slug}/guestbook`, { body: "몰래 하는 말", secret: true });
await call(yun.cookie, "GET", `/homes/${slug}`);

const VIEWS = [
  { name: "phone", width: 390, height: 844, mobile: true },
  { name: "pad", width: 820, height: 1180, mobile: true },
  { name: "desk", width: 1280, height: 860, mobile: false },
];

const problems = [];
async function shoot(browser, who, view, path, file, { night = false, dark = false, before } = {}) {
  const ctx = await browser.newContext({
    colorScheme: dark ? "dark" : "light",
    viewport: { width: view.width, height: view.height },
    deviceScaleFactor: 2,
    isMobile: view.mobile && browser.browserType().name() === "chromium",
    hasTouch: view.mobile,
    timezoneId: "Asia/Seoul",
    locale: "ko-KR",
  });
  if (who) await ctx.addCookies([{ name: "dj", value: who.cookie.split("=")[1], url: BASE }]);
  const page = await ctx.newPage();
  page.on("pageerror", (e) => problems.push(`${file}: ${e.message}`));
  page.on("console", (m) => m.type() === "error" && !m.text().includes("WebSocket") && problems.push(`${file}: ${m.text()}`));
  await page.clock.setFixedTime(new Date(`${today}T${night ? "22:30" : "14:00"}:00+09:00`));
  await page.goto(BASE + path);
  await page.waitForLoadState("networkidle").catch(() => undefined);
  await page.waitForTimeout(500);
  if (before) await before(page);
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  if (over > 0) problems.push(`${file}: 가로로 ${over}px 넘침`);
  await page.screenshot({ path: `${OUT}/${file}.png`, fullPage: true });
  await ctx.close();
}

const chrome = await chromium.launch({ channel: "chrome" });
for (const v of VIEWS) {
  await shoot(chrome, null, v, "/", `${v.name}-login`);
  await shoot(chrome, yun, v, `/h/${slug}`, `${v.name}-home`);
}
await shoot(chrome, yun, VIEWS[0], `/h/${slug}`, "phone-home-night", { night: true });
await shoot(chrome, yun, VIEWS[0], `/h/${slug}?tab=diary`, "phone-diary");
await shoot(chrome, yun, VIEWS[0], `/h/${slug}`, "phone-home-dark", { night: true, dark: true });
await shoot(chrome, yun, VIEWS[1], `/h/${slug}?tab=diary`, "pad-diary-dark", { dark: true });
await shoot(chrome, yun, VIEWS[0], `/h/${slug}?tab=guestbook`, "phone-guestbook-owner");
await shoot(chrome, yun, VIEWS[0], `/h/${slug}?tab=neighbors`, "phone-neighbors");
await shoot(chrome, moon, VIEWS[0], `/h/${slug}`, "phone-visit");
await shoot(chrome, moon, VIEWS[1], `/h/${slug}?tab=guestbook`, "pad-visit-guestbook");
await shoot(chrome, yun, VIEWS[0], `/h/${slug}`, "phone-shop", { before: (p) => p.getByRole("button", { name: "상점" }).click().then(() => p.waitForTimeout(400)) });
await shoot(chrome, yun, VIEWS[0], `/h/${slug}`, "phone-storage", { before: (p) => p.getByRole("button", { name: /창고 \d/ }).click().then(() => p.waitForTimeout(400)) });
await shoot(chrome, yun, VIEWS[1], `/h/${slug}`, "pad-care-tank", {
  before: async (p) => {
    await p.locator(".room-tappable").filter({ has: p.locator("ellipse") }).nth(0).waitFor();
    const tank = await p.evaluate(() => [...document.querySelectorAll(".room-tappable")].findIndex((g) => g.querySelector(".fish-swim")));
    await p.locator(".room-tappable").nth(tank).click({ force: true });
    await p.waitForTimeout(400);
  },
});
await shoot(chrome, yun, VIEWS[0], `/h/${slug}`, "phone-edit", {
  before: async (p) => {
    await p.getByRole("button", { name: "꾸미기" }).click();
    await p.locator(".room-editable").first().dispatchEvent("pointerdown");
    await p.waitForTimeout(300);
  },
});
await chrome.close();

const safari = await webkit.launch();
await shoot(safari, yun, VIEWS[0], `/h/${slug}`, "webkit-phone-home");
await shoot(safari, null, VIEWS[0], "/", "webkit-phone-login");
await safari.close();

console.log(problems.length ? problems.join("\n") : "문제 없음");
console.log(`slug=${slug}`);
