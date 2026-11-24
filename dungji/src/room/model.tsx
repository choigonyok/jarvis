// 물건을 네 방향으로 돌려 그린다.
//
// 그림은 돌리지 않은 방향의 모형 좌표에 도형을 쌓아 만든다(Art). x 는 오른쪽 아래, y 는 왼쪽 아래로 늘어나고,
// z 는 위로. 물건의 등은 y = 0(오른쪽 벽) 쪽, 앞은 +y 쪽이라 왼쪽 아래를 본다. 그릴 때 바닥 좌표를 돌린 방향으로
// 옮기고(View), 도형들을 뒤에서 앞으로 다시 줄 세운다(가리는 순서). 상자와 원기둥은 입체라서 돌리면 뒷모습이
// 나오고, 면 위에 그린 무늬(face)는 그 면이 화면 쪽을 볼 때만 그린다.
//
// 돌린 정도(turn)는 위에서 보아 시계 반대 방향으로 90도씩: 0 은 왼쪽 아래를, 1 은 오른쪽 아래를, 2 는 오른쪽
// 위를(뒷모습), 3 은 왼쪽 위를 본다. 홀수면 가로와 세로가 바뀐다(shared/rules.ts 의 footprint).

import { Fragment, type ReactNode } from "react";
import { Box, Cyl, FLOOR_FRAME, proj } from "./iso";

export type Turn = 0 | 1 | 2 | 3;
export type P3 = [number, number, number];
/** 가리는 순서를 정하는 상자 [x0, y0, z0, x1, y1, z1] */
export type Bounds = [number, number, number, number, number, number];

export const asTurn = (n: unknown): Turn => (((Math.round(Number(n)) % 4) + 4) % 4 || 0) as Turn;

/** 모형 바닥 좌표 → 돌린 바닥 좌표(돌린 물건의 넓이 안) */
export function turnPoint(turn: Turn, w: number, d: number, x: number, y: number): [number, number] {
  switch (turn) {
    case 1:
      return [y, w - x];
    case 2:
      return [w - x, d - y];
    case 3:
      return [d - y, x];
    default:
      return [x, y];
  }
}

/** 모형의 방향을 돌린 방향으로 */
function turnDir(turn: Turn, dx: number, dy: number): [number, number] {
  switch (turn) {
    case 1:
      return [dy, -dx];
    case 2:
      return [-dx, -dy];
    case 3:
      return [-dy, dx];
    default:
      return [dx, dy];
  }
}

const matrix = (o: [number, number], a: [number, number], c: [number, number]) =>
  `matrix(${a[0] - o[0]},${a[1] - o[1]},${c[0] - o[0]},${c[1] - o[1]},${o[0]},${o[1]})`;

/** 한 방향으로 돌린 물건을 그릴 때의 좌표 */
export class View {
  constructor(
    readonly turn: Turn,
    /** 돌리지 않은 물건의 넓이 */
    readonly w: number,
    readonly d: number,
  ) {}

  /** 모형 바닥 좌표 → 돌린 바닥 좌표 */
  at = (x: number, y: number) => turnPoint(this.turn, this.w, this.d, x, y);

  /** 모형 좌표 → 화면 */
  p = (x: number, y: number, z = 0): [number, number] => {
    const [X, Y] = this.at(x, y);
    return proj(X, Y, z);
  };

  pts = (list: P3[]) => list.map(([x, y, z]) => this.p(x, y, z).join(",")).join(" ");

  /** 모형의 상자 → 돌린 상자 */
  bounds(b: Bounds): Bounds {
    const [ax, ay] = this.at(b[0], b[1]);
    const [bx, by] = this.at(b[3], b[4]);
    return [Math.min(ax, bx), Math.min(ay, by), b[2], Math.max(ax, bx), Math.max(ay, by), b[5]];
  }

  /** (dx, dy) 쪽을 보는 면이 화면에서 보이나: 돌린 뒤 +x 나 +y 를 보면 보인다. */
  sees(dx: number, dy: number) {
    const [X, Y] = turnDir(this.turn, dx, dy);
    return X > 0 || Y > 0;
  }

  /** 면 위의 좌표 (u, v) → 화면. y 면: (u, v) = (x, z), x 면: (u, v) = (y, z). v 는 위로. */
  faceT(axis: "x" | "y", at: number) {
    const q = (u: number, v: number) => (axis === "y" ? this.p(u, at, v) : this.p(at, u, v));
    return matrix(q(0, 0), q(1, 0), q(0, 1));
  }

  /** 모형 바닥 좌표에 그린 것을 돌린 자리로(FLOOR_FRAME 안에 둔다) */
  floorT() {
    return matrix(this.at(0, 0), this.at(1, 0), this.at(0, 1));
  }
}

interface Prim {
  b: Bounds;
  draw: (v: View) => ReactNode;
}

export interface BoxOpts {
  x?: number;
  y?: number;
  z?: number;
  w: number;
  d: number;
  h: number;
  c: string;
  top?: string;
  edge?: boolean;
}

export interface CylOpts {
  cx: number;
  cy: number;
  z?: number;
  r: number;
  h: number;
  c: string;
  top?: string;
  /** 위쪽 반지름을 이만큼 줄이거나(+) 늘린다(-) */
  taper?: number;
}

/** 물건 하나의 도형들. 넣은 차례는 서로 파고든 도형끼리의 순서로만 쓴다(나머지는 자리로 정한다). */
export class Art {
  readonly prims: Prim[] = [];

  constructor(
    /** 돌리지 않은 물건의 넓이 */
    readonly w: number,
    readonly d: number,
  ) {}

  /** 아무 그림. b 는 그 그림이 차지하는 모형 상자(가리는 순서에 쓴다). */
  add(b: Bounds, draw: (v: View) => ReactNode) {
    this.prims.push({ b, draw });
    return this;
  }

  /** 상자: 윗면은 밝게, 왼쪽 아래 면은 그대로, 오른쪽 아래 면은 어둡게(돌려도 빛은 늘 같은 쪽). */
  box(o: BoxOpts) {
    const { x = 0, y = 0, z = 0, w, d, h } = o;
    const b: Bounds = [x, y, z, x + w, y + d, z + h];
    return this.add(b, (v) => {
      const [X0, Y0, , X1, Y1] = v.bounds(b);
      return <Box x={X0} y={Y0} z={z} w={X1 - X0} d={Y1 - Y0} h={h} c={o.c} top={o.top} edge={o.edge} />;
    });
  }

  /** 선 원기둥. (cx, cy) 는 바닥 좌표의 가운데, r 은 칸 단위 반지름. */
  cyl(o: CylOpts) {
    const { cx, cy, z = 0, r, h } = o;
    const R = Math.max(r, r - (o.taper ?? 0));
    return this.add([cx - R, cy - R, z, cx + R, cy + R, z + h], (v) => {
      const [X, Y] = v.at(cx, cy);
      return <Cyl cx={X} cy={Y} z={z} r={r} h={h} c={o.c} top={o.top} taper={o.taper} />;
    });
  }

  /** 바닥의 납작한 사각형 */
  flat(o: { x: number; y: number; w: number; d: number; z?: number; fill: string; stroke?: string; opacity?: number }) {
    const z = o.z ?? 0;
    const b: Bounds = [o.x, o.y, z, o.x + o.w, o.y + o.d, z];
    return this.add(b, (v) => {
      const [X0, Y0, , X1, Y1] = v.bounds(b);
      return (
        <polygon
          points={[proj(X0, Y0, z), proj(X1, Y0, z), proj(X1, Y1, z), proj(X0, Y1, z)].map((p) => p.join(",")).join(" ")}
          fill={o.fill}
          stroke={o.stroke}
          strokeWidth={o.stroke ? 1.4 : 0}
          opacity={o.opacity}
        />
      );
    });
  }

  /** 물건 아래의 옅은 그림자: 모형 바닥의 (x, y, w, d)(기본은 넓이 전체). 빛이 늘 같은 쪽이라 돌려도 같은 쪽으로 진다. */
  shadow(o: { x?: number; y?: number; w?: number; d?: number; inset?: number } = {}) {
    const { x = 0, y = 0, w = this.w, d = this.d, inset = 0.06 } = o;
    const b: Bounds = [x, y, 0, x + w, y + d, 0];
    return this.add(b, (v) => {
      const [X0, Y0, , X1, Y1] = v.bounds(b);
      const [a, c] = [X0 + inset, Y0 + inset];
      return (
        <polygon
          points={[proj(a, c), proj(X1, c), proj(X1, Y1), proj(a, Y1)].map((p) => p.join(",")).join(" ")}
          fill="rgba(40,30,20,0.16)"
        />
      );
    });
  }

  /**
   * 상자 면 위의 무늬. y 면(y = at)에는 (x, z) 로, x 면(x = at)에는 (y, z) 로 그린다(z 는 위로).
   * out 은 그 면이 보는 쪽(1 이면 +y·+x 쪽, -1 이면 반대쪽): 돌려서 그 면이 등을 돌리면 그리지 않는다.
   * u·z 는 무늬가 차지하는 범위(가리는 순서에 쓴다).
   */
  face(axis: "x" | "y", at: number, span: { u: [number, number]; z: [number, number]; out?: 1 | -1 }, node: ReactNode) {
    const out = span.out ?? 1;
    const s = at + out * 0.002;
    const [u0, u1] = span.u;
    const [z0, z1] = span.z;
    const b: Bounds = axis === "y" ? [u0, s, z0, u1, s, z1] : [s, u0, z0, s, u1, z1];
    return this.add(b, (v) =>
      v.sees(axis === "x" ? out : 0, axis === "y" ? out : 0) ? <g transform={v.faceT(axis, at)}>{node}</g> : null,
    );
  }

  /** 한 점에 붙은 작은 그림(화면 좌표로 그린다): 과일, 손잡이, 잎. r·h 는 가리는 순서에 쓰는 너비·높이(칸). */
  dot(x: number, y: number, z: number, draw: (sx: number, sy: number) => ReactNode, r = 0.05, h = r) {
    return this.add([x - r, y - r, z, x + r, y + r, z + h], (v) => {
      const [sx, sy] = v.p(x, y, z);
      return draw(sx, sy);
    });
  }

  /** 두 점 사이의 막대(다리, 줄, 손잡이) */
  line(a: P3, b: P3, stroke: string, width = 2) {
    const bb: Bounds = [Math.min(a[0], b[0]), Math.min(a[1], b[1]), Math.min(a[2], b[2]), Math.max(a[0], b[0]), Math.max(a[1], b[1]), Math.max(a[2], b[2])];
    return this.add(bb, (v) => {
      const [x1, y1] = v.p(...a);
      const [x2, y2] = v.p(...b);
      return <line x1={x1} y1={y1} x2={x2} y2={y2} stroke={stroke} strokeWidth={width} strokeLinecap="round" />;
    });
  }

  /** 바닥에 깔린 그림(러그, 매트): 안에서는 모형 바닥 좌표 (x, y) 로 그린다. */
  floor(node: ReactNode) {
    return this.add([0, 0, 0, this.w, this.d, 0], (v) => (
      <g transform={FLOOR_FRAME}>
        <g transform={v.floorT()}>{node}</g>
      </g>
    ));
  }
}

const EPS = 1e-6;

/** a 를 b 보다 먼저(뒤에) 그리나: -1 먼저, 1 나중, 0 은 서로 파고들어 모른다. 시점은 +x, +y, 위쪽에 있다. */
function order(a: Bounds, b: Bounds): -1 | 0 | 1 {
  for (const k of [0, 1, 2]) {
    if (a[k + 3] <= b[k] + EPS) return -1;
    if (b[k + 3] <= a[k] + EPS) return 1;
  }
  return 0;
}

function screenRect(b: Bounds): [number, number, number, number] {
  let x0 = Infinity;
  let y0 = Infinity;
  let x1 = -Infinity;
  let y1 = -Infinity;
  for (const X of [b[0], b[3]])
    for (const Y of [b[1], b[4]])
      for (const Z of [b[2], b[5]]) {
        const [sx, sy] = proj(X, Y, Z);
        x0 = Math.min(x0, sx);
        y0 = Math.min(y0, sy);
        x1 = Math.max(x1, sx);
        y1 = Math.max(y1, sy);
      }
  return [x0, y0, x1, y1];
}

/**
 * 돌린 상자들을 뒤에서 앞으로 그리는 순서. 화면에서 겹치는 둘만 견주고(둘을 가르는 축이 있으면 시점에서 먼 쪽이
 * 먼저, 서로 파고들었으면 넣은 차례), 그 관계를 지키며 넣은 차례가 빠른 것부터 꺼낸다.
 */
export function depthOrder(list: Bounds[]): number[] {
  const n = list.length;
  const rects = list.map(screenRect);
  const next: number[][] = list.map(() => []);
  const waits = new Array<number>(n).fill(0);
  for (let i = 0; i < n; i++)
    for (let j = i + 1; j < n; j++) {
      const [a, b] = [rects[i], rects[j]];
      if (a[2] <= b[0] + 0.25 || b[2] <= a[0] + 0.25 || a[3] <= b[1] + 0.25 || b[3] <= a[1] + 0.25) continue;
      const [s, t] = order(list[i], list[j]) === 1 ? [j, i] : [i, j];
      next[s].push(t);
      waits[t]++;
    }
  const out: number[] = [];
  const done = new Array<boolean>(n).fill(false);
  while (out.length < n) {
    let pick = -1;
    for (let i = 0; i < n && pick < 0; i++) if (!done[i] && waits[i] === 0) pick = i;
    // 고리(관계가 서로 엇갈린 경우)면 넣은 차례가 빠른 것부터
    for (let i = 0; i < n && pick < 0; i++) if (!done[i]) pick = i;
    done[pick] = true;
    out.push(pick);
    for (const t of next[pick]) waits[t]--;
  }
  return out;
}

/** 도형들을 그 방향으로 돌려 가리는 순서대로 그린다. */
export function paint(art: Art, turn: Turn): ReactNode {
  const v = new View(turn, art.w, art.d);
  const seq = depthOrder(art.prims.map((p) => v.bounds(p.b)));
  return seq.map((i) => <Fragment key={i}>{art.prims[i].draw(v)}</Fragment>);
}
