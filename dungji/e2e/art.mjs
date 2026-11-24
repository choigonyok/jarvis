// 물건 그림 점검 화면(art.html, src/dev/ArtSheet.tsx)을 찍는다. vite 개발 서버를 직접 띄운다.
//   node e2e/art.mjs [분류…] [--night] [--keys=a,b]   → e2e/shots/art-<분류>-<쪽>.png
// 분류: furniture kitchen light hobby rug plant tank wall (기본: 모두). ART_PORT 로 포트를 바꾼다(기본 5190).

import { spawn } from "node:child_process";
import { mkdirSync } from "node:fs";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const night = args.includes("--night");
const keysArg = args.find((a) => a.startsWith("--keys="));
const groups = args.filter((a) => !a.startsWith("--"));
const GROUPS = groups.length ? groups : ["furniture", "kitchen", "light", "hobby", "rug", "plant", "tank", "wall"];
const PORT = Number(process.env.ART_PORT ?? 5190);
const PER = 6;
const BASE = `http://localhost:${PORT}`;

const vite = spawn("npx", ["vite", "--port", String(PORT), "--strictPort"], { detached: true, stdio: ["ignore", "pipe", "pipe"] });
let log = "";
vite.stdout.on("data", (b) => (log += b));
vite.stderr.on("data", (b) => (log += b));
const stop = () => {
  try {
    process.kill(-vite.pid, "SIGTERM");
  } catch {
    // 이미 끝났다
  }
};
for (let i = 0; ; i++) {
  try {
    if ((await fetch(`${BASE}/art.html`)).ok) break;
  } catch {
    // 뜨는 중
  }
  if (i > 60) {
    console.log(log);
    stop();
    process.exit(1);
  }
  await new Promise((r) => setTimeout(r, 500));
}

mkdirSync("e2e/shots", { recursive: true });
const browser = await chromium.launch({ channel: "chrome" });
const page = await browser.newPage({ viewport: { width: 1180, height: 900 }, deviceScaleFactor: 1 });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
const shots = [];
for (const g of keysArg ? ["keys"] : GROUPS) {
  for (let p = 0; ; p++) {
    const query = keysArg ? `keys=${keysArg.slice(7)}` : `group=${g}`;
    await page.goto(`${BASE}/art.html?${query}&page=${p}&per=${PER}${night ? "&night=1" : ""}`);
    await page.locator("#done").waitFor({ state: "attached" });
    const count = await page.locator("section").count();
    if (!count) break;
    const file = `e2e/shots/art-${g}${night ? "-night" : ""}-${p}.png`;
    await page.screenshot({ path: file, fullPage: true });
    shots.push(file);
    if (count < PER || keysArg) break;
  }
}
await browser.close();
stop();
console.log(shots.join("\n"));
if (errors.length) {
  console.log("화면 오류:\n" + errors.join("\n"));
  process.exit(1);
}
