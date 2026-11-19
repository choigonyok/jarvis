// public/icon.svg 를 홈 화면용 PNG 로 굽는다.
import { chromium } from "playwright-core";
import { readFileSync } from "node:fs";
const svg = readFileSync("public/icon.svg", "utf8");
const browser = await chromium.launch({ channel: "chrome" });
for (const size of [180, 192, 512]) {
  const page = await browser.newPage({ viewport: { width: size, height: size } });
  await page.setContent(`<html><body style="margin:0;background:#CFE6EF">${svg.replace("<svg ", `<svg width="${size}" height="${size}" `)}</body></html>`);
  await page.screenshot({ path: `public/icon-${size}.png`, omitBackground: false });
  await page.close();
}
await browser.close();
console.log("icons ok");
