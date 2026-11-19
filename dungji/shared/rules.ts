// 둥지의 규칙: 먼지가 쌓이는 속도, 화분과 물고기가 자라는 방식, 깃털을 주고받는 값.
// 모두 순수 함수라 서버와 시험이 같은 계산을 쓴다.

import { GRID, ITEMS, itemDef, type ItemDef, type PlantDef } from "./catalog";
import { inside, proj } from "./iso";

export const HOUR = 3_600_000;

/** 세 시간마다 먼지 하나가 생긴다. 열두 개가 넘으면 더 쌓이지 않는다. */
export const DIRT_EVERY = 3 * HOUR;
export const DIRT_MAX = 12;
/** 이만큼 쌓이면 지저분한 방: 화분이 절반 속도로 자란다. */
export const MESSY_AT = 8;

export const START_COINS = 300;
export const STORAGE_BASE = 4;
export const STORAGE_MAX = 12;

export const REWARD = {
  attend: 20, // 그날 처음 들어왔을 때(한 사람마다)
  diary: 15, // 그날 처음 일기를 썼을 때(한 사람마다)
  clean: 3, // 우리 방 먼지 하나
  helpClean: 2, // 이웃 방 먼지 하나
  guestbook: 5, // 이웃 방명록 한 줄
  water: 2,
  feed: 1,
  scrub: 3,
} as const;

export const LIMIT = {
  helpClean: 3, // 한 이웃 방에서 하루에 치울 수 있는 먼지
  guestbook: 3, // 깃털을 받는 방명록, 하루에
} as const;

/** 창고를 한 칸 넓히는 값: 다섯째 칸 80, 여섯째 칸 160 … */
export const expandPrice = (slots: number) => 80 * (slots - 3);
export const sellPrice = (price: number) => Math.floor(price / 2);
export const cleanliness = (spots: number) => Math.max(0, Math.round(100 * (1 - spots / DIRT_MAX)));

/** 한국 시각으로 그날(YYYY-MM-DD) */
export const dayKey = (ms: number) => new Date(ms + 9 * HOUR).toISOString().slice(0, 10);

// ── 화분 ─────────────────────────────────────────────────────────

export interface PlantState {
  /** 0~100 */
  water: number;
  /** 물이 있는 동안 자란 시간(시간 단위) */
  growth: number;
  /** 물이 다 마른 때. 물이 있으면 null */
  dryAt: number | null;
  /** 이 값을 계산한 때 */
  t: number;
}

export const newPlant = (now: number, def: PlantDef, stage = 0): PlantState => ({
  water: 100,
  growth: def.stages[stage] ?? 0,
  dryAt: null,
  t: now,
});

/** now 까지 시간을 흘려 보낸 화분. 물이 있는 동안만 자라고, 방이 지저분하면 절반 속도다. */
export function tickPlant(def: PlantDef, s: PlantState, now: number, messy: boolean): PlantState {
  const hours = Math.max(0, (now - s.t) / HOUR);
  if (hours === 0) return s;
  const rate = 100 / def.waterHours; // 시간당 마르는 양
  const wetHours = Math.min(hours, s.water / rate);
  const water = Math.max(0, s.water - hours * rate);
  const grown = wetHours * (messy ? 0.5 : 1);
  const top = def.stages[def.stages.length - 1];
  return {
    water: round2(water),
    growth: round2(Math.min(top, s.growth + grown)),
    dryAt: water > 0 ? null : (s.dryAt ?? s.t + wetHours * HOUR),
    t: now,
  };
}

export interface PlantView {
  water: number;
  stage: number;
  maxStage: number;
  ready: boolean;
  wilted: boolean;
  /** 다음 단계까지 남은 자람 시간(물이 있을 때) */
  nextHours: number | null;
  /** 물을 줄 수 있나(70 아래) */
  thirsty: boolean;
}

export function plantView(def: PlantDef, s: PlantState, now: number): PlantView {
  const stage = stageOf(def, s.growth);
  const maxStage = def.stages.length - 1;
  return {
    water: Math.round(s.water),
    stage,
    maxStage,
    ready: stage === maxStage,
    wilted: s.water <= 0 && s.dryAt !== null && now - s.dryAt >= 24 * HOUR,
    nextHours: stage === maxStage ? null : round2(def.stages[stage + 1] - s.growth),
    thirsty: s.water < 70,
  };
}

export function stageOf(def: PlantDef, growth: number) {
  let stage = 0;
  def.stages.forEach((need, i) => {
    if (growth >= need) stage = i;
  });
  return stage;
}

/** 다 자란 화분을 거두면 바로 전 단계로 돌아가 다시 핀다. */
export function harvestPlant(def: PlantDef, s: PlantState): PlantState {
  return { ...s, growth: def.stages[def.stages.length - 2] };
}

// ── 어항 ─────────────────────────────────────────────────────────

/** 물이 탁해지는 데 걸리는 시간, 배가 고파지는 데 걸리는 시간 */
export const TANK_DIRTY_HOURS = 96;
export const FISH_HUNGRY_HOURS = 24;
/** 이만큼 자라면 어른 물고기 */
export const FISH_ADULT = 72;

export interface TankState {
  /** 0~100, 물의 맑기 */
  clarity: number;
  t: number;
}

export interface FishState {
  /** 0~100, 배부름 */
  hunger: number;
  growth: number;
  /** 배가 완전히 고파진 때 */
  hungryAt: number | null;
  t: number;
}

const CLARITY_RATE = 100 / TANK_DIRTY_HOURS;
const HUNGER_RATE = 100 / FISH_HUNGRY_HOURS;
/** 물이 이 아래로 탁하면 물고기가 자라지 않는다. */
const CLEAR_ENOUGH = 20;

export function tickTank(s: TankState, now: number): TankState {
  const hours = Math.max(0, (now - s.t) / HOUR);
  return { clarity: round2(Math.max(0, s.clarity - hours * CLARITY_RATE)), t: now };
}

/** 물고기는 배가 고프지 않고 물이 맑은 동안 자란다. tank 는 tickTank 하기 전(같은 t)의 어항. */
export function tickFish(f: FishState, tankBefore: TankState, now: number): FishState {
  const hours = Math.max(0, (now - f.t) / HOUR);
  if (hours === 0) return f;
  // 어항의 시각이 물고기와 다를 수 있다: 물고기의 시각에서 어항의 맑기를 다시 잰다.
  const clarityAtStart = Math.max(0, tankBefore.clarity - Math.max(0, (f.t - tankBefore.t) / HOUR) * CLARITY_RATE);
  const fedHours = Math.min(hours, f.hunger / HUNGER_RATE);
  const clearHours = Math.max(0, (clarityAtStart - CLEAR_ENOUGH) / CLARITY_RATE);
  const hunger = Math.max(0, f.hunger - hours * HUNGER_RATE);
  return {
    hunger: round2(hunger),
    growth: round2(Math.min(FISH_ADULT, f.growth + Math.min(fedHours, clearHours))),
    hungryAt: hunger > 0 ? null : (f.hungryAt ?? f.t + fedHours * HOUR),
    t: now,
  };
}

export interface FishView {
  hunger: number;
  /** 0~1, 어른이 되기까지 */
  grown: number;
  adult: boolean;
  sick: boolean;
}

export function fishView(f: FishState, tank: TankState, now: number): FishView {
  const starving = f.hunger <= 0 && f.hungryAt !== null && now - f.hungryAt >= 12 * HOUR;
  return {
    hunger: Math.round(f.hunger),
    grown: round2(f.growth / FISH_ADULT),
    adult: f.growth >= FISH_ADULT,
    sick: starving || tank.clarity < 10,
  };
}

// ── 먼지 ─────────────────────────────────────────────────────────

export const DIRT_KINDS = ["dust", "crumbs", "stain", "tissue", "sock", "leaf"] as const;
export type DirtKind = (typeof DIRT_KINDS)[number];
const DIRT_WEIGHTS: [DirtKind, number][] = [
  ["dust", 35],
  ["crumbs", 20],
  ["stain", 15],
  ["tissue", 15],
  ["sock", 10],
  ["leaf", 5],
];

export const dirtSlot = (ms: number) => Math.floor(ms / DIRT_EVERY);

/** 문자열에서 만든 0~1 의 수. 같은 둥지·같은 시각이면 늘 같은 자리에 먼지가 생긴다(동시에 두 번 계산해도 하나). */
export function seeded(seed: string): () => number {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return () => {
    h += 0x6d2b79f5;
    let t = h;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface Spot {
  id: string;
  x: number;
  y: number;
  kind: DirtKind;
}

/**
 * lastSlot 다음부터 now 의 칸까지 생길 먼지. 이미 있는 먼지와 합쳐 DIRT_MAX 를 넘지 않는다.
 * 가구가 덮은 칸과 이미 먼지가 있는 칸은 피한다(빈 칸이 없으면 아무 칸).
 */
export function spawnDirt(
  homeId: string,
  lastSlot: number,
  now: number,
  existing: { x: number; y: number }[],
  room: Placed[],
): Spot[] {
  const current = dirtSlot(now);
  const capacity = Math.max(0, DIRT_MAX - existing.length);
  if (current <= lastSlot || capacity === 0) return [];
  const covered = coveredTiles(room);
  const taken = new Set(existing.map((s) => `${s.x},${s.y}`));
  // 키 큰 가구 바로 뒤 칸은 그림에 가려 누를 수 없다: 피한다.
  for (let x = 0; x < GRID; x++) for (let y = 0; y < GRID; y++) if (occluded(room, x, y)) covered.add(`${x},${y}`);
  const out: Spot[] = [];
  // 오래 비운 방도 열두 개까지만: 가장 최근 칸들만 본다.
  const first = Math.max(lastSlot + 1, current - capacity + 1);
  for (let slot = first; slot <= current; slot++) {
    const rnd = seeded(`${homeId}:${slot}`);
    const free: [number, number][] = [];
    for (let x = 0; x < GRID; x++)
      for (let y = 0; y < GRID; y++) if (!covered.has(`${x},${y}`) && !taken.has(`${x},${y}`)) free.push([x, y]);
    const pool: [number, number][] = free.length ? free : [[Math.floor(rnd() * GRID), Math.floor(rnd() * GRID)]];
    const [x, y] = pool[Math.floor(rnd() * pool.length)];
    let pick = rnd() * 100;
    let kind: DirtKind = "dust";
    for (const [k, w] of DIRT_WEIGHTS) {
      if (pick < w) {
        kind = k;
        break;
      }
      pick -= w;
    }
    taken.add(`${x},${y}`);
    out.push({ id: `${homeId}-${slot}`, x, y, kind });
  }
  return out;
}

// ── 방 배치 ──────────────────────────────────────────────────────

export interface Placed {
  id: string;
  key: string;
  x: number;
  y: number;
  flip: boolean;
  wall: "left" | "right" | null;
}

/** 뒤집으면 x·y 가 바뀐다(화면에서 좌우 대칭). */
export function footprint(def: ItemDef, flip: boolean) {
  return flip ? { w: def.d, d: def.w } : { w: def.w, d: def.d };
}

function coveredTiles(room: Placed[], opts: { under?: boolean } = {}) {
  const set = new Set<string>();
  for (const p of room) {
    const def = itemDef(p.key);
    if (!def || def.kind === "wall") continue;
    if (Boolean(def.under) !== Boolean(opts.under)) continue;
    const { w, d } = footprint(def, p.flip);
    for (let i = 0; i < w; i++) for (let j = 0; j < d; j++) set.add(`${p.x + i},${p.y + j}`);
  }
  return set;
}

/** 바닥 물건을 (x, y) 에 놓을 수 있나. 러그는 러그끼리만, 가구는 가구끼리만 겹치면 안 된다. */
export function fitsFloor(room: Placed[], def: ItemDef, x: number, y: number, flip: boolean, ignoreId?: string) {
  const { w, d } = footprint(def, flip);
  if (x < 0 || y < 0 || x + w > GRID || y + d > GRID) return false;
  const others = room.filter((p) => p.id !== ignoreId);
  const covered = coveredTiles(others, { under: def.under });
  for (let i = 0; i < w; i++) for (let j = 0; j < d; j++) if (covered.has(`${x + i},${y + j}`)) return false;
  return true;
}

/** 벽 물건을 그 벽의 pos 에 놓을 수 있나. */
export function fitsWall(room: Placed[], def: ItemDef, wall: "left" | "right", pos: number, ignoreId?: string) {
  if (pos < 0 || pos + def.w > GRID) return false;
  for (const p of room) {
    if (p.id === ignoreId || p.wall !== wall) continue;
    const other = itemDef(p.key);
    if (!other) continue;
    if (pos < p.x + other.w && p.x < pos + def.w) return false;
  }
  return true;
}

/** 바닥 물건의 그림 윤곽(상자의 육각형) */
function silhouette(p: Placed) {
  const def = itemDef(p.key)!;
  const { w, d } = footprint(def, p.flip);
  const h = Math.max(def.h, 0.05);
  return {
    box: { x: p.x, y: p.y, w, d },
    poly: [
      proj(p.x, p.y, h),
      proj(p.x + w, p.y, h),
      proj(p.x + w, p.y, 0),
      proj(p.x + w, p.y + d, 0),
      proj(p.x, p.y + d, 0),
      proj(p.x, p.y + d, h),
    ] as [number, number][],
  };
}

const standing = (p: Placed) => {
  const def = itemDef(p.key);
  return Boolean(def && def.kind !== "wall" && !def.under);
};

/**
 * back 의 그림 가운데 얼마가 front 에 가려지나(0~1). front 가 앞(그리는 순서로 뒤)에 있을 때만 센다.
 * back 의 윤곽 안에 고르게 찍은 점들 가운데 front 의 윤곽 안에 드는 몫.
 */
export function coveredShare(front: Placed, back: Placed) {
  if (front.id === back.id || !standing(front) || !standing(back)) return 0;
  const f = silhouette(front);
  const b = silhouette(back);
  const behind = b.box.x + b.box.w <= f.box.x || b.box.y + b.box.d <= f.box.y;
  if (!behind) return 0;
  const xs = b.poly.map((p) => p[0]);
  const ys = b.poly.map((p) => p[1]);
  const [x0, x1, y0, y1] = [Math.min(...xs), Math.max(...xs), Math.min(...ys), Math.max(...ys)];
  let mine = 0;
  let hidden = 0;
  for (let i = 1; i <= 5; i++)
    for (let j = 1; j <= 5; j++) {
      const x = x0 + ((x1 - x0) * i) / 6;
      const y = y0 + ((y1 - y0) * j) / 6;
      if (!inside(x, y, b.poly)) continue;
      mine++;
      if (inside(x, y, f.poly)) hidden++;
    }
  return mine ? hidden / mine : 0;
}

/** front 가 back 을 반 넘게 가리나 */
export const hides = (front: Placed, back: Placed) => coveredShare(front, back) >= 0.5;

/** 앞의 물건에 반 넘게 가려진 물건들(화면이 위에 표시를 띄운다) */
export function hiddenItems(room: Placed[]) {
  const out = new Set<string>();
  for (const back of room) if (room.some((front) => hides(front, back))) out.add(back.id);
  return out;
}

/**
 * 새 물건을 둘 빈자리. 벽 쪽(뒤)부터 채우고, 키 큰 물건(옷장·책장·스탠드)은 벽에 붙이며,
 * 다른 물건을 가리거나 가려지는 자리는 피한다. 러그는 바닥 가운데로. 벽 장식은 오른쪽 벽부터.
 */
export function findSpot(room: Placed[], def: ItemDef): Omit<Placed, "id" | "key"> | null {
  if (def.kind === "wall") {
    for (const wall of ["right", "left"] as const)
      for (let pos = 0; pos + def.w <= GRID; pos++)
        if (fitsWall(room, def, wall, pos)) return { x: pos, y: 0, flip: false, wall };
    return null;
  }
  let best: Omit<Placed, "id" | "key"> | null = null;
  let bestScore = Infinity;
  for (let x = 0; x < GRID; x++)
    for (let y = 0; y < GRID; y++)
      for (const flip of [false, true]) {
        if (!fitsFloor(room, def, x, y, flip)) continue;
        const { w, d } = footprint(def, flip);
        let score: number;
        if (def.under) score = Math.abs(x + w / 2 - GRID / 2) + Math.abs(y + d / 2 - GRID / 2);
        else {
          score = x + y;
          if (def.h >= 1.4 && x !== 0 && y !== 0) score += 20;
          // 조금이라도 가리거나 가려지는 자리는 그만큼 뒤로 미룬다(반쯤 가려지면 손가락이 앞의 가구를 누른다).
          const me: Placed = { id: "_new", key: def.key, x, y, flip, wall: null };
          // 돌봐야 하는 화분·어항을 가리는 것은 훨씬 더 미룬다(가려지면 물을 줄 수 없다).
          const care = (p: Placed) => (itemDef(p.key)?.kind === "plant" || itemDef(p.key)?.kind === "tank" ? 120 : 40);
          for (const p of room) score += care(p) * coveredShare(me, p) + care(me) * coveredShare(p, me);
        }
        if (score < bestScore) {
          bestScore = score;
          best = { x, y, flip, wall: null };
        }
      }
  return best;
}

/**
 * (x, y) 칸의 먼지가 앞쪽 가구 그림에 가려지나. 칸 가운데 점이 가구 그림의 윤곽(상자의 육각형) 안에 들면 가려진 것.
 * 가구 바로 아래 칸은 여기서 세지 않는다(그 칸에는 아예 먼지가 생기지 않는다).
 */
export function occluded(room: Placed[], x: number, y: number) {
  const [cx, cy] = proj(x + 0.5, y + 0.5);
  for (const p of room) {
    const def = itemDef(p.key);
    if (!def || def.kind === "wall" || def.under || def.h <= 0) continue;
    const { w, d } = footprint(def, p.flip);
    if (x >= p.x && x < p.x + w && y >= p.y && y < p.y + d) continue;
    const poly: [number, number][] = [
      proj(p.x, p.y, def.h),
      proj(p.x + w, p.y, def.h),
      proj(p.x + w, p.y, 0),
      proj(p.x + w, p.y + d, 0),
      proj(p.x, p.y + d, 0),
      proj(p.x, p.y + d, def.h),
    ];
    if (inside(cx, cy - 3, poly)) return true;
  }
  return false;
}

export const isGrowing = (key: string) => {
  const def = itemDef(key);
  return def?.kind === "plant" || def?.kind === "tank";
};

export const ITEM_KEYS = new Set(ITEMS.map((i) => i.key));

const round2 = (n: number) => Math.round(n * 100) / 100;
