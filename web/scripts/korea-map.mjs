// Builds public/maps/korea.json, the date map's outline of Korea: one path per
// 시/군/구 under the region codes uniple keeps its visits by, and one outline
// per 시/도.
//
//   curl -LO https://raw.githubusercontent.com/vuski/admdongkor/master/ver20261001/HangJeongDong_ver20261001.geojson
//   node scripts/korea-map.mjs HangJeongDong_ver20261001.geojson public/maps/korea.json
//
// The boundaries are Statistics Korea's (SGIS, KOGL Type 1) as corrected and
// published by vuski/admdongkor under CC BY 4.0 - the map screen names both.
// mapshaper (fetched by npx) dissolves the 3,500 행정동 into 시/군/구,
// projects them (UTM-K) and thins them; the rest happens here.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [input, output] = process.argv.slice(2);
if (!input || !output) {
  console.error("usage: node scripts/korea-map.mjs <HangJeongDong.geojson> <out.json>");
  process.exit(2);
}

const MAPSHAPER = ["-y", "mapshaper@0.6.102"];
const UTMK = "+proj=tmerc +lat_0=38 +lon_0=127.5 +k=0.9996 +x_0=1000000 +y_0=2000000 +ellps=GRS80 +units=m +no_defs";
const WIDTH = 1000;

// The 시/도 by the codes uniple uses, in its order.
const SIDO = {
  11: "서울", 21: "부산", 22: "대구", 23: "인천", 24: "광주", 25: "대전", 26: "울산", 29: "세종",
  31: "경기", 32: "강원", 33: "충북", 34: "충남", 35: "전북", 36: "전남", 37: "경북", 38: "경남", 39: "제주",
};

// 광주 and 전남 are one 시/도 (12) in the boundaries since the 2026 merger;
// uniple still files them under 24 and 36.
const MERGED = { 12060: "36010", 12070: "36020", 12080: "36030", 12090: "36040", 12100: "36060" };
function uniple(code) {
  if (!code.startsWith("12")) return code;
  if (MERGED[code]) return MERGED[code];
  const n = Number(code.slice(2));
  return (n <= 50 ? "24" : "36") + code.slice(2);
}

const dir = mkdtempSync(join(tmpdir(), "korea-map-"));
const regionsRaw = join(dir, "regions-raw.json");
const regionsFile = join(dir, "regions.json");
const sidoFile = join(dir, "sido.json");

// A city's 일반구 (수원시 장안구, 31011) is the city (31010): its last digit
// is not 0. The city's name is the first word.
execFileSync("npx", [
  ...MAPSHAPER, "-i", input,
  "-each", "c = adm_cd.substr(0, 5); code = c[4] !== '0' ? c.substr(0, 4) + '0' : c; name = sggnm.split(' ')[0]",
  "-dissolve", "code", "copy-fields=name",
  "-proj", UTMK,
  "-simplify", "weighted", "7%", "keep-shapes",
  "-o", "format=geojson", "precision=1", regionsRaw,
], { stdio: "inherit" });

const raw = JSON.parse(readFileSync(regionsRaw, "utf8"));
for (const f of raw.features) {
  f.properties.code = uniple(f.properties.code);
  f.properties.sido = f.properties.code.slice(0, 2);
  // A city's 일반구 is named with it, sometimes without a space ("고양시덕양구"):
  // the region is the city.
  f.properties.name = f.properties.name.replace(/^(.+?시)\s?\S+구$/, "$1");
}

// 울릉군 sits 130km out to sea: brought in to just off the coast, as maps of
// Korea draw it, or the country is a third of the width.
const ulleung = raw.features.find((f) => f.properties.name === "울릉군");
const mainland = raw.features.filter((f) => f !== ulleung);
const box = (features) => {
  let [x0, y0, x1, y1] = [Infinity, Infinity, -Infinity, -Infinity];
  for (const f of features)
    for (const poly of polygons(f.geometry))
      for (const ring of poly)
        for (const [x, y] of ring) {
          x0 = Math.min(x0, x); y0 = Math.min(y0, y); x1 = Math.max(x1, x); y1 = Math.max(y1, y);
        }
  return [x0, y0, x1, y1];
};
if (ulleung) {
  const [mx0, , mx1] = box(mainland);
  const [ux0] = box([ulleung]);
  const dx = mx1 - (mx1 - mx0) * 0.02 - ux0;
  for (const poly of polygons(ulleung.geometry)) for (const ring of poly) for (const p of ring) p[0] += dx;
}
writeFileSync(regionsFile, JSON.stringify(raw));

execFileSync("npx", [...MAPSHAPER, "-i", regionsFile, "-dissolve", "sido", "-o", "format=geojson", "precision=1", sidoFile], {
  stdio: "inherit",
});
const sido = JSON.parse(readFileSync(sidoFile, "utf8"));

// North up, scaled to a viewBox WIDTH wide. The frame holds the land and the
// big islands; the far specks (백령도, 가거도) fall outside it rather than
// leaving a quarter of the map as sea - their 군 still shows by its other
// islands.
const BIG = 60e6; // m², 백령도 is 51 km², 울릉도 73
const [x0, y0, x1, y1] = (() => {
  let [a, b, c, e] = [Infinity, Infinity, -Infinity, -Infinity];
  for (const f of raw.features)
    for (const poly of polygons(f.geometry)) {
      const ring = poly[0];
      let s = 0;
      for (let i = 0; i < ring.length - 1; i++) s += ring[i][0] * ring[i + 1][1] - ring[i + 1][0] * ring[i][1];
      if (Math.abs(s) / 2 < BIG) continue;
      for (const [x, y] of ring) {
        a = Math.min(a, x); b = Math.min(b, y); c = Math.max(c, x); e = Math.max(e, y);
      }
    }
  const m = (c - a) * 0.02;
  return [a - m, b - m, c + m, e + m];
})();
const k = WIDTH / (x1 - x0);
const height = Math.round((y1 - y0) * k);
const at = ([x, y]) => [(x - x0) * k, (y1 - y) * k];
const round = (v) => Math.round(v * 10) / 10;

function polygons(g) {
  if (!g) return [];
  return g.type === "Polygon" ? [g.coordinates] : g.type === "MultiPolygon" ? g.coordinates : [];
}

// Relative moves, a tenth of a unit: a third the size of absolute ones. The
// pen's place is kept in tenths so the rounding never drifts.
function path(g) {
  const parts = [];
  let pen = null;
  const pair = (x, y) => `${x / 10} ${y / 10}`.replace(/ -/g, "-");
  for (const poly of polygons(g)) {
    for (const ring of poly) {
      const pts = ring.map(at).map(([x, y]) => [Math.round(x * 10), Math.round(y * 10)]);
      if (pts.length > 1 && pts[0][0] === pts.at(-1)[0] && pts[0][1] === pts.at(-1)[1]) pts.pop();
      if (pts.length < 3) continue;
      pts.forEach(([x, y], i) => {
        if (i === 0) parts.push(pen === null ? `M${pair(x, y)}` : `m${pair(x - pen[0], y - pen[1])}`);
        else parts.push((i === 1 ? "l" : " ") + pair(x - pen[0], y - pen[1]));
        pen = [x, y];
      });
      parts.push("z");
      // A closed subpath leaves the pen where it began.
      pen = pts[0];
    }
  }
  return parts.join("").replace(/ -/g, "-");
}

function bounds(g) {
  let [a, b, c, e] = [Infinity, Infinity, -Infinity, -Infinity];
  for (const poly of polygons(g))
    for (const ring of poly)
      for (const p of ring) {
        const [x, y] = at(p);
        a = Math.min(a, x); b = Math.min(b, y); c = Math.max(c, x); e = Math.max(e, y);
      }
  return [round(a), round(b), round(c - a), round(e - b)];
}

// Where a name goes: the middle of the largest piece's box (an island
// county's name sits on its main island, not in the sea between them).
function label(g) {
  let best = null;
  let area = -1;
  for (const poly of polygons(g)) {
    const pts = poly[0].map(at);
    let s = 0;
    for (let i = 0; i < pts.length - 1; i++) s += pts[i][0] * pts[i + 1][1] - pts[i + 1][0] * pts[i][1];
    if (Math.abs(s) > area) {
      area = Math.abs(s);
      const xs = pts.map((p) => p[0]);
      const ys = pts.map((p) => p[1]);
      best = [round((Math.min(...xs) + Math.max(...xs)) / 2), round((Math.min(...ys) + Math.max(...ys)) / 2)];
    }
  }
  return best;
}

const regions = raw.features
  .map((f) => ({
    code: f.properties.code,
    sido: f.properties.sido,
    name: f.properties.name,
    d: path(f.geometry),
    box: bounds(f.geometry),
    at: label(f.geometry),
  }))
  .sort((a, b) => a.code.localeCompare(b.code));

const provinces = sido.features
  .map((f) => ({ code: String(f.properties.sido), name: SIDO[f.properties.sido], d: path(f.geometry), box: bounds(f.geometry), at: label(f.geometry) }))
  .sort((a, b) => a.code.localeCompare(b.code));

const out = {
  source:
    "통계청 통계지리정보서비스(SGIS) 행정동 경계(공공누리 제1유형)를 vuski/admdongkor 가 가공한 것을 시/군/구로 묶음 · CC BY 4.0",
  viewBox: `0 0 ${WIDTH} ${height}`,
  provinces,
  regions,
};
writeFileSync(output, JSON.stringify(out));
console.log(`${regions.length} regions, ${provinces.length} provinces, ${(JSON.stringify(out).length / 1024).toFixed(0)}KB -> ${output}`);
