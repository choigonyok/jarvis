// jarvis 웹 안의 둥지 화면: 로컬 jarvis(next start)와 로컬 둥지(wrangler dev)를 함께 띄우고 연다.
// jarvis 의 둥지 탭 → /api/nest/ticket → iframe 이 둥지 /api/sso/jarvis → 이름 묻기 → 둥지,
// 그리고 다른 탭에 다녀와도 iframe 이 다시 열리지 않는지(통화가 이어지는지).
//
//   JARVIS=http://localhost:3301 node --experimental-sqlite e2e/jarvis.mjs [.wrangler-test]
// jarvis 는 DUNGJI_URL=http://localhost:8788, DUNGJI_SSO_SECRET=<둥지의 JARVIS_SSO_SECRET> 로,
// 로컬 비밀번호 로그인(JARVIS_AUTH_ID·PASSWORD·SECRET)을 켜고 띄운다. 두 주소가 같은 사이트(localhost)여야
// iframe 안의 둥지 쿠키가 남는다(운영은 jarvis.choigonyok.com·dungji.choigonyok.com).

import { chromium } from "playwright-core";
import { DatabaseSync } from "node:sqlite";
import { mkdirSync, readdirSync } from "node:fs";
import { join } from "node:path";

const JARVIS = process.env.JARVIS ?? "http://localhost:3301";
const PERSIST = process.argv[2] ?? ".wrangler-test";
const d1dir = join(PERSIST, "v3/d1/miniflare-D1DatabaseObject");
const db = new DatabaseSync(join(d1dir, readdirSync(d1dir).find((f) => f.endsWith(".sqlite") && f !== "metadata.sqlite")));
db.prepare("DELETE FROM sso_links").run();
mkdirSync("e2e/shots", { recursive: true });

let failed = 0;
const check = (name, ok, detail) => {
  if (!ok) failed++;
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${!ok && detail !== undefined ? `\n     ${JSON.stringify(detail)}` : ""}`);
};
const visible = (loc, ms = 10_000) => loc.waitFor({ state: "visible", timeout: ms }).then(() => true, () => false);

const browser = await chromium.launch({ channel: "chrome" });
const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, locale: "ko-KR", timezoneId: "Asia/Seoul" });
const page = await ctx.newPage();
const login = await page.request.post(`${JARVIS}/api/auth/login`, {
  data: { id: process.env.JARVIS_AUTH_ID ?? "tester", password: process.env.JARVIS_AUTH_PASSWORD ?? "local-pass-123" },
});
check("jarvis 로컬 로그인", login.ok(), await login.text());

await page.goto(`${JARVIS}/nest`);
const frameEl = page.locator('iframe[title="둥지"]');
check("둥지 탭에 둥지 화면(iframe)이 뜬다", await visible(frameEl));
const frame = page.frameLocator('iframe[title="둥지"]');
const ask = frame.getByRole("dialog", { name: "둥지에 처음 왔어요" });
check("처음 들어오면 이름을 묻는다", await visible(ask));
await ask.getByLabel("내 이름").fill("운영자");
await ask.getByLabel("둥지 이름").fill("시험 둥지");
await ask.getByRole("button", { name: "들어가기" }).click();
check("이름을 정하면 둥지가 보인다", await visible(frame.locator("h1", { hasText: "시험 둥지" })));
check("방이 그려진다", await visible(frame.locator(".room-svg")));
await page.waitForTimeout(800);
await page.screenshot({ path: "e2e/shots/jarvis-nest.png" });

// 테마: 콘솔의 밤이 둥지에도, 머리글의 테마 버튼을 누르면 둥지도 낮으로
const frameTheme = () => frame.locator("html").getAttribute("data-theme");
check("둥지가 콘솔의 테마(밤)로 열린다", (await frameTheme()) === "dark", await frameTheme());
await page.getByRole("button", { name: "라이트 모드로 바꾸기" }).first().click();
await page.waitForTimeout(500);
check("콘솔에서 낮으로 바꾸면 둥지도 낮", (await frameTheme()) === "light", await frameTheme());
await page.screenshot({ path: "e2e/shots/jarvis-nest-light.png" });
await page.getByRole("button", { name: "다크 모드로 바꾸기" }).first().click();
await page.waitForTimeout(500);
check("다시 밤으로", (await frameTheme()) === "dark", await frameTheme());

// 다른 탭에 다녀와도 같은 iframe(다시 열지 않는다): 머리글의 칸 이름으로 오간다.
await frameEl.evaluate((el) => (el.dataset.mark = "kept"));
await page.getByRole("link", { name: "캘린더", exact: true }).first().click();
await page.waitForURL("**/calendar");
check("둥지 화면은 숨고", !(await frameEl.isVisible()));
await page.getByRole("link", { name: "둥지", exact: true }).first().click();
await page.waitForURL("**/nest");
const kept = await page.locator('iframe[title="둥지"]').evaluate((el) => el.dataset.mark);
check("다른 탭에 다녀와도 같은 둥지 화면(다시 열지 않아 통화가 이어진다)", kept === "kept", kept);
check("둥지 화면은 하나만 있다", (await page.locator('iframe[title="둥지"]').count()) === 1);

// 다른 탭을 보는 중에 전화가 오면 콘솔 위에 띠가 뜨고, 누르면 둥지로 간다
const nestFrame = page.frames().find((f) => f.url().includes(":8788"));
await page.getByRole("link", { name: "캘린더", exact: true }).first().click();
await page.waitForURL("**/calendar");
await nestFrame.evaluate(() => window.parent.postMessage({ type: "dungji:call", state: "incoming", name: "바다" }, "http://localhost:3301"));
const banner = page.getByRole("button", { name: /바다님의 전화/ });
check("다른 탭에서도 전화가 온 것이 보인다", await visible(banner, 5000));
await banner.click();
await page.waitForURL("**/nest");
check("띠를 누르면 둥지로 간다", page.url().endsWith("/nest"));
await nestFrame.evaluate(() => window.parent.postMessage({ type: "dungji:call", state: "idle", name: "" }, "http://localhost:3301"));
check("둥지에서는 띠가 없다", !(await banner.isVisible().catch(() => false)));

const me = await page.evaluate(async (base) => {
  const r = await fetch(`${base}/api/nest/ticket`);
  return { status: r.status, url: (await r.json()).url };
}, JARVIS);
check("입장권은 운영자 자리로 서명된다", me.status === 200 && me.url.includes("ticket=v1.owner."), me);

await browser.close();
console.log(failed ? `\n${failed} failed` : "\njarvis 안의 둥지: 모두 통과");
db.close();
process.exit(failed ? 1 : 0);
