// 둥지 하나의 상태: 시간을 흘려 먼지·화분·어항을 지금 값으로 맞추고, 화면에 보낼 모양을 만든다.

import { fishDef, itemDef, plantDef, tankDef } from "../shared/catalog";
import type { FishItem, HomeView, ItemView, Member, Visitor } from "../shared/api";
import {
  cleanliness,
  DIRT_MAX,
  dirtSlot,
  fishView,
  MESSY_AT,
  plantView,
  spawnDirt,
  tickFish,
  tickPlant,
  tickTank,
  type DirtKind,
  type FishState,
  type Placed,
  type PlantState,
  type TankState,
} from "../shared/rules";

export interface CoupleRow {
  id: string;
  slug: string;
  name: string;
  intro: string;
  since: string | null;
  coins: number;
  storage_slots: number;
  wallpaper: string;
  floor: string;
  themes: string;
  invite_code: string | null;
  listed: number;
  dirt_slot: number;
  visits_total: number;
  active_at: number;
  created_at: number;
}

export interface ItemRow {
  id: string;
  couple_id: string;
  key: string;
  place: "room" | "storage";
  x: number;
  y: number;
  /** 돌린 정도(0~3). 칸 이름은 좌우 뒤집기만 있던 때의 것이라 flip 이고, 그때의 0·1 이 지금의 0·1 과 같은 방향이다. */
  flip: number;
  wall: "left" | "right" | null;
  state: string | null;
  stored_at: number | null;
  created_at: number;
}

export interface FishRow {
  id: string;
  couple_id: string;
  tank_id: string;
  species: string;
  name: string;
  hunger: number;
  growth: number;
  hungry_at: number | null;
  t: number;
  created_at: number;
}

export interface DirtRow {
  id: string;
  couple_id: string;
  x: number;
  y: number;
  kind: DirtKind;
  created_at: number;
}

export interface HomeState {
  items: ItemRow[];
  fish: FishRow[];
  dirt: DirtRow[];
}

export async function getCouple(db: D1Database, by: { id?: string; slug?: string }) {
  if (by.id) return db.prepare("SELECT * FROM couples WHERE id = ?").bind(by.id).first<CoupleRow>();
  return db.prepare("SELECT * FROM couples WHERE slug = ?").bind(by.slug ?? "").first<CoupleRow>();
}

export async function loadState(db: D1Database, coupleId: string): Promise<HomeState> {
  const [items, fish, dirt] = await db.batch([
    db.prepare("SELECT * FROM items WHERE couple_id = ? ORDER BY created_at").bind(coupleId),
    db.prepare("SELECT * FROM fish WHERE couple_id = ? ORDER BY created_at").bind(coupleId),
    db.prepare("SELECT * FROM dirt WHERE couple_id = ? ORDER BY created_at").bind(coupleId),
  ]);
  return {
    items: items.results as unknown as ItemRow[],
    fish: fish.results as unknown as FishRow[],
    dirt: dirt.results as unknown as DirtRow[],
  };
}

export const placed = (items: ItemRow[]): Placed[] =>
  items
    .filter((i) => i.place === "room")
    .map((i) => ({ id: i.id, key: i.key, x: i.x, y: i.y, turn: i.flip, wall: i.wall }));

export const parse = <T>(json: string | null): T => JSON.parse(json ?? "{}") as T;

/** 이만큼 지나지 않았으면 다시 쓰지 않는다(읽을 때마다 쓰지 않게). */
const TICK_MIN = 60_000;

/**
 * now 까지 시간을 흘린다: 새 먼지, 방에 놓인 화분의 물·자람, 어항의 맑기와 물고기.
 * 창고에 든 것은 멈춰 있다. 쓰기는 조건부라 다른 요청이 먼저 바꿨으면 그쪽을 남긴다.
 */
export async function tick(db: D1Database, couple: CoupleRow, now: number): Promise<HomeState> {
  const state = await loadState(db, couple.id);
  const stmts: D1PreparedStatement[] = [];

  const slot = dirtSlot(now);
  if (slot > couple.dirt_slot) {
    const spawned = spawnDirt(couple.id, couple.dirt_slot, now, state.dirt, placed(state.items));
    stmts.push(
      db.prepare("UPDATE couples SET dirt_slot = ? WHERE id = ? AND dirt_slot < ?").bind(slot, couple.id, slot),
    );
    for (const s of spawned) {
      stmts.push(
        db
          .prepare("INSERT OR IGNORE INTO dirt (id, couple_id, x, y, kind, created_at) VALUES (?, ?, ?, ?, ?, ?)")
          .bind(s.id, couple.id, s.x, s.y, s.kind, now),
      );
      if (!state.dirt.some((d) => d.id === s.id))
        state.dirt.push({ ...s, couple_id: couple.id, created_at: now });
    }
    couple.dirt_slot = slot;
  }

  const messy = state.dirt.length >= MESSY_AT;
  for (const item of state.items) {
    if (item.place !== "room") continue;
    const def = itemDef(item.key);
    if (def?.kind === "plant") {
      const pdef = plantDef(item.key)!;
      const before = parse<PlantState>(item.state);
      if (now - before.t < TICK_MIN) continue;
      const next = JSON.stringify(tickPlant(pdef, before, now, messy));
      stmts.push(db.prepare("UPDATE items SET state = ? WHERE id = ? AND state = ?").bind(next, item.id, item.state));
      item.state = next;
    } else if (def?.kind === "tank") {
      const before = parse<TankState>(item.state);
      if (now - before.t < TICK_MIN) continue;
      for (const f of state.fish) {
        if (f.tank_id !== item.id) continue;
        const next = tickFish(fishState(f), before, now);
        stmts.push(
          db
            .prepare("UPDATE fish SET hunger = ?, growth = ?, hungry_at = ?, t = ? WHERE id = ? AND t = ?")
            .bind(next.hunger, next.growth, next.hungryAt, next.t, f.id, f.t),
        );
        Object.assign(f, { hunger: next.hunger, growth: next.growth, hungry_at: next.hungryAt, t: next.t });
      }
      const next = JSON.stringify(tickTank(before, now));
      stmts.push(db.prepare("UPDATE items SET state = ? WHERE id = ? AND state = ?").bind(next, item.id, item.state));
      item.state = next;
    }
  }

  if (stmts.length) await db.batch(stmts);
  return state;
}

export const fishState = (f: FishRow): FishState => ({
  hunger: f.hunger,
  growth: f.growth,
  hungryAt: f.hungry_at,
  t: f.t,
});

function itemView(item: ItemRow, fish: FishRow[], now: number): ItemView {
  const def = itemDef(item.key);
  const view: ItemView = {
    id: item.id,
    key: item.key,
    place: item.place,
    x: item.x,
    y: item.y,
    turn: item.flip,
    wall: item.wall,
  };
  if (def?.kind === "plant") {
    // 창고에 든 화분은 멈춘 그대로 보인다.
    const s = parse<PlantState>(item.state);
    view.plant = plantView(plantDef(item.key)!, s, item.place === "room" ? now : s.t);
  } else if (def?.kind === "tank") {
    const tank = parse<TankState>(item.state);
    const at = item.place === "room" ? now : tank.t;
    view.tank = {
      clarity: Math.round(tank.clarity),
      capacity: tankDef(item.key)!.capacity,
      fish: fish
        .filter((f) => f.tank_id === item.id)
        .map(
          (f): FishItem => ({
            id: f.id,
            species: fishDef(f.species) ? f.species : "fish_gold",
            name: f.name,
            ...fishView(fishState(f), tank, at),
          }),
        ),
    };
  }
  return view;
}

export interface ViewExtras {
  owner: boolean;
  members: Member[];
  visits: { today: number; recent: Visitor[] };
  helpLeft?: number;
}

export function homeView(couple: CoupleRow, state: HomeState, now: number, extra: ViewExtras): HomeView {
  const items = state.items
    .filter((i) => extra.owner || i.place === "room")
    .sort((a, b) => (a.stored_at ?? 0) - (b.stored_at ?? 0))
    .map((i) => itemView(i, state.fish, now));
  const view: HomeView = {
    id: couple.id,
    slug: couple.slug,
    name: couple.name,
    intro: couple.intro,
    since: couple.since,
    wallpaper: couple.wallpaper,
    floor: couple.floor,
    members: extra.members,
    cleanliness: cleanliness(state.dirt.length),
    dirt: state.dirt.map((d) => ({ id: d.id, x: d.x, y: d.y, kind: d.kind })),
    items,
    visits: { today: extra.visits.today, total: couple.visits_total, recent: extra.visits.recent },
    owner: extra.owner,
  };
  if (extra.owner) {
    view.coins = couple.coins;
    view.storageSlots = couple.storage_slots;
    view.themes = JSON.parse(couple.themes) as string[];
    view.invite = extra.members.length < 2 ? couple.invite_code : null;
    view.listed = Boolean(couple.listed);
  } else {
    view.helpLeft = extra.helpLeft ?? 0;
  }
  return view;
}

/** 이웃 목록에서 쓰는 어림 청결도: 마지막으로 센 먼지에 그 뒤로 생겼을 먼지를 더한다. */
export function estimateClean(spots: number, lastSlot: number, now: number) {
  const more = Math.max(0, dirtSlot(now) - lastSlot);
  return cleanliness(Math.min(DIRT_MAX, spots + more));
}
