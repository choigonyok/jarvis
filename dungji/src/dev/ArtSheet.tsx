// 물건 그림 점검 화면(개발용: vite dev 의 /art.html, 빌드에는 들어가지 않는다).
// 바닥 물건은 네 방향(0 왼쪽 아래를 봄, 1 오른쪽 아래, 2 오른쪽 위 = 뒷모습, 3 왼쪽 위 = 뒷모습)으로, 벽 장식은 벽 한 조각에.
//   /art.html?group=furniture|kitchen|light|hobby|rug|plant|tank|wall|all&page=0&per=8&night=1&keys=a,b
// e2e/art.mjs 가 이 화면을 찍는다.

import { createRoot } from "react-dom/client";
import { ITEMS, type ItemDef } from "../../shared/catalog";
import { footprint } from "../../shared/rules";
import { drawItem, drawWall } from "../room/art";
import { proj, pts, RIGHT_WALL_FRAME, WALL_H } from "../room/iso";
import type { Turn } from "../room/model";

const q = new URLSearchParams(location.search);
const group = q.get("group") ?? "all";
const keys = q.get("keys")?.split(",").filter(Boolean);
const night = q.get("night") === "1";
const per = Number(q.get("per") ?? 8);
const page = Number(q.get("page") ?? 0);

function inGroup(d: ItemDef) {
  if (keys) return keys.includes(d.key);
  switch (group) {
    case "all":
      return true;
    case "rug":
      return Boolean(d.under);
    case "plant":
    case "tank":
    case "wall":
      return d.kind === group;
    default:
      return d.kind === "floor" && !d.under && (d.group ?? "furniture") === group;
  }
}

const list = ITEMS.filter(inGroup);
const shown = list.slice(page * per, page * per + per);
const now = new Date(2026, 10, 23, night ? 22 : 14, 10);
const ctx = (id: string) => ({
  id,
  night,
  now,
  sky: night ? "#1E2A4A" : "#CFE6EF",
  plant: { water: 90, stage: 4, maxStage: 4, ready: false, wilted: false, nextHours: null, thirsty: false },
  tank: {
    clarity: 100,
    fish: [0, 1, 2].map((i) => ({ id: `f${i}`, species: ["fish_gold", "fish_guppy", "fish_neon"][i], name: "", hunger: 100, grown: 1, adult: true, sick: false })),
  },
});

function FloorCell({ def, turn }: { def: ItemDef; turn: Turn }) {
  const { w, d } = footprint(def, turn);
  const [left] = proj(0, d);
  const [right] = proj(w, 0);
  const [, bottom] = proj(w, d);
  const top = -(Math.max(def.h, 0.3) + 0.5) * 28;
  const pad = 10;
  const tiles = [];
  for (let i = 0; i < w; i++)
    for (let j = 0; j < d; j++)
      tiles.push(<polygon key={`${i},${j}`} points={pts([[i, j, 0], [i + 1, j, 0], [i + 1, j + 1, 0], [i, j + 1, 0]])} fill={night ? "#3B3F52" : "#E8DCC7"} stroke={night ? "#565B72" : "#D4C4A8"} strokeWidth={0.8} />);
  return (
    <svg viewBox={`${left - pad} ${top - pad} ${right - left + pad * 2} ${bottom - top + pad * 2}`} style={{ width: (right - left + pad * 2) * 1.25, height: "auto", display: "block" }}>
      {tiles}
      {drawItem(def.key, ctx(`${def.key}-${turn}`), turn)}
    </svg>
  );
}

function WallCell({ def }: { def: ItemDef }) {
  const span = def.w + 0.4;
  return (
    <svg viewBox={`-6 ${-WALL_H * 28 - 6} ${span * 28 + 12} ${span * 14 + WALL_H * 28 + 12}`} style={{ width: (span * 28 + 12) * 1.6, height: "auto", display: "block" }}>
      <g transform={RIGHT_WALL_FRAME}>
        <rect x={0} y={0} width={span} height={WALL_H} fill={night ? "#3A3F55" : "#F1ECE2"} />
        <g transform="translate(0.2 0)">{drawWall(def.key, 0, ctx(def.key))}</g>
      </g>
    </svg>
  );
}

function Sheet() {
  return (
    <main style={{ padding: 16, background: night ? "#22263A" : undefined, color: night ? "#E8E6F0" : undefined }}>
      <p style={{ margin: "0 0 12px", fontSize: 13 }}>
        {group} · {page * per + 1}–{page * per + shown.length} / {list.length} {night ? "· 밤" : ""}
      </p>
      {shown.map((def) => (
        <section key={def.key} style={{ display: "flex", alignItems: "flex-end", gap: 18, padding: "10px 0", borderTop: "1px solid rgba(0,0,0,.08)" }}>
          <div style={{ width: 120, fontSize: 13, lineHeight: 1.35 }}>
            <b>{def.name}</b>
            <br />
            <code style={{ fontSize: 11, opacity: 0.7 }}>{def.key}</code>
            <br />
            <span style={{ fontSize: 11, opacity: 0.7 }}>
              {def.kind === "wall" ? `벽 ${def.w}` : `${def.w}×${def.d}, 높이 ${def.h}`}
            </span>
          </div>
          {def.kind === "wall" ? (
            <WallCell def={def} />
          ) : (
            ([0, 1, 2, 3] as Turn[]).map((t) => <FloorCell key={t} def={def} turn={t} />)
          )}
        </section>
      ))}
      <span id="done" />
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<Sheet />);
