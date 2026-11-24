// 상점·창고·돌보기에 쓰는 물건 하나의 작은 그림.

import { fishDef, itemDef, themeDef } from "../../shared/catalog";
import type { ItemView } from "../../shared/api";
import { drawItem, drawWall } from "./art";
import { proj } from "./iso";
import { FLOORS, WALLPAPER } from "./Room";

const HEIGHT: Record<string, number> = {
  bed_wood: 1.1,
  sofa_two: 0.9,
  table_low: 0.6,
  table_dining: 0.9,
  chair_wood: 1.05,
  shelf_book: 2.25,
  lamp_floor: 1.85,
  desk: 1.25,
  fridge_mini: 1.45,
  record_player: 0.75,
  tv_stand: 1.45,
  wardrobe: 2.45,
  cat_tower: 2.05,
  easel: 1.65,
  cushion: 0.2,
  snack_box: 0.7,
  rug_round: 0.05,
  rug_stripe: 0.05,
  tank_small: 1.35,
  tank_big: 1.35,
};

export function ItemThumb({ itemKey, view, className = "size-16" }: { itemKey: string; view?: ItemView; className?: string }) {
  const def = itemDef(itemKey);
  if (!def) return null;
  const now = new Date();
  if (def.kind === "wall")
    return (
      <svg viewBox={`-0.1 0.3 ${def.w + 0.2} 1.95`} className={className} aria-hidden>
        <g>{drawWall(def.key, 0, { id: "thumb", night: false, now, sky: "#CFE6EF" })}</g>
      </svg>
    );
  const h = def.kind === "plant" ? 1.9 : (HEIGHT[def.key] ?? Math.max(def.h + 0.08, 0.3));
  const [left] = proj(0, def.d);
  const [right] = proj(def.w, 0);
  const [, bottom] = proj(def.w, def.d);
  const top = -h * 28;
  const pad = 6;
  const plant = view?.plant ?? (def.kind === "plant" ? { water: 90, stage: 4, maxStage: 4, ready: false, wilted: false, nextHours: null, thirsty: false } : undefined);
  const tank = view?.tank ?? (def.kind === "tank" ? { clarity: 100, capacity: 3, fish: [] } : undefined);
  return (
    <svg viewBox={`${left - pad} ${top - pad} ${right - left + pad * 2} ${bottom - top + pad * 2}`} className={className} aria-hidden>
      {drawItem(def.key, { id: `thumb-${view?.id ?? def.key}`, night: false, now, sky: "#CFE6EF", plant, tank })}
    </svg>
  );
}

export function FishThumb({ species, className = "size-12" }: { species: string; className?: string }) {
  const f = fishDef(species);
  return (
    <svg viewBox="-11 -6 22 12" className={className} aria-hidden>
      <path d="M -5 0 L -9 -3.2 L -8.2 0 L -9 3.2 Z" fill={f?.fin ?? "#F6B46B"} />
      <ellipse cx={0} cy={0} rx={5} ry={2.9} fill={f?.body ?? "#F08A3C"} />
      <path d="M -1 -2.6 L 1.6 -4.4 L 2.4 -2.4 Z" fill={f?.fin ?? "#F6B46B"} />
      <circle cx={2.8} cy={-0.6} r={0.7} fill="#1C1C1F" />
    </svg>
  );
}

export function ThemeThumb({ themeKey, className = "size-16" }: { themeKey: string; className?: string }) {
  const def = themeDef(themeKey);
  const wall = def?.kind === "wallpaper" ? WALLPAPER[themeKey] : WALLPAPER.paper_plain;
  const floor = def?.kind === "floor" ? FLOORS[themeKey] : FLOORS.floor_wood;
  return (
    <svg viewBox="0 0 64 64" className={className} aria-hidden>
      <rect x={0} y={0} width={64} height={40} fill={wall.base} />
      {def?.kind === "wallpaper" &&
        Array.from({ length: 6 }, (_, i) => <rect key={i} x={4 + i * 11} y={0} width={themeKey === "paper_wood" ? 1 : 4} height={40} fill={wall.ink} opacity={themeKey === "paper_dot" || themeKey === "paper_star" ? 0 : 1} />)}
      {(themeKey === "paper_dot" || themeKey === "paper_star") &&
        Array.from({ length: 12 }, (_, i) => <circle key={i} cx={6 + (i % 4) * 16 + (Math.floor(i / 4) % 2) * 8} cy={7 + Math.floor(i / 4) * 12} r={themeKey === "paper_star" ? 1 : 2} fill={wall.ink} />)}
      <rect x={0} y={40} width={64} height={24} fill={floor.base} />
      {def?.kind === "floor" && [46, 52, 58].map((y) => <rect key={y} x={0} y={y} width={64} height={1.2} fill={floor.ink} />)}
    </svg>
  );
}
