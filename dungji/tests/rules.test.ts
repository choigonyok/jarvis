import { describe, expect, it } from "vitest";
import { GRID, itemDef, plantDef, STARTER } from "../shared/catalog";
import {
  cleanliness,
  DIRT_EVERY,
  DIRT_MAX,
  dirtSlot,
  expandPrice,
  findSpot,
  fishView,
  fitsFloor,
  fitsWall,
  harvestPlant,
  hiddenItems,
  occluded,
  HOUR,
  newPlant,
  plantView,
  spawnDirt,
  tickFish,
  tickPlant,
  tickTank,
  type Placed,
} from "../shared/rules";

const T0 = Date.UTC(2026, 10, 20, 0, 0, 0);
const tulip = plantDef("plant_tulip")!;

describe("화분", () => {
  it("물은 waterHours 만에 다 마르고, 물이 있는 동안만 자란다", () => {
    const s = newPlant(T0, tulip);
    const half = tickPlant(tulip, s, T0 + 18 * HOUR, false);
    expect(half.water).toBe(50);
    expect(half.growth).toBe(18);
    const dry = tickPlant(tulip, s, T0 + 60 * HOUR, false);
    expect(dry.water).toBe(0);
    expect(dry.growth).toBe(36); // 36시간 동안만 젖어 있었다
    expect(dry.dryAt).toBe(T0 + 36 * HOUR);
  });

  it("방이 지저분하면 절반 속도로 자란다", () => {
    const s = newPlant(T0, tulip);
    expect(tickPlant(tulip, s, T0 + 10 * HOUR, true).growth).toBe(5);
  });

  it("마른 지 하루가 지나면 시든다", () => {
    const s = newPlant(T0, tulip);
    const at = T0 + (36 + 25) * HOUR;
    const v = plantView(tulip, tickPlant(tulip, s, at, false), at);
    expect(v.wilted).toBe(true);
    expect(v.thirsty).toBe(true);
  });

  it("여러 번에 나눠 흘려도 한 번에 흘린 것과 같다", () => {
    let s = newPlant(T0, tulip);
    for (let h = 1; h <= 50; h++) s = tickPlant(tulip, s, T0 + h * HOUR, false);
    const once = tickPlant(tulip, newPlant(T0, tulip), T0 + 50 * HOUR, false);
    expect(s.water).toBeCloseTo(once.water, 1);
    expect(s.growth).toBeCloseTo(once.growth, 1);
  });

  it("다 자라면 거둘 수 있고, 거두면 한 단계 전으로 돌아간다", () => {
    const grown = { ...newPlant(T0, tulip), growth: 72 };
    expect(plantView(tulip, grown, T0).ready).toBe(true);
    const after = harvestPlant(tulip, grown);
    expect(plantView(tulip, after, T0).stage).toBe(3);
  });
});

describe("어항", () => {
  it("배부르고 물이 맑은 동안만 자란다", () => {
    const tank = { clarity: 100, t: T0 };
    const fish = { hunger: 100, growth: 0, hungryAt: null, t: T0 };
    const later = tickFish(fish, tank, T0 + 30 * HOUR);
    expect(later.hunger).toBe(0);
    expect(later.growth).toBe(24); // 24시간 뒤 배고파 멈춤
    expect(later.hungryAt).toBe(T0 + 24 * HOUR);
  });

  it("오래 굶거나 물이 아주 탁하면 아프다", () => {
    const tank = tickTank({ clarity: 100, t: T0 }, T0 + 40 * HOUR);
    const fish = tickFish({ hunger: 100, growth: 0, hungryAt: null, t: T0 }, { clarity: 100, t: T0 }, T0 + 40 * HOUR);
    expect(fishView(fish, tank, T0 + 40 * HOUR).sick).toBe(true);
    const muddy = tickTank({ clarity: 100, t: T0 }, T0 + 95 * HOUR);
    expect(fishView({ hunger: 100, growth: 0, hungryAt: null, t: T0 }, muddy, T0).sick).toBe(true);
  });
});

const starterRoom = (): Placed[] =>
  STARTER.map((s, i) => ({ id: `s${i}`, key: s.key, x: s.x, y: s.y, flip: Boolean(s.flip), wall: s.wall ?? null }));

describe("먼지", () => {
  it("세 시간마다 하나, 열두 개까지", () => {
    const last = dirtSlot(T0);
    expect(spawnDirt("h1", last, T0 + DIRT_EVERY - 1, [], [])).toHaveLength(0);
    expect(spawnDirt("h1", last, T0 + 3 * DIRT_EVERY, [], [])).toHaveLength(3);
    expect(spawnDirt("h1", last, T0 + 100 * DIRT_EVERY, [], [])).toHaveLength(DIRT_MAX);
    const existing = Array.from({ length: 10 }, (_, i) => ({ x: i % GRID, y: Math.floor(i / GRID) }));
    expect(spawnDirt("h1", last, T0 + 100 * DIRT_EVERY, existing, [])).toHaveLength(2);
  });

  it("같은 둥지·같은 때면 같은 자리(동시에 계산해도 하나)", () => {
    const a = spawnDirt("h1", dirtSlot(T0), T0 + 5 * DIRT_EVERY, [], starterRoom());
    const b = spawnDirt("h1", dirtSlot(T0), T0 + 5 * DIRT_EVERY, [], starterRoom());
    expect(a).toEqual(b);
    expect(new Set(a.map((s) => s.id)).size).toBe(a.length);
  });

  it("가구 아래나 이미 먼지가 있는 칸에는 생기지 않는다", () => {
    const room = starterRoom();
    const spots = spawnDirt("h2", dirtSlot(T0), T0 + 12 * DIRT_EVERY, [], room);
    const bed = { x: 0, y: 1, w: 2, d: 3 };
    for (const s of spots) {
      const underBed = s.x >= bed.x && s.x < bed.x + bed.w && s.y >= bed.y && s.y < bed.y + bed.d;
      expect(underBed).toBe(false);
    }
    expect(new Set(spots.map((s) => `${s.x},${s.y}`)).size).toBe(spots.length);
  });

  it("키 큰 가구 바로 뒤 칸(그림에 가려지는 칸)에는 생기지 않는다", () => {
    const room = starterRoom();
    expect(occluded(room, 0, 0)).toBe(true); // 침대 머리 뒤
    expect(occluded(room, 6, 6)).toBe(false); // 맨 앞 칸
    for (const seed of ["a", "b", "c", "d"]) {
      const spots = spawnDirt(seed, dirtSlot(T0), T0 + 12 * DIRT_EVERY, [], room);
      for (const s of spots) expect(occluded(room, s.x, s.y), `${seed} ${s.x},${s.y}`).toBe(false);
    }
  });

  it("청결도", () => {
    expect(cleanliness(0)).toBe(100);
    expect(cleanliness(6)).toBe(50);
    expect(cleanliness(DIRT_MAX)).toBe(0);
  });
});

describe("배치", () => {
  it("처음 둥지의 물건들은 서로 겹치지 않는다", () => {
    const room = starterRoom();
    for (const p of room) {
      const def = itemDef(p.key)!;
      const ok = p.wall ? fitsWall(room, def, p.wall, p.x, p.id) : fitsFloor(room, def, p.x, p.y, p.flip, p.id);
      expect(ok, p.key).toBe(true);
    }
  });

  it("러그 위에는 가구를 놓을 수 있지만 가구끼리는 겹치지 않는다", () => {
    const room = starterRoom();
    expect(fitsFloor(room, itemDef("table_low")!, 3, 3, false)).toBe(true); // 러그 위
    expect(fitsFloor(room, itemDef("table_low")!, 0, 2, false)).toBe(false); // 침대 자리
    expect(fitsFloor(room, itemDef("bed_wood")!, 6, 0, false)).toBe(false); // 방 밖으로 나감
  });

  it("뒤집으면 가로세로가 바뀐다", () => {
    const sofa = itemDef("sofa_two")!; // 2 × 1
    expect(fitsFloor([], sofa, 6, 0, false)).toBe(false);
    expect(fitsFloor([], sofa, 6, 0, true)).toBe(true);
  });

  it("벽 장식은 같은 벽에서 겹치지 않는다", () => {
    const room = starterRoom(); // 오른쪽 벽 3~4 에 창문
    const clock = itemDef("wall_clock")!;
    expect(fitsWall(room, clock, "right", 4)).toBe(false);
    expect(fitsWall(room, clock, "right", 5)).toBe(true);
    expect(fitsWall(room, clock, "left", 4, "s5")).toBe(true);
  });

  it("자동으로 놓는 물건은 벽 쪽부터, 키 큰 것은 벽에 붙고, 서로 가리지 않는다", () => {
    const room = starterRoom();
    const keys = ["sofa_two", "shelf_book", "tank_small", "plant_sunflower", "desk", "wardrobe", "table_low", "cushion", "cat_tower", "easel"];
    for (const [i, key] of keys.entries()) {
      const def = itemDef(key)!;
      const spot = findSpot(room, def);
      expect(spot, key).not.toBeNull();
      // 벽 자리가 남아 있는 동안의 키 큰 것(책장·옷장)은 벽에 붙는다
      if (key === "shelf_book" || key === "wardrobe") expect(spot!.x === 0 || spot!.y === 0, `${key} 는 벽에`).toBe(true);
      room.push({ id: `n${i}`, key, ...spot! });
    }
    const hidden = hiddenItems(room);
    // 돌봐야 하는 화분·어항은 하나도 가려지지 않는다
    for (const p of room) if (/^(plant|tank)_/.test(p.key)) expect(hidden.has(p.id), p.key).toBe(false);
    expect(hidden.size).toBeLessThanOrEqual(1);
  });

  it("위에 선 물건들에 반 넘게 덮인 러그도 가려진 것으로 친다(누를 곳이 없어 표시로 고른다)", () => {
    const rug: Placed = { id: "rug", key: "rug_round", x: 3, y: 3, flip: false, wall: null };
    expect(hiddenItems([rug]).has("rug")).toBe(false);
    const covered = [rug, { id: "t", key: "tank_small", x: 3, y: 4, flip: false, wall: null }, { id: "p", key: "plant_sunflower", x: 4, y: 3, flip: false, wall: null }] as Placed[];
    expect(hiddenItems(covered).has("rug")).toBe(true);
    // 앞쪽 한 칸만 덮이면 손가락이 닿는다
    const front = [rug, { id: "c", key: "cushion", x: 4, y: 4, flip: false, wall: null }] as Placed[];
    expect(hiddenItems(front).has("rug")).toBe(false);
  });

  it("빈 자리를 찾는다", () => {
    const spot = findSpot(starterRoom(), itemDef("desk")!);
    expect(spot).not.toBeNull();
    expect(fitsFloor(starterRoom(), itemDef("desk")!, spot!.x, spot!.y, spot!.flip)).toBe(true);
  });
});

describe("값", () => {
  it("창고 넓히기는 칸마다 비싸진다", () => {
    expect(expandPrice(4)).toBe(80);
    expect(expandPrice(5)).toBe(160);
  });
});
