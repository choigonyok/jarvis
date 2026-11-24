// 화면을 실제로 눌러 보는 시험: 청소, 사기, 꺼내기, 끌어 옮기기, 물 주기, 일기, 방명록,
// 이웃 찾기, 상대가 바꾼 것이 바로 보이는지, 그리고 둘 사이 음성통화가 이어지는지.
//
//   node --experimental-sqlite e2e/flow.mjs [http://127.0.0.1:8788] [.wrangler-test] [chromium|webkit]

import { chromium, webkit } from "playwright-core";
import { DatabaseSync } from "node:sqlite";
import { readdirSync } from "node:fs";
import { join } from "node:path";

const BASE = process.argv[2] ?? "http://127.0.0.1:8788";
const PERSIST = process.argv[3] ?? ".wrangler-test";
const ENGINE = process.argv[4] ?? "chromium";
const d1dir = join(PERSIST, "v3/d1/miniflare-D1DatabaseObject");
const db = new DatabaseSync(join(d1dir, readdirSync(d1dir).find((f) => f.endsWith(".sqlite") && f !== "metadata.sqlite")));
// 시험은 같은 주소에서 여러 번 가입한다: 하루 가입 시도 수를 비운다.
db.prepare("DELETE FROM claims WHERE kind = 'signup'").run();

let failed = 0;
let passed = 0;
function check(name, ok, detail) {
  if (ok) passed++;
  else failed++;
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${!ok && detail !== undefined ? `\n     ${typeof detail === "string" ? detail : JSON.stringify(detail)}` : ""}`);
}

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
  return { cookie: r.cookie, me: r.json.me, login: `${login}_${tag}` };
}

const yun = await signup("fyun", "하늘", { mode: "create", homeName: `숲속 둥지 ${tag}` });
const slug = yun.me.home;
const invite = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home.invite;
const eun = await signup("feun", "바다", { mode: "join", invite });
const moon = await signup("fmoon", "달님", { mode: "create", homeName: `달빛 다락방 ${tag}` });
const homeId = db.prepare("SELECT id FROM couples WHERE slug = ?").get(slug).id;
db.prepare("UPDATE couples SET coins = 1000, dirt_slot = dirt_slot - 3 WHERE id = ?").run(homeId);

const engine = ENGINE === "webkit" ? webkit : chromium;
const browser = await engine.launch(
  ENGINE === "webkit"
    ? {}
    : { channel: "chrome", args: ["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream", "--autoplay-policy=no-user-gesture-required"] },
);
const errors = [];
async function open(user, path, size = { width: 390, height: 844 }) {
  const ctx = await browser.newContext({ viewport: size, hasTouch: false, timezoneId: "Asia/Seoul", locale: "ko-KR", permissions: ENGINE === "webkit" ? [] : ["microphone"] });
  await ctx.addCookies([{ name: "dj", value: user.cookie.split("=")[1], url: BASE }]);
  const page = await ctx.newPage();
  page.on("pageerror", (e) => errors.push(`${user.me.name}: ${e.message}`));
  if (process.env.DEBUG) {
    page.on("request", (r) => r.url().includes("/api/items") && console.log(`  [${user.me.name}] →`, r.method(), r.url().replace(BASE, "")));
    page.on("response", (r) => r.url().includes("/api/items") && console.log(`  [${user.me.name}] ←`, r.status(), r.url().replace(BASE, "")));
  }
  await page.goto(BASE + path);
  await page.locator("h1").first().waitFor();
  return page;
}
const visible = (loc, ms = 6000) => loc.waitFor({ state: "visible", timeout: ms }).then(() => true, () => false);
const toastHas = (page, text) => page.locator(".toast", { hasText: text }).first().waitFor({ timeout: 5000 }).then(() => true, () => false);
/** 먼지 수(가려진 먼지의 표시는 한 번만 센다) */
const dirtCount = (page) => page.evaluate(() => new Set([...document.querySelectorAll("[data-dirt]")].map((e) => e.getAttribute("data-dirt"))).size);
/** 사람이 실제로 누를 수 있는 먼지 하나를 누른다(그 자리의 맨 위 그림이 먼지인 것). */
async function tapDirt(page) {
  const pt = await page.evaluate(() => {
    for (const el of [...document.querySelectorAll("[data-dirt]")].reverse()) {
      el.scrollIntoView({ block: "center" });
      const r = el.getBoundingClientRect();
      const x = r.left + r.width / 2;
      const y = r.top + r.height / 2;
      if (document.elementFromPoint(x, y)?.closest("[data-dirt]") === el) return { x, y };
    }
    return null;
  });
  if (!pt) throw new Error("누를 수 있는 먼지가 없어요");
  await page.mouse.click(pt.x, pt.y);
}

// ── 주인: 청소 ──
const a = await open(yun, `/h/${slug}`);
await a.locator("[data-dirt]").first().waitFor();
check("먼지 세 개가 보인다", (await dirtCount(a)) === 3, await dirtCount(a));
await tapDirt(a);
check("먼지를 누르면 깃털 +3", await toastHas(a, "청소"));
await a.waitForTimeout(300);
check("먼지가 두 개로", (await dirtCount(a)) === 2, await dirtCount(a));

// ── 상점에서 사면 방에 놓이고 바로 고른 채 꾸미기 ──
// 방이 메뉴 줄 위로 넘어갈 만큼 내린 채로 상점을 연다: 사고 나면 방이 다시 보이는 데까지 올라와야 한다.
// (작은 폰 높이에서만 그만큼 내려가므로 이 대목만 화면을 낮춘다.)
await a.setViewportSize({ width: 390, height: 560 });
const hiddenBefore = await a.evaluate(() => {
  const shop = [...document.querySelectorAll("button")].find((b) => b.textContent.includes("상점"));
  window.scrollBy(0, shop.getBoundingClientRect().top - 70);
  const nav = document.querySelector('nav[aria-label="둥지 메뉴"]').getBoundingClientRect();
  return document.querySelector(".sky").getBoundingClientRect().top < nav.bottom;
});
check("(준비) 상점을 열 때 방 위쪽이 화면 밖에 있다", hiddenBefore);
await a.getByRole("button", { name: "상점" }).click();
const card = a.locator("li", { hasText: "낮은 탁자" });
await card.getByRole("button", { name: "사기" }).click();
check("사면 방에 놓았다고 알린다", await toastHas(a, "방에 놓았어요"));
check("상점이 닫히고 꾸미기가 열린다", await visible(a.getByRole("button", { name: "다 꾸몄어요" })));
let state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
const table = state.items.find((i) => i.key === "table_low");
check("탁자가 방에 놓였다", table?.place === "room", table);
const node = a.locator(`[data-item="${table.id}"]`);
check("산 탁자가 골라져 있다", (await node.getAttribute("aria-pressed")) === "true");
// 꾸미기로 들어가며 방과 옮기기 줄이 한 화면에 들어오게 부드럽게 옮긴다: 멈춘 뒤에 잰다.
// (시트를 닫는 뒤로가기가 그 스크롤을 끊거나 상점을 연 자리로 되돌리면 방 위쪽이 메뉴 줄 아래로 숨는다.)
await a.waitForTimeout(900);
const framed = await a.evaluate(() => {
  const nav = document.querySelector('nav[aria-label="둥지 메뉴"]').getBoundingClientRect();
  const room = document.querySelector(".sky").getBoundingClientRect();
  const done = [...document.querySelectorAll("button")].find((b) => b.textContent.includes("다 꾸몄어요")).getBoundingClientRect();
  return { navBottom: Math.round(nav.bottom), roomTop: Math.round(room.top), doneBottom: Math.round(done.bottom), vh: innerHeight };
});
check("산 뒤 방 전체와 옮기기 줄이 한 화면에 보인다", framed.roomTop >= framed.navBottom && framed.doneBottom <= framed.vh, framed);
await a.setViewportSize({ width: 390, height: 844 });
await a.waitForTimeout(200);

// ── 꾸미기: 끌어서, 화살표 버튼으로, 키보드로 ──
const target = { x: 2, y: 5 };
const free = !state.items.some((i) => i.place === "room" && i.x === target.x && i.y === target.y);
const pt = (gx, gy) =>
  a.evaluate(([gx, gy]) => {
    const svg = document.querySelector(".room-svg");
    const p = svg.createSVGPoint();
    p.x = (gx - gy) * 28;
    p.y = (gx + gy) * 14;
    const q = p.matrixTransform(svg.getScreenCTM());
    return { x: q.x, y: q.y };
  }, [gx, gy]);
// 탁자의 윗면 가운데(높이 0.37)를 잡아, 목표 칸 가운데에 놓는다
// 화면에서 높이 h 만큼 위 = 바닥 좌표를 (-h, -h) 옮긴 자리
const top = await pt(table.x + 0.5 - 0.37, table.y + 0.5 - 0.37);
const from = { x: top.x, y: top.y };
const to = await pt(target.x + 0.5, target.y + 0.5);
await a.mouse.move(from.x, from.y);
await a.mouse.down();
for (let k = 1; k <= 8; k++) await a.mouse.move(from.x + ((to.x - from.x) * k) / 8, from.y + ((to.y - from.y) * k) / 8);
await a.mouse.up();
await a.waitForTimeout(600);
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
const moved = state.items.find((i) => i.id === table.id);
check("끌어서 옮긴다", free && moved.x === target.x && moved.y === target.y, { moved, target });
await a.getByRole("button", { name: "오른쪽 아래로" }).click();
await a.waitForTimeout(600);
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
check("화살표 버튼으로 한 칸(오른쪽 아래 = x+1)", state.items.find((i) => i.id === table.id).x === target.x + 1, state.items.find((i) => i.id === table.id));
await a.locator(`[data-item="${table.id}"]`).focus();
await a.keyboard.press("ArrowLeft");
await a.waitForTimeout(600);
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
check("키보드 화살표로 한 칸", state.items.find((i) => i.id === table.id).x === target.x, state.items.find((i) => i.id === table.id));
// 돌리기: ↺ 는 시계 반대로 90도씩, ↻ 는 반대로. 네 방향을 다 돈다(2·3 은 뒷모습).
const turns = [];
for (let k = 0; k < 4; k++) {
  await a.getByRole("button", { name: "시계 반대 방향으로 돌리기" }).click();
  await a.waitForTimeout(500);
  state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
  turns.push(state.items.find((i) => i.id === table.id).turn);
}
check("돌리기 버튼으로 네 방향을 돈다", turns.join(",") === "1,2,3,0", turns);
await a.getByRole("button", { name: "시계 방향으로 돌리기" }).click();
await a.waitForTimeout(500);
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
check("반대로 돌리기", state.items.find((i) => i.id === table.id).turn === 3, state.items.find((i) => i.id === table.id));
await a.locator(`[data-item="${table.id}"]`).focus();
await a.keyboard.press("r");
await a.waitForTimeout(500);
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
check("키보드 R 로 돌리기", state.items.find((i) => i.id === table.id).turn === 0, state.items.find((i) => i.id === table.id));

// ── 창고에 넣었다가 꺼내면 다시 고른 채 꾸미기 ──
await a.getByRole("button", { name: "창고에 넣기" }).click();
await a.waitForTimeout(500);
await a.getByRole("button", { name: "다 꾸몄어요" }).click();
check("창고 1/4", await visible(a.getByRole("button", { name: "창고 1/4" })));
await a.getByRole("button", { name: "창고 1/4" }).click();
await a.getByRole("button", { name: "방에 두기" }).click();
check("꺼내면 바로 꾸미기", await visible(a.getByRole("button", { name: "다 꾸몄어요" })));
await a.getByRole("button", { name: "다 꾸몄어요" }).click();

// ── 꾸미기 밖에서 길게 누르면 들어 올려 옮기고, 꾸미기 중엔 고르지 않은 물건도 처음 누른 채로 끈다 ──
const taken = (items, ignore) => {
  const set = new Set();
  const size = { bed_wood: [2, 3], table_low: [1, 1], lamp_floor: [1, 1], plant_tulip: [1, 1] };
  for (const i of items) {
    if (i.place !== "room" || i.wall || i.id === ignore || !size[i.key]) continue;
    const [w, d] = i.turn % 2 ? [...size[i.key]].reverse() : size[i.key];
    for (let dx = 0; dx < w; dx++) for (let dy = 0; dy < d; dy++) set.add(`${i.x + dx},${i.y + dy}`);
  }
  return set;
};
/** 한 칸짜리 물건 옆의 빈 칸 */
const besideFree = (item, items) => {
  const busy = taken(items, item.id);
  return [[1, 0], [0, 1], [-1, 0], [0, -1]].map(([dx, dy]) => ({ x: item.x + dx, y: item.y + dy })).find((c) => c.x >= 0 && c.y >= 0 && c.x < 7 && c.y < 7 && !busy.has(`${c.x},${c.y}`));
};
/** 마우스로 (바닥 칸 가운데) a → b 끌기. hold 만큼 누른 채 기다렸다가 움직인다. */
const dragCell = async (from, to, hold = 0) => {
  const p = await pt(from.x + 0.5, from.y + 0.5);
  const q = await pt(to.x + 0.5, to.y + 0.5);
  await a.mouse.move(p.x, p.y);
  await a.mouse.down();
  if (hold) await a.waitForTimeout(hold);
  for (let k = 1; k <= 8; k++) await a.mouse.move(p.x + ((q.x - p.x) * k) / 8, p.y + ((q.y - p.y) * k) / 8);
  await a.mouse.up();
  await a.waitForTimeout(600);
};
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
const lamp = state.items.find((i) => i.key === "lamp_floor");
const lampTo = besideFree(lamp, state.items);
await dragCell(lamp, lampTo, 700);
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
const lampNow = state.items.find((i) => i.id === lamp.id);
check("꾸미기 밖에서 길게 누르면 들어 올려 옮긴다", lampNow.x === lampTo.x && lampNow.y === lampTo.y, { lampNow, lampTo });
check("길게 눌러 옮기면 그 물건을 고른 채 꾸미기", (await visible(a.getByRole("button", { name: "다 꾸몄어요" }), 2000)) && (await a.locator(`[data-item="${lamp.id}"]`).getAttribute("aria-pressed")) === "true");
const tulip0 = state.items.find((i) => i.key === "plant_tulip");
const tulipTo = besideFree(tulip0, state.items);
await dragCell(tulip0, tulipTo);
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
const tulipNow = state.items.find((i) => i.id === tulip0.id);
check("꾸미기 중 고르지 않은 물건도 처음 누른 채로 끌어 옮긴다", tulipNow.x === tulipTo.x && tulipNow.y === tulipTo.y, { tulipNow, tulipTo });
// 빈 바닥을 눌렀다 떼면 고른 것을 놓을 뿐, 물건이 그리로 옮겨 가지 않는다
const busyNow = taken(state.items);
const empty = [...Array(49).keys()].map((n) => ({ x: n % 7, y: Math.floor(n / 7) })).reverse().find((c) => !busyNow.has(`${c.x},${c.y}`) && !(c.x >= 3 && c.x <= 4 && c.y >= 3 && c.y <= 4));
const ep = await pt(empty.x + 0.5, empty.y + 0.5);
await a.mouse.click(ep.x, ep.y);
await a.waitForTimeout(600);
const afterTap = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
check(
  "빈 바닥을 누르면 고른 것만 놓는다(물건이 순간이동하지 않는다)",
  afterTap.items.every((i) => {
    const b4 = state.items.find((x) => x.id === i.id);
    return b4.x === i.x && b4.y === i.y;
  }) && (await a.locator('.room-svg [aria-pressed="true"]').count()) === 0,
);
await a.getByRole("button", { name: "다 꾸몄어요" }).click();

// ── 가구를 누르면 옮기기·창고에 넣기 시트 ──
await a.locator(`[data-item="${table.id}"]`).focus();
await a.keyboard.press("Enter");
check("가구 시트", await visible(a.getByRole("dialog", { name: "낮은 탁자" })));
await a.getByRole("button", { name: "닫기" }).click();

// ── 화분 물 주기 ──
const tulip = state.items.find((i) => i.key === "plant_tulip");
db.prepare("UPDATE items SET state = ? WHERE id = ?").run(JSON.stringify({ water: 10, growth: 20, dryAt: null, t: Date.now() }), tulip.id);
await a.waitForTimeout(300);
await a.reload();
await a.getByRole("button", { name: "물 주기" }).first().click();
check("목마른 화분에 물 주기 +2", await toastHas(a, "물 주기"));
state = (await call(yun.cookie, "GET", `/homes/${slug}`)).json.home;
check("화분의 물이 가득", state.items.find((i) => i.id === tulip.id).plant.water === 100);

// ── 일기 ──
await a.getByRole("button", { name: "일기" }).first().click();
await a.getByLabel("일기", { exact: true }).fill("오늘은 탁자를 옮겼다.");
await a.getByRole("button", { name: "일기 저장" }).click();
check("일기 저장 +15", await toastHas(a, "오늘 일기"));
check("쓴 일기가 보인다", await a.getByText("오늘은 탁자를 옮겼다.").isVisible());

// ── 상대 화면: 실시간으로 바뀌는 것 ──
const b = await open(eun, `/h/${slug}`);
await b.waitForTimeout(800);
check("상대 화면에도 같은 방(먼지 둘)", (await dirtCount(b)) === 2, await dirtCount(b));
await tapDirt(b);
await a.getByRole("button", { name: "우리 방" }).first().click();
await a.waitForTimeout(1500);
check("상대가 치우면 내 화면에서도 사라진다(새로 고침 없이)", (await dirtCount(a)) === 1, await dirtCount(a));

// ── 이웃이 놀러 와서 청소를 돕고 방명록을 남긴다 ──
const c = await open(moon, `/h/${slug}`);
check("놀러 온 이웃에게 도움 안내", await c.getByText("오늘 3개 더 치워 줄 수 있어요").isVisible());
await tapDirt(c);
check("이웃 청소 +2", await toastHas(c, "이웃 방 청소"));
await c.getByLabel("방명록에 남길 말").fill("탁자가 귀여워요!");
await c.getByRole("button", { name: "남기기" }).click();
check("방명록 +5", await toastHas(c, "방명록"));
check("남긴 방명록이 보인다", await c.getByText("탁자가 귀여워요!").first().isVisible());
await a.getByRole("button", { name: "방명록" }).first().click();
check("주인 방명록에 이웃 글", await visible(a.getByText("탁자가 귀여워요!").first(), 3000));
await a.getByRole("button", { name: "답글" }).first().click();
await a.getByLabel("답글").fill("고마워요");
await a.getByRole("button", { name: "달기" }).click();
check("답글", await visible(a.getByText("고마워요").first(), 3000));

// ── 이웃 찾기와 놀러 가기 ──
await a.getByRole("button", { name: "이웃" }).first().click();
await a.getByPlaceholder("둥지나 사람 이름").fill("달빛 다락방");
await a.getByRole("link", { name: new RegExp(`달빛 다락방 ${tag}`) }).first().click();
await a.locator("h1", { hasText: `달빛 다락방 ${tag}` }).waitFor();
check("이웃 둥지로 놀러 간다", a.url().includes(moon.me.home));
await a.getByRole("link", { name: "우리 둥지", exact: true }).click();
await a.getByPlaceholder("둥지나 사람 이름").waitFor();
check("우리 둥지로 돌아오면 보던 이웃 칸이 다시 열린다", a.url().includes(slug) && a.url().includes("tab=neighbors"), a.url());
await a.getByRole("button", { name: "우리 방" }).first().click();
await a.locator("h1", { hasText: `숲속 둥지 ${tag}` }).waitFor();

// ── 음성통화 ──
if (ENGINE === "chromium") {
  await a.waitForTimeout(800);
  const callBtn = a.getByRole("button", { name: "바다님과 통화하기" });
  check("상대가 들어와 있으면 통화 버튼이 켜진다", await callBtn.isEnabled());
  await callBtn.click();
  check("거는 중", await visible(a.getByText("바다님에게 거는 중"), 3000));
  const ring = b.getByRole("alertdialog", { name: "하늘님의 전화" });
  check("상대에게 전화가 온다", await visible(ring, 8000));
  await ring.getByRole("button", { name: "받기" }).click();
  const talking = (page, name) => visible(page.getByText(new RegExp(`${name}님과 통화 중`)), 15000);
  await a.getByText(/바다님과 통화 중/).waitFor({ timeout: 15000 }).catch(() => undefined);
  check("건 쪽: 연결됨(통화 중)", await talking(a, "바다"));
  check("받은 쪽: 연결됨(통화 중)", await talking(b, "하늘"));
  await b.waitForTimeout(1500);
  const audio = await b.evaluate(() => {
    const el = document.querySelector("audio[data-role=call-audio]");
    const tracks = el?.srcObject?.getAudioTracks?.() ?? [];
    return { tracks: tracks.length, live: tracks.some((t) => t.readyState === "live"), paused: el?.paused };
  });
  check("상대의 소리가 들어온다(오디오 트랙 재생 중)", audio.tracks > 0 && audio.live && audio.paused === false, audio);
  await a.getByRole("button", { name: "마이크 끄기" }).click();
  check("음소거", await a.getByRole("button", { name: "마이크 켜기" }).isVisible());
  await a.getByRole("button", { name: "끊기" }).click();
  check("끊으면 상대 화면에서도 통화가 끝난다", await visible(b.getByText("통화가 끝났어요"), 5000));
  check("통화 줄이 사라진다", !(await b.getByText(/통화 중/).isVisible().catch(() => false)));
}

await browser.close();
if (errors.length) check("화면 오류 없음", false, errors.join("\n"));
console.log(`\n${ENGINE}: ${passed} passed, ${failed} failed`);
db.close();
process.exit(failed ? 1 : 0);
