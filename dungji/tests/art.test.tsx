import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ITEMS } from "../shared/catalog";
import { buildArt, drawItem, drawWall } from "../src/room/art";
import type { DrawCtx } from "../src/room/kit";
import { depthOrder, View, type Bounds, type Turn } from "../src/room/model";

const ctx = (night: boolean): DrawCtx => ({
  id: "t",
  night,
  now: new Date(2026, 10, 23, night ? 22 : 14, 10),
  sky: "#CFE6EF",
  plant: { water: 90, stage: 4, maxStage: 4, ready: false, wilted: false, nextHours: null, thirsty: false },
  tank: { clarity: 100, fish: [{ id: "f", species: "fish_gold", name: "", hunger: 100, grown: 1, adult: true, sick: false }] },
});
const markup = (node: React.ReactNode) => renderToStaticMarkup(<svg>{node}</svg>);
const floors = ITEMS.filter((d) => d.kind !== "wall");
const walls = ITEMS.filter((d) => d.kind === "wall");

describe("물건 그림", () => {
  it("상점의 바닥 물건마다 그림이 있고, 낮·밤과 네 방향 모두 그려진다", () => {
    const missing = floors.filter((d) => !buildArt(d.key, ctx(false))).map((d) => d.key);
    expect(missing).toEqual([]);
    for (const d of floors)
      for (const night of [false, true])
        for (const turn of [0, 1, 2, 3] as Turn[]) expect(markup(drawItem(d.key, ctx(night), turn)).length, `${d.key} ${turn}`).toBeGreaterThan(40);
  });

  it("벽 장식마다 그림이 있다", () => {
    const none = markup(drawWall("__none__", 0, ctx(false)));
    const missing = walls.filter((d) => markup(drawWall(d.key, 0, ctx(false))) === none).map((d) => d.key);
    expect(missing).toEqual([]);
    for (const d of walls) expect(markup(drawWall(d.key, 1, ctx(true))).length, d.key).toBeGreaterThan(40);
  });

  it("도형은 물건의 넓이 안에 있고, 높이는 목록의 높이 언저리다", () => {
    const out: string[] = [];
    for (const d of floors) {
      const art = buildArt(d.key, ctx(false));
      if (!art) continue;
      let top = 0;
      for (const p of art.prims) {
        const [x0, y0, z0, x1, y1, z1] = p.b;
        if (x0 < -0.05 || y0 < -0.05 || x1 > d.w + 0.05 || y1 > d.d + 0.05 || z0 < -0.01) out.push(`${d.key} 넓이 밖 ${p.b.map((n) => n.toFixed(2))}`);
        top = Math.max(top, z1);
      }
      if (top > d.h + 0.75) out.push(`${d.key} 높이 ${top.toFixed(2)} > ${d.h}`);
    }
    expect(out).toEqual([]);
  });
});

describe("돌려 그리기", () => {
  // 의자의 등받이 기둥(뒤)과 앉는 판(앞)
  const post: Bounds = [0.14, 0.1, 0, 0.22, 0.18, 1.02];
  const seat: Bounds = [0.1, 0.18, 0.42, 0.9, 0.92, 0.49];
  const order = (turn: Turn) => {
    const v = new View(turn, 1, 1);
    return depthOrder([post, seat].map((b) => v.bounds(b)));
  };

  it("앞을 보면 등받이가 먼저, 뒤를 보면 앉는 판이 먼저(등받이가 앞에 온다)", () => {
    expect(order(0)).toEqual([0, 1]);
    expect(order(2)).toEqual([1, 0]);
  });

  it("90도·270도 돌리면 넓이의 가로·세로가 바뀌고 모서리는 넓이 안에 머문다", () => {
    const v1 = new View(1, 2, 1);
    expect(v1.bounds([0, 0, 0, 2, 1, 1])).toEqual([0, 0, 0, 1, 2, 1]);
    const v3 = new View(3, 2, 1);
    expect(v3.bounds([0, 0, 0, 2, 1, 1])).toEqual([0, 0, 0, 1, 2, 1]);
    expect(new View(2, 2, 1).bounds([0, 0, 0, 0.5, 0.2, 1])).toEqual([1.5, 0.8, 0, 2, 1, 1]);
  });

  it("앞면 무늬는 그 면이 화면 쪽을 볼 때만", () => {
    const front = (t: Turn) => new View(t, 1, 1).sees(0, 1);
    expect([0, 1, 2, 3].map((t) => front(t as Turn))).toEqual([true, true, false, false]);
    const side = (t: Turn) => new View(t, 1, 1).sees(1, 0);
    expect([0, 1, 2, 3].map((t) => side(t as Turn))).toEqual([true, false, false, true]);
  });
});
