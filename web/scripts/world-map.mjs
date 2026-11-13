// Builds public/maps/world.json, the 세계 지도 block's outline of the world:
// one path per country under the ISO 3166-1 alpha-2 code uniple keeps its
// visits by, its Korean name and its continent.
//
//   curl -LO https://raw.githubusercontent.com/nvkelso/natural-earth-vector/v5.1.2/geojson/ne_50m_admin_0_countries.geojson
//   node scripts/world-map.mjs ne_50m_admin_0_countries.geojson public/maps/world.json
//
// The boundaries are Natural Earth's 1:50m admin-0 countries (public domain).
// mapshaper (fetched by npx) merges each code's pieces, projects them in the
// Natural Earth projection the app draws its map in, and thins them; the rest
// happens here. A country too small to see is drawn as a dot to tap.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [input, output] = process.argv.slice(2);
if (!input || !output) {
  console.error("usage: node scripts/world-map.mjs <ne_50m_admin_0_countries.geojson> <out.json>");
  process.exit(2);
}

const MAPSHAPER = ["-y", "mapshaper@0.6.102"];
const WIDTH = 1000;

// Names as Korean speakers say them where Natural Earth's are formal.
const NAMES = {
  KP: "북한", KR: "대한민국", TW: "대만", CN: "중국", AU: "호주", TR: "튀르키예", CD: "콩고민주공화국", CG: "콩고",
  CF: "중앙아프리카공화국", DO: "도미니카공화국", ZA: "남아프리카공화국", VA: "바티칸", FM: "미크로네시아",
};
const CONTINENT = {
  Asia: "asia", Europe: "europe", Africa: "africa", "North America": "northAmerica", "South America": "southAmerica", Oceania: "oceania",
};
// The open sea's islands, by where they lie; the rest are 기타.
const SEAS = { MV: "asia", SC: "africa", MU: "africa" };

const dir = mkdtempSync(join(tmpdir(), "world-map-"));
const projected = join(dir, "projected.json");
execFileSync(
  "npx",
  [
    ...MAPSHAPER,
    input,
    "-filter", "ISO_A2_EH !== '-99' && ISO_A2_EH !== 'AQ'",
    "-dissolve", "ISO_A2_EH", "copy-fields=NAME_KO,CONTINENT",
    "-proj", "+proj=natearth +lon_0=10",
    "-simplify", "12%", "keep-shapes",
    "-o", projected, "format=geojson", "precision=1",
  ],
  { stdio: "inherit" },
);

const fc = JSON.parse(readFileSync(projected, "utf8"));
let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
const rings = (g) => (g.type === "Polygon" ? g.coordinates : g.type === "MultiPolygon" ? g.coordinates.flat() : []);
for (const f of fc.features) {
  for (const ring of rings(f.geometry)) {
    for (const [x, y] of ring) {
      minX = Math.min(minX, x); maxX = Math.max(maxX, x);
      minY = Math.min(minY, y); maxY = Math.max(maxY, y);
    }
  }
}
const scale = WIDTH / (maxX - minX);
const HEIGHT = Math.ceil((maxY - minY) * scale);
const px = ([x, y]) => [(x - minX) * scale, (maxY - y) * scale];
const r1 = (n) => Math.round(n * 10) / 10;

const countries = [];
for (const f of fc.features) {
  const code = f.properties.ISO_A2_EH;
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  const parts = [];
  // The largest piece's box: where the map goes to show the country, not
  // the ocean between it and its far islands.
  let main = null;
  for (const ring of rings(f.geometry)) {
    const pts = ring.map(px);
    for (const [x, y] of pts) {
      x0 = Math.min(x0, x); x1 = Math.max(x1, x); y0 = Math.min(y0, y); y1 = Math.max(y1, y);
    }
    // A ring too small to see is not drawn as a ring.
    const w = Math.max(...pts.map((p) => p[0])) - Math.min(...pts.map((p) => p[0]));
    const h = Math.max(...pts.map((p) => p[1])) - Math.min(...pts.map((p) => p[1]));
    if (!main || w * h > main[2] * main[3]) main = [Math.min(...pts.map((p) => p[0])), Math.min(...pts.map((p) => p[1])), w, h];
    if (w < 0.4 && h < 0.4) continue;
    let d = "";
    let last = "";
    for (const [i, p] of pts.entries()) {
      const s = `${r1(p[0])} ${r1(p[1])}`;
      if (s === last) continue;
      d += (i === 0 ? "M" : "L") + s;
      last = s;
    }
    parts.push(d + "Z");
  }
  const box = [r1(x0), r1(y0), r1(x1 - x0), r1(y1 - y0)];
  const dot = box[2] < 2.5 && box[3] < 2.5;
  let d = parts.join("");
  if (dot || !d) {
    // A dot a finger can find, where the country is.
    const cx = (x0 + x1) / 2, cy = (y0 + y1) / 2, r = 1.4;
    d = "M" + Array.from({ length: 12 }, (_, i) => `${r1(cx + r * Math.cos((i * Math.PI) / 6))} ${r1(cy + r * Math.sin((i * Math.PI) / 6))}`).join("L") + "Z";
    box.splice(0, 4, r1(cx - r), r1(cy - r), r1(2 * r), r1(2 * r));
  }
  const continent = CONTINENT[f.properties.CONTINENT] ?? SEAS[code] ?? "etc";
  const focus = main && !dot && (main[2] < box[2] * 0.8 || main[3] < box[3] * 0.8) ? main.map(r1) : undefined;
  countries.push({ code, name: NAMES[code] ?? f.properties.NAME_KO, continent, d, box, ...(focus ? { focus } : {}), ...(dot ? { dot: true } : {}) });
}
countries.sort((a, b) => a.code.localeCompare(b.code));

writeFileSync(
  output,
  JSON.stringify({
    source: "Natural Earth 1:50m admin-0 countries (public domain)",
    viewBox: `0 0 ${WIDTH} ${HEIGHT}`,
    countries,
  }),
);
console.log(`${countries.length} countries, ${WIDTH}x${HEIGHT}, ${(readFileSync(output).length / 1024).toFixed(0)}KB`);
