// 비스듬히 내려다본 방(아이소메트릭)의 좌표와 기본 도형.
// 바닥 칸 (gx, gy) 와 높이 z 를 화면 좌표로 옮긴다. gx 는 오른쪽 아래, gy 는 왼쪽 아래로 늘어난다.

import { createContext, useContext, type ReactNode } from "react";
import { GRID } from "../../shared/catalog";

import { proj, TH, TW, UH } from "../../shared/iso";

export { proj, TH, TW, UH };
export const WALL_H = 3.4; // 벽 높이

/** 화면 좌표 → 바닥 좌표(z = 0) */
export function unproj(sx: number, sy: number): [number, number] {
  const a = sx / (TW / 2);
  const b = sy / (TH / 2);
  return [(a + b) / 2, (b - a) / 2];
}

export const pts = (list: [number, number, number][]) => list.map(([x, y, z]) => proj(x, y, z).join(",")).join(" ");

/** 바닥 평면에 그릴 때: 안에서는 (gx, gy) 단위로 그린다. */
export const FLOOR_FRAME = `matrix(${TW / 2},${TH / 2},${-TW / 2},${TH / 2},0,0)`;
/** 오른쪽 벽(y = 0)에 그릴 때: u 는 벽을 따라(gx), w 는 벽 꼭대기에서 아래로. */
export const RIGHT_WALL_FRAME = `matrix(${TW / 2},${TH / 2},0,${UH},0,${-WALL_H * UH})`;
/** 왼쪽 벽(x = 0)에 그릴 때: u 는 벽의 왼쪽 끝(gy = GRID)에서 안쪽 모서리로, w 는 위에서 아래로. */
export const LEFT_WALL_FRAME = `matrix(${TW / 2},${-TH / 2},0,${UH},${-GRID * (TW / 2)},${GRID * (TH / 2) - WALL_H * UH})`;

/** 물건의 y = y0 인 앞면(왼쪽 아래를 보는 면) 위에 그릴 때: 안에서는 (gx, z) 이고 z 는 위로. */
export const faceY = (y0: number) => `matrix(${TW / 2},${TH / 2},0,${-UH},${-y0 * (TW / 2)},${y0 * (TH / 2)})`;
/** 물건의 x = x0 인 앞면(오른쪽 아래를 보는 면) 위에 그릴 때: 안에서는 (gy, z). */
export const faceX = (x0: number) => `matrix(${-TW / 2},${TH / 2},0,${-UH},${x0 * (TW / 2)},${x0 * (TH / 2)})`;

// ── 색 ──

function parseHex(hex: string): [number, number, number] {
  // shade()·mixHex() 가 돌려준 rgb(…) 도 다시 섞을 수 있게
  const rgb = /^rgb\((\d+),\s*(\d+),\s*(\d+)\)$/.exec(hex);
  if (rgb) return [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])];
  const h = hex.replace("#", "");
  const n = parseInt(h.length === 3 ? h.replace(/./g, (c) => c + c) : h, 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

/** amt > 0 이면 흰색 쪽으로, < 0 이면 검은색 쪽으로 섞는다. */
export function shade(hex: string, amt: number) {
  const [r, g, b] = parseHex(hex);
  const t = amt > 0 ? 255 : 0;
  const k = Math.abs(amt);
  const mix = (c: number) => Math.round(c + (t - c) * k);
  return `rgb(${mix(r)},${mix(g)},${mix(b)})`;
}

export function mixHex(a: string, b: string, t: number) {
  const ca = parseHex(a);
  const cb = parseHex(b);
  const m = (i: number) => Math.round(ca[i] + (cb[i] - ca[i]) * t);
  return `rgb(${m(0)},${m(1)},${m(2)})`;
}

// ── 그리는 중인 물건이 뒤집혔나(빛 방향을 맞추려고) ──

const FlipCtx = createContext(false);
export const FlipProvider = ({ flip, children }: { flip: boolean; children: ReactNode }) => (
  <FlipCtx.Provider value={flip}>{children}</FlipCtx.Provider>
);

/** 상자 하나: 윗면은 밝게, 왼쪽 아래 면은 그대로, 오른쪽 아래 면은 어둡게. */
export function Box({
  x = 0,
  y = 0,
  z = 0,
  w,
  d,
  h,
  c,
  top,
  edge = true,
}: {
  x?: number;
  y?: number;
  z?: number;
  w: number;
  d: number;
  h: number;
  c: string;
  top?: string;
  edge?: boolean;
}) {
  const flip = useContext(FlipCtx);
  // 뒤집으면 화면에서 좌우가 바뀌므로, 빛이 늘 왼쪽 위에서 오게 두 면의 밝기를 바꾼다.
  const left = shade(c, flip ? -0.16 : 0);
  const right = shade(c, flip ? 0 : -0.16);
  const stroke = edge ? shade(c, -0.32) : "none";
  return (
    <g strokeWidth={0.6} stroke={stroke} strokeLinejoin="round">
      <polygon points={pts([[x, y + d, z], [x + w, y + d, z], [x + w, y + d, z + h], [x, y + d, z + h]])} fill={left} />
      <polygon points={pts([[x + w, y, z], [x + w, y + d, z], [x + w, y + d, z + h], [x + w, y, z + h]])} fill={right} />
      <polygon points={pts([[x, y, z + h], [x + w, y, z + h], [x + w, y + d, z + h], [x, y + d, z + h]])} fill={top ?? shade(c, 0.14)} />
    </g>
  );
}

/** 바닥에 선 원기둥. (cx, cy) 는 바닥 좌표의 가운데, r 은 칸 단위 반지름. */
export function Cyl({
  cx,
  cy,
  z = 0,
  r,
  h,
  c,
  top,
  taper = 0,
}: {
  cx: number;
  cy: number;
  z?: number;
  r: number;
  h: number;
  c: string;
  top?: string;
  /** 위쪽 반지름을 이만큼 줄이거나(+) 늘린다(-). 화분·갓처럼. */
  taper?: number;
}) {
  const [sx, sy0] = proj(cx, cy, z);
  const [, sy1] = proj(cx, cy, z + h);
  const k = Math.SQRT2 * (TW / 2);
  const rx0 = r * k;
  const ry0 = rx0 / 2;
  const rx1 = (r - taper) * k;
  const ry1 = rx1 / 2;
  const body = `M ${sx - rx1} ${sy1} L ${sx - rx0} ${sy0} A ${rx0} ${ry0} 0 0 0 ${sx + rx0} ${sy0} L ${sx + rx1} ${sy1} Z`;
  return (
    <g stroke={shade(c, -0.32)} strokeWidth={0.6}>
      <path d={body} fill={shade(c, -0.06)} />
      <ellipse cx={sx} cy={sy1} rx={rx1} ry={ry1} fill={top ?? shade(c, 0.14)} />
    </g>
  );
}

/** 바닥 위 납작한 사각형(러그·그림자·칸 표시) */
export function Flat({ x, y, w, d, z = 0, fill, stroke, opacity }: { x: number; y: number; w: number; d: number; z?: number; fill: string; stroke?: string; opacity?: number }) {
  return (
    <polygon
      points={pts([[x, y, z], [x + w, y, z], [x + w, y + d, z], [x, y + d, z]])}
      fill={fill}
      stroke={stroke}
      strokeWidth={stroke ? 1.4 : 0}
      opacity={opacity}
    />
  );
}

/** 물건 아래에 깔리는 옅은 그림자 */
export function Shadow({ w, d, inset = 0.06 }: { w: number; d: number; inset?: number }) {
  return <Flat x={inset} y={inset} w={w - inset} d={d - inset} fill="rgba(40,30,20,0.16)" />;
}
