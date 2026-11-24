// 벽 장식 그림. 벽 좌표에서 x 는 u(물건의 왼쪽 끝)부터 벽을 따라 u + w 까지, y 는 벽 꼭대기 0 에서 아래로 3.4 까지.
// 상점 그림(Thumb)은 y 0.3~2.25 만 보여서 그 안에 그린다. 불빛 나는 것은 밤이면 밝은 색에 옅은 번짐을 겹친다.

import type { ReactNode } from "react";
import { C, shade, type WallDraw } from "../kit";

type Pt = [number, number];

const rad = (deg: number) => (deg * Math.PI) / 180;
/** (cx, cy) 에서 12시부터 시계 방향으로 deg, 거리 r 인 점 */
const around = (cx: number, cy: number, deg: number, r: number): Pt => [cx + Math.sin(rad(deg)) * r, cy - Math.cos(rad(deg)) * r];

const UNIT_HEART =
  "M 0 1 C -0.4 0.7 -1 0.3 -1 -0.25 C -1 -0.65 -0.7 -0.9 -0.45 -0.9 C -0.2 -0.9 -0.05 -0.75 0 -0.55 C 0.05 -0.75 0.2 -0.9 0.45 -0.9 C 0.7 -0.9 1 -0.65 1 -0.25 C 1 0.3 0.4 0.7 0 1 Z";

/** 하트 경로: 가운데 (cx, cy), 반폭 s, 뾰족한 끝이 +y 쪽 */
export function heart(cx: number, cy: number, s: number) {
  let i = 0;
  return UNIT_HEART.replace(/-?\d*\.?\d+/g, (n) => String(+((i++ % 2 ? cy : cx) + Number(n) * s).toFixed(4)));
}

const nail = (x: number, y: number) => <circle cx={x} cy={y} r={0.025} fill={C.slate} />;

/** 압정·지도 핀의 머리 */
const pin = (x: number, y: number, c: string, r = 0.03) => (
  <g key={`pin${x},${y}`}>
    <circle cx={x} cy={y} r={r} fill={c} stroke={shade(c, -0.3)} strokeWidth={0.008} />
    <circle cx={x - r * 0.3} cy={y - r * 0.35} r={r * 0.32} fill="#fff" opacity={0.75} />
  </g>
);

/** 시침·분침 */
const hands = (cx: number, cy: number, now: Date, r: number) => {
  const h = (now.getHours() % 12) * 30 + now.getMinutes() * 0.5;
  const m = now.getMinutes() * 6;
  return (
    <g stroke={C.ink} strokeLinecap="round">
      <line x1={cx} y1={cy} x2={around(cx, cy, h, r * 0.55)[0]} y2={around(cx, cy, h, r * 0.55)[1]} strokeWidth={r * 0.16} />
      <line x1={cx} y1={cy} x2={around(cx, cy, m, r * 0.8)[0]} y2={around(cx, cy, m, r * 0.8)[1]} strokeWidth={r * 0.1} />
    </g>
  );
};

/** 꺾은선 위에 고르게 찍은 점(전구 자리). closed 면 끝점을 빼서 첫 점과 겹치지 않는다. */
function along(pts: Pt[], gap: number, closed = false): Pt[] {
  const seg = pts.slice(1).map((p, i) => Math.hypot(p[0] - pts[i][0], p[1] - pts[i][1]));
  const total = seg.reduce((s, l) => s + l, 0);
  const n = Math.max(1, Math.round(total / gap));
  const out: Pt[] = [];
  for (let k = 0; k < (closed ? n : n + 1); k++) {
    let d = (k * total) / n;
    let i = 0;
    while (i < seg.length - 1 && d > seg[i]) d -= seg[i++];
    const t = Math.min(1, d / seg[i]);
    out.push([pts[i][0] + (pts[i + 1][0] - pts[i][0]) * t, pts[i][1] + (pts[i + 1][1] - pts[i][1]) * t]);
  }
  return out;
}

const ring = (cx: number, cy: number, rx: number, ry: number, n = 28): Pt[] =>
  Array.from({ length: n + 1 }, (_, i) => [cx + Math.sin((i / n) * 2 * Math.PI) * rx, cy - Math.cos((i / n) * 2 * Math.PI) * ry]);

/** LOVE 의 획(글자 칸 0.36×0.56 안). O 는 닫힌 고리. */
const LOVE: { strokes: Pt[][]; closed?: boolean }[] = [
  { strokes: [[[0.07, 0.05], [0.07, 0.51], [0.31, 0.51]]] },
  { strokes: [ring(0.18, 0.28, 0.125, 0.23)], closed: true },
  { strokes: [[[0.05, 0.05], [0.18, 0.51], [0.31, 0.05]]] },
  {
    strokes: [
      [[0.3, 0.05], [0.07, 0.05], [0.07, 0.51], [0.3, 0.51]],
      [[0.07, 0.28], [0.26, 0.28]],
    ],
  },
];

const polyline = (pts: Pt[]) => `M ${pts.map((p) => p.join(" ")).join(" L ")}`;

/** 덩굴 하나: 3차 곡선 [시작, 조절1, 조절2, 끝] 의 t 자리 */
const bez = ([p0, p1, p2, p3]: Pt[], t: number): Pt => {
  const s = 1 - t;
  const f = (k: 0 | 1) => s * s * s * p0[k] + 3 * s * s * t * p1[k] + 3 * s * t * t * p2[k] + t * t * t * p3[k];
  return [f(0), f(1)];
};

const leaf = (x: number, y: number, deg: number, c: string, s = 1, key?: string | number) => (
  <ellipse key={key} cx={x} cy={y} rx={0.065 * s} ry={0.032 * s} transform={`rotate(${deg} ${x} ${y})`} fill={c} stroke={C.leafDark} strokeWidth={0.008} />
);

/** 액자 하나: 테두리 c, 안쪽 바탕 mat, 그 위에 그림 */
const frame = (x: number, y: number, w: number, h: number, c: string, mat: string, pic: ReactNode) => (
  <g key={`${x},${y}`}>
    <rect x={x} y={y} width={w} height={h} fill={c} stroke={shade(c, -0.3)} strokeWidth={0.012} />
    <rect x={x + 0.05} y={y + 0.05} width={w - 0.1} height={h - 0.1} fill={mat} />
    {pic}
  </g>
);

/** 다트판의 고리 한 칸(12시부터 시계 방향 a0→a1, 반지름 r0→r1) */
const sector = (cx: number, cy: number, r0: number, r1: number, a0: number, a1: number) => {
  const [p, q, s, t] = [around(cx, cy, a0, r1), around(cx, cy, a1, r1), around(cx, cy, a1, r0), around(cx, cy, a0, r0)];
  return `M ${p[0]} ${p[1]} A ${r1} ${r1} 0 0 1 ${q[0]} ${q[1]} L ${s[0]} ${s[1]} A ${r0} ${r0} 0 0 0 ${t[0]} ${t[1]} Z`;
};

/** 부드러운 덩어리: 점들의 가운데를 이어 둥글게 닫는다(지도 대륙) */
const blob = (pts: Pt[]) => {
  const mid = (a: Pt, b: Pt) => `${(a[0] + b[0]) / 2} ${(a[1] + b[1]) / 2}`;
  const n = pts.length;
  return `M ${mid(pts[n - 1], pts[0])} ${pts.map((p, i) => `Q ${p[0]} ${p[1]} ${mid(p, pts[(i + 1) % n])}`).join(" ")} Z`;
};

const CONTINENTS: Pt[][] = [
  [[0.07, 0.2], [0.18, 0.09], [0.33, 0.1], [0.4, 0.2], [0.33, 0.32], [0.27, 0.45], [0.21, 0.37], [0.12, 0.32]],
  [[0.27, 0.5], [0.36, 0.51], [0.39, 0.61], [0.33, 0.78], [0.3, 0.88], [0.27, 0.7], [0.24, 0.57]],
  [[0.38, 0.05], [0.46, 0.04], [0.47, 0.13], [0.4, 0.15]],
  [[0.48, 0.17], [0.55, 0.11], [0.6, 0.18], [0.56, 0.27], [0.49, 0.26]],
  [[0.5, 0.34], [0.6, 0.32], [0.67, 0.42], [0.62, 0.58], [0.57, 0.72], [0.53, 0.58], [0.47, 0.45]],
  [[0.6, 0.12], [0.76, 0.07], [0.91, 0.13], [0.93, 0.26], [0.84, 0.36], [0.76, 0.43], [0.68, 0.36], [0.62, 0.26]],
  [[0.8, 0.62], [0.9, 0.6], [0.94, 0.7], [0.86, 0.76], [0.79, 0.71]],
];

export const WALLS: Record<string, WallDraw> = {
  wall_mirror: (u, { night }) => {
    const [cx, cy] = [u + 0.5, 1.12];
    return (
      <g>
        <path d={`M ${cx - 0.13} ${cy - 0.357} L ${cx} 0.5 L ${cx + 0.13} ${cy - 0.357}`} fill="none" stroke={C.woodDark} strokeWidth={0.022} strokeLinejoin="round" />
        {nail(cx, 0.5)}
        <circle cx={cx} cy={cy} r={0.38} fill={C.wood} stroke={C.woodDark} strokeWidth={0.015} />
        <circle cx={cx} cy={cy} r={0.31} fill={night ? "#8EA2B6" : "#D3E6EE"} stroke={shade(C.wood, -0.25)} strokeWidth={0.015} />
        <g stroke="#fff" strokeLinecap="round">
          <line x1={cx - 0.2} y1={cy + 0.02} x2={cx + 0.02} y2={cy - 0.2} strokeWidth={0.06} opacity={0.75} />
          <line x1={cx - 0.11} y1={cy + 0.12} x2={cx + 0.11} y2={cy - 0.1} strokeWidth={0.03} opacity={0.6} />
        </g>
      </g>
    );
  },

  wall_neon: (u, { night }) => {
    const d = heart(u + 0.5, 1.12, 0.3);
    return (
      <g fill="none" strokeLinecap="round" strokeLinejoin="round">
        <rect x={u + 0.13} y={0.77} width={0.74} height={0.72} rx={0.06} fill="rgba(255,255,255,.22)" stroke="rgba(120,120,130,.3)" strokeWidth={0.012} />
        <path d={`M ${u + 0.5} 1.42 C ${u + 0.52} 1.62 ${u + 0.74} 1.58 ${u + 0.76} 1.8 L ${u + 0.76} 2.18`} stroke={C.slate} strokeWidth={0.014} />
        {night && (
          <>
            <path d={d} stroke="#FF5C9D" strokeWidth={0.3} opacity={0.14} />
            <path d={d} stroke="#FF6FAA" strokeWidth={0.16} opacity={0.32} />
          </>
        )}
        <path d={d} stroke={night ? "#FF86BC" : "#F2A2C0"} strokeWidth={0.07} />
        <path d={d} stroke={night ? "#FFF0F6" : "#FBD9E6"} strokeWidth={0.024} />
      </g>
    );
  },

  wall_lights: (u, { night }) => {
    const [y0, sag] = [0.5, 0.2];
    const swags: Pt[] = [
      [u + 0.06, u + 1],
      [u + 1, u + 1.94],
    ];
    const cord = `M ${u + 0.06} ${y0} ${swags.map(([a, b]) => `Q ${(a + b) / 2} ${y0 + 2 * sag} ${b} ${y0}`).join(" ")}`;
    const bulbs = swags.flatMap(([a, b]) => [0.17, 0.39, 0.61, 0.83].map((t): Pt => [a + (b - a) * t, y0 + 4 * sag * t * (1 - t)]));
    const warm = ["#FBE3A4", "#F9D3A0", "#FCEBC0"];
    return (
      <g>
        <path d={cord} fill="none" stroke="#5E554B" strokeWidth={0.018} />
        {[u + 0.06, u + 1, u + 1.94].map((x) => (
          <circle key={x} cx={x} cy={y0} r={0.022} fill={C.slate} />
        ))}
        {bulbs.map(([x, y], i) => (
          <g key={i}>
            {night && <circle cx={x} cy={y + 0.1} r={0.15} fill="#FFD36E" opacity={0.28} />}
            <rect x={x - 0.025} y={y} width={0.05} height={0.05} rx={0.01} fill="#5E554B" />
            <ellipse cx={x} cy={y + 0.11} rx={0.055} ry={0.07} fill={night ? "#FFF3C4" : warm[i % 3]} stroke={night ? "#FFC85A" : "#CDAA6A"} strokeWidth={0.01} />
          </g>
        ))}
      </g>
    );
  },

  wall_calendar: (u, { now }) => {
    const [x0, w, top] = [u + 0.21, 0.58, 0.62];
    const first = new Date(now.getFullYear(), now.getMonth(), 1).getDay();
    const days = new Date(now.getFullYear(), now.getMonth() + 1, 0).getDate();
    const cell = (day: number): Pt => {
      const k = first + day - 1;
      return [x0 + 0.065 + (k % 7) * 0.075, top + 0.62 + Math.floor(k / 7) * 0.062];
    };
    const [tx, ty] = cell(now.getDate());
    return (
      <g>
        <path d={`M ${u + 0.3} ${top} L ${u + 0.5} 0.44 L ${u + 0.7} ${top}`} fill="none" stroke={C.woodDark} strokeWidth={0.012} />
        {nail(u + 0.5, 0.44)}
        <rect x={x0} y={top} width={w} height={1.0} fill={C.white} stroke="#CFCAC0" strokeWidth={0.012} />
        <rect x={x0 + 0.04} y={top + 0.07} width={w - 0.08} height={0.36} fill="#CFE8F2" />
        <circle cx={x0 + 0.42} cy={top + 0.17} r={0.05} fill="#F5C66A" />
        <path d={`M ${x0 + 0.04} ${top + 0.43} L ${x0 + 0.04} ${top + 0.31} Q ${x0 + 0.2} ${top + 0.18} ${x0 + 0.34} ${top + 0.32} Q ${x0 + 0.44} ${top + 0.25} ${x0 + w - 0.04} ${top + 0.33} L ${x0 + w - 0.04} ${top + 0.43} Z`} fill={C.leaf} />
        <rect x={x0 + 0.04} y={top + 0.48} width={w - 0.08} height={0.07} fill={C.blush} />
        {Array.from({ length: days }, (_, i) => {
          const [x, y] = cell(i + 1);
          return <rect key={i} x={x - 0.022} y={y - 0.019} width={0.044} height={0.038} fill={(first + i) % 7 === 0 ? "#F0B2AA" : "#D6D2C8"} />;
        })}
        <circle cx={tx} cy={ty} r={0.045} fill="none" stroke="#E0464B" strokeWidth={0.018} />
        <rect x={x0 - 0.01} y={top - 0.03} width={w + 0.02} height={0.06} rx={0.015} fill={C.slate} />
      </g>
    );
  },

  wall_corkboard: (u) => {
    const specks = Array.from({ length: 26 }, (_, i): Pt => [u + 0.2 + ((i * 37) % 100) * 0.016, 0.68 + ((i * 53) % 100) * 0.0095]);
    return (
      <g>
        <rect x={u + 0.1} y={0.58} width={1.8} height={1.14} rx={0.03} fill={C.wood} stroke={C.woodDark} strokeWidth={0.015} />
        <rect x={u + 0.16} y={0.64} width={1.68} height={1.02} fill="#D9B68C" />
        {specks.map(([x, y], i) => (
          <circle key={i} cx={x} cy={y} r={0.012} fill="#B98F62" />
        ))}
        <g transform={`rotate(-3 ${u + 0.41} 0.72)`}>
          <rect x={u + 0.24} y={0.72} width={0.34} height={0.8} fill={C.white} stroke="#D8D4CC" strokeWidth={0.008} />
          {[0.86, 0.97, 1.08, 1.19, 1.3, 1.41].map((y, i) => (
            <g key={y}>
              <rect x={u + 0.28} y={y - 0.025} width={0.04} height={0.04} fill="none" stroke="#8CA3B5" strokeWidth={0.008} />
              <line x1={u + 0.35} y1={y} x2={u + 0.35 + [0.16, 0.12, 0.17, 0.1, 0.14, 0.09][i]} y2={y} stroke="#8CA3B5" strokeWidth={0.014} />
            </g>
          ))}
          <path d={`M ${u + 0.282} 0.855 l 0.015 0.018 l 0.03 -0.04 M ${u + 0.282} 0.965 l 0.015 0.018 l 0.03 -0.04`} fill="none" stroke="#E0464B" strokeWidth={0.012} />
          {pin(u + 0.41, 0.76, "#E0464B")}
        </g>
        <g transform={`rotate(5 ${u + 0.9} 0.76)`}>
          <rect x={u + 0.72} y={0.74} width={0.36} height={0.44} fill={C.white} stroke="#D8D4CC" strokeWidth={0.008} />
          <rect x={u + 0.75} y={0.77} width={0.3} height={0.3} fill="#CFE8F2" />
          <path d={`M ${u + 0.75} 1.07 L ${u + 0.75} 0.98 Q ${u + 0.85} 0.9 ${u + 0.93} 0.98 Q ${u + 0.99} 0.94 ${u + 1.05} 0.99 L ${u + 1.05} 1.07 Z`} fill={C.leaf} />
          <circle cx={u + 0.99} cy={0.84} r={0.03} fill="#F5C66A" />
          {pin(u + 0.9, 0.765, "#5B8FB0")}
        </g>
        <g transform={`rotate(-5 ${u + 1.36} 0.74)`}>
          <rect x={u + 1.21} y={0.72} width={0.3} height={0.3} fill="#F6DD7C" />
          {[0.84, 0.9, 0.96].map((y) => (
            <line key={y} x1={u + 1.26} y1={y} x2={u + 1.45} y2={y} stroke="#C9A84A" strokeWidth={0.012} />
          ))}
          {pin(u + 1.36, 0.76, C.eggDeep)}
        </g>
        <g transform={`rotate(7 ${u + 1.63} 1.12)`}>
          <rect x={u + 1.49} y={1.1} width={0.28} height={0.28} fill="#F6B8C4" />
          <path d={heart(u + 1.63, 1.25, 0.06)} fill="#E0647E" />
          {pin(u + 1.63, 1.14, C.straw)}
        </g>
        <g transform={`rotate(-2 ${u + 1.04} 1.3)`}>
          <rect x={u + 0.74} y={1.3} width={0.6} height={0.24} fill="#BFE3D6" />
          <line x1={u + 0.79} y1={1.38} x2={u + 1.16} y2={1.38} stroke="#6FA897" strokeWidth={0.014} />
          <line x1={u + 0.79} y1={1.46} x2={u + 1.06} y2={1.46} stroke="#6FA897" strokeWidth={0.014} />
          <line x1={u + 1.22} y1={1.32} x2={u + 1.22} y2={1.52} stroke="#6FA897" strokeWidth={0.008} strokeDasharray="0.02 0.015" />
          {pin(u + 1.04, 1.33, C.blush)}
        </g>
      </g>
    );
  },

  wall_ivy: (u) => {
    const cx = u + 0.5;
    const vines: Pt[][] = [
      [[cx - 0.14, 1.2], [cx - 0.3, 1.45], [cx - 0.12, 1.7], [cx - 0.27, 1.98]],
      [[cx + 0.02, 1.26], [cx + 0.1, 1.55], [cx - 0.06, 1.85], [cx + 0.05, 2.14]],
      [[cx + 0.16, 1.2], [cx + 0.3, 1.4], [cx + 0.2, 1.6], [cx + 0.3, 1.8]],
    ];
    const greens = [C.leaf, "#86B56B", C.leafDark];
    return (
      <g>
        <g stroke="#C9B28A" strokeWidth={0.014} fill="none">
          <path d={`M ${cx} 0.44 L ${cx - 0.2} 1.02 M ${cx} 0.44 L ${cx + 0.2} 1.02 M ${cx} 0.44 L ${cx - 0.05} 1.02`} />
        </g>
        {[-0.135, -0.035, 0.135].map((dx) => (
          <circle key={dx} cx={cx + dx} cy={0.83} r={0.016} fill="#C9B28A" />
        ))}
        <circle cx={cx} cy={0.42} r={0.03} fill="none" stroke={C.slate} strokeWidth={0.014} />
        {nail(cx, 0.38)}
        {vines.map((v, i) => (
          <path key={i} d={`M ${v[0].join(" ")} C ${v[1].join(" ")} ${v[2].join(" ")} ${v[3].join(" ")}`} fill="none" stroke={C.leafDark} strokeWidth={0.012} />
        ))}
        <path d={`M ${cx - 0.22} 1.0 L ${cx + 0.22} 1.0 L ${cx + 0.17} 1.2 Q ${cx} 1.3 ${cx - 0.17} 1.2 Z`} fill="#D98C66" stroke={shade("#D98C66", -0.3)} strokeWidth={0.012} />
        <rect x={cx - 0.23} y={0.99} width={0.46} height={0.05} rx={0.015} fill="#E5A27F" stroke={shade("#D98C66", -0.3)} strokeWidth={0.01} />
        {[-0.18, -0.08, 0.04, 0.14, -0.13, 0.1].map((dx, i) => leaf(cx + dx, i < 4 ? 0.96 : 0.92, [-30, 20, -15, 35, 60, -60][i], greens[i % 3], 1.1, `t${i}`))}
        {vines.flatMap((v, i) =>
          [0.18, 0.36, 0.54, 0.72, 0.9, 1].map((t, j) => {
            const [x, y] = bez(v, t);
            const side = j % 2 ? 1 : -1;
            return leaf(x + side * 0.04, y, side * 40 + 90, greens[(i + j) % 3], 0.95, `${i}-${j}`);
          }),
        )}
      </g>
    );
  },

  wall_guitar: (u) => {
    const cx = u + 0.5;
    const half = (k: 1 | -1) =>
      `C ${cx + k * 0.13} 1.25 ${cx + k * 0.21} 1.33 ${cx + k * 0.2} 1.45 C ${cx + k * 0.2} 1.53 ${cx + k * 0.15} 1.56 ${cx + k * 0.16} 1.62 C ${cx + k * 0.17} 1.68 ${cx + k * 0.28} 1.72 ${cx + k * 0.28} 1.86 C ${cx + k * 0.28} 2.02 ${cx + k * 0.15} 2.12 ${cx} 2.12`;
    const wood = "#E6B676";
    return (
      <g>
        <path d={`M ${cx} 1.25 ${half(1)} M ${cx} 1.25 ${half(-1)}`} fill={wood} />
        <path d={`M ${cx} 1.25 ${half(1)} M ${cx} 1.25 ${half(-1)}`} fill="none" stroke="#9A6A3E" strokeWidth={0.02} />
        <ellipse cx={cx + 0.08} cy={1.86} rx={0.1} ry={0.13} fill="#fff" opacity={0.14} />
        <circle cx={cx} cy={1.56} r={0.085} fill="none" stroke="#B9874F" strokeWidth={0.014} />
        <circle cx={cx} cy={1.56} r={0.065} fill="#3B2A20" />
        <rect x={cx - 0.09} y={1.9} width={0.18} height={0.04} rx={0.01} fill="#4A3226" />
        <rect x={cx - 0.04} y={0.62} width={0.08} height={0.72} fill="#6B4630" />
        {[0.72, 0.82, 0.91, 1.0, 1.08, 1.16].map((y) => (
          <line key={y} x1={cx - 0.04} y1={y} x2={cx + 0.04} y2={y} stroke="#C9B89A" strokeWidth={0.008} />
        ))}
        <g stroke="#F1E9DA" strokeWidth={0.006} opacity={0.85}>
          {[-0.022, 0, 0.022].map((dx) => (
            <line key={dx} x1={cx + dx} y1={0.63} x2={cx + dx * 1.6} y2={1.91} />
          ))}
        </g>
        <rect x={cx - 0.07} y={0.42} width={0.14} height={0.21} rx={0.035} fill="#5A3A28" />
        {[0.47, 0.53, 0.59].flatMap((y) => [
          <circle key={`l${y}`} cx={cx - 0.09} cy={y} r={0.018} fill="#D5D2CA" stroke="#8E8A82" strokeWidth={0.006} />,
          <circle key={`r${y}`} cx={cx + 0.09} cy={y} r={0.018} fill="#D5D2CA" stroke="#8E8A82" strokeWidth={0.006} />,
        ])}
        <rect x={cx - 0.045} y={0.615} width={0.09} height={0.015} fill="#EFE6D2" />
        <path d={`M ${cx - 0.085} 0.6 Q ${cx - 0.085} 0.69 ${cx} 0.69 Q ${cx + 0.085} 0.69 ${cx + 0.085} 0.6`} fill="none" stroke={C.slate} strokeWidth={0.022} strokeLinecap="round" />
      </g>
    );
  },

  wall_dartboard: (u) => {
    const [cx, cy] = [u + 0.5, 1.1];
    const cream = "#F2E6C9";
    const dark = "#34302F";
    const dart = (x: number, y: number, dx: number, dy: number, c: string) => (
      <g key={`${x}`} strokeLinecap="round">
        <line x1={x} y1={y} x2={x + dx * 0.55} y2={y + dy * 0.55} stroke="#A9B0B5" strokeWidth={0.018} />
        <line x1={x + dx * 0.2} y1={y + dy * 0.2} x2={x + dx * 0.5} y2={y + dy * 0.5} stroke="#6E767C" strokeWidth={0.034} />
        <path d={`M ${x + dx * 0.62} ${y + dy * 0.62} L ${x + dx - dy * 0.35} ${y + dy + dx * 0.35} L ${x + dx * 0.85} ${y + dy * 0.85} L ${x + dx + dy * 0.35} ${y + dy - dx * 0.35} Z`} fill={c} stroke={shade(c, -0.3)} strokeWidth={0.008} />
      </g>
    );
    return (
      <g>
        {nail(cx, 0.66)}
        <circle cx={cx} cy={cy} r={0.38} fill={dark} stroke="#1E1C1B" strokeWidth={0.012} />
        {Array.from({ length: 20 }, (_, i) => {
          const [a0, a1] = [i * 18 - 9, i * 18 + 9];
          return (
            <g key={i}>
              <path d={sector(cx, cy, 0.05, 0.3, a0, a1)} fill={i % 2 ? cream : dark} />
              <path d={sector(cx, cy, 0.275, 0.3, a0, a1)} fill={i % 2 ? "#4E9A6A" : "#D9534F"} />
              <path d={sector(cx, cy, 0.165, 0.19, a0, a1)} fill={i % 2 ? "#4E9A6A" : "#D9534F"} />
              <circle cx={around(cx, cy, i * 18, 0.34)[0]} cy={around(cx, cy, i * 18, 0.34)[1]} r={0.009} fill={cream} />
            </g>
          );
        })}
        <circle cx={cx} cy={cy} r={0.05} fill="#4E9A6A" />
        <circle cx={cx} cy={cy} r={0.022} fill="#D9534F" />
        {dart(cx + 0.1, cy - 0.1, 0.2, -0.17, C.blush)}
        {dart(cx - 0.13, cy + 0.12, 0.22, -0.06, C.egg)}
      </g>
    );
  },

  wall_tapestry: (u) => {
    const [x0, x1, y0, y1] = [u + 0.32, u + 1.68, 0.6, 1.74];
    return (
      <g>
        <path d={`M ${u + 0.26} 0.57 L ${u + 1} 0.4 L ${u + 1.74} 0.57`} fill="none" stroke={C.woodDark} strokeWidth={0.012} />
        {nail(u + 1, 0.4)}
        <g stroke="#D9C7AA" strokeWidth={0.016} strokeLinecap="round">
          {Array.from({ length: 24 }, (_, i) => (
            <line key={i} x1={x0 + 0.04 + i * 0.0557} y1={y1} x2={x0 + 0.04 + i * 0.0557} y2={y1 + 0.13} />
          ))}
        </g>
        <rect x={x0} y={y0} width={x1 - x0} height={y1 - y0} fill="#F5E9D6" stroke="#D9C7AA" strokeWidth={0.012} />
        <circle cx={u + 1.05} cy={1.08} r={0.24} fill="#EFB45C" />
        <circle cx={u + 1.05} cy={1.08} r={0.32} fill="none" stroke="#EFB45C" strokeWidth={0.018} strokeDasharray="0.04 0.05" />
        <path d={`M ${x0} 1.46 L ${u + 0.64} 1.08 L ${u + 0.86} 1.3 L ${u + 1.18} 0.96 L ${x1} 1.44 L ${x1} ${y1} L ${x0} ${y1} Z`} fill="#D99A84" />
        <path d={`M ${u + 1.18} 0.96 L ${u + 1.27} 1.06 L ${u + 1.22} 1.04 L ${u + 1.18} 1.08 L ${u + 1.13} 1.03 L ${u + 1.09} 1.05 Z`} fill="#FBF3E6" />
        <path d={`M ${x0} 1.6 L ${u + 0.8} 1.27 L ${u + 1.08} 1.5 L ${u + 1.36} 1.3 L ${x1} 1.58 L ${x1} ${y1} L ${x0} ${y1} Z`} fill="#8FAE8B" />
        <path d={`M ${u + 0.5} 0.82 q 0.03 -0.03 0.06 0 q 0.03 -0.03 0.06 0 M ${u + 0.68} 0.74 q 0.025 -0.025 0.05 0 q 0.025 -0.025 0.05 0`} fill="none" stroke="#7A6A5A" strokeWidth={0.012} />
        <rect x={u + 0.22} y={0.55} width={1.56} height={0.06} rx={0.03} fill={C.wood} stroke={C.woodDark} strokeWidth={0.01} />
        <circle cx={u + 0.22} cy={0.58} r={0.04} fill={C.woodDark} />
        <circle cx={u + 1.78} cy={0.58} r={0.04} fill={C.woodDark} />
      </g>
    );
  },

  wall_map: (u) => {
    const [x0, y0, W, H] = [u + 0.18, 0.7, 1.64, 0.94];
    const at = ([x, y]: Pt): Pt => [x0 + x * W, y0 + y * H];
    const pins: Pt[] = [
      [0.3, 0.26],
      [0.52, 0.2],
      [0.83, 0.27],
      [0.89, 0.7],
    ];
    return (
      <g>
        <rect x={x0 - 0.05} y={y0 - 0.05} width={W + 0.1} height={H + 0.1} fill={C.woodDark} stroke={shade(C.woodDark, -0.3)} strokeWidth={0.012} />
        <rect x={x0} y={y0} width={W} height={H} fill="#A8D4E6" />
        <line x1={x0} y1={y0 + H * 0.5} x2={x0 + W} y2={y0 + H * 0.5} stroke="#fff" strokeWidth={0.008} strokeDasharray="0.04 0.03" opacity={0.7} />
        {CONTINENTS.map((c, i) => (
          <path key={i} d={blob(c.map(at))} fill="#F1E3B8" stroke="#C9B47E" strokeWidth={0.01} />
        ))}
        {pins.map((p) => {
          const [x, y] = at(p);
          return (
            <g key={`${x}`}>
              <line x1={x} y1={y} x2={x + 0.012} y2={y + 0.05} stroke="#6E767C" strokeWidth={0.01} />
              {pin(x, y, "#E0464B", 0.028)}
            </g>
          );
        })}
      </g>
    );
  },

  wall_frames: (u) => (
    <g>
      {frame(u + 0.16, 0.66, 0.52, 0.76, C.woodDark, "#F9E1E6", <path d={heart(u + 0.42, 1.05, 0.15)} fill="#E8798F" />)}
      {frame(
        u + 0.82,
        0.6,
        1.0,
        0.56,
        C.ink,
        "#CFE8F2",
        <>
          <circle cx={u + 1.58} cy={0.78} r={0.06} fill="#F5C66A" />
          <path d={`M ${u + 0.87} 1.11 L ${u + 0.87} 0.98 Q ${u + 1.1} 0.8 ${u + 1.3} 0.98 Q ${u + 1.5} 0.88 ${u + 1.77} 1.0 L ${u + 1.77} 1.11 Z`} fill={C.leaf} />
        </>,
      )}
      {frame(
        u + 0.96,
        1.28,
        0.58,
        0.5,
        C.white,
        "#FFF4DE",
        [
          [u + 1.15, "#F7C9B6"],
          [u + 1.35, "#FBE0A2"],
        ].map(([x, c]) => {
          const cx = x as number;
          return (
            <g key={cx}>
              <circle cx={cx} cy={1.54} r={0.1} fill={c as string} stroke={shade(c as string, -0.25)} strokeWidth={0.008} />
              <circle cx={cx - 0.035} cy={1.52} r={0.011} fill={C.ink} />
              <circle cx={cx + 0.035} cy={1.52} r={0.011} fill={C.ink} />
              <path d={`M ${cx - 0.035} 1.565 Q ${cx} 1.6 ${cx + 0.035} 1.565`} fill="none" stroke={C.ink} strokeWidth={0.01} strokeLinecap="round" />
            </g>
          );
        }),
      )}
    </g>
  ),

  wall_sconce: (u, { night }) => {
    const cx = u + 0.5;
    const brass = "#C9A15A";
    return (
      <g>
        {night && (
          <>
            <path d={`M ${cx - 0.17} 1.04 L ${cx - 0.4} 1.75 L ${cx + 0.4} 1.75 L ${cx + 0.17} 1.04 Z`} fill="#FFE3A3" opacity={0.16} />
            <path d={`M ${cx - 0.1} 0.78 L ${cx - 0.26} 0.42 L ${cx + 0.26} 0.42 L ${cx + 0.1} 0.78 Z`} fill="#FFE3A3" opacity={0.14} />
            <circle cx={cx} cy={0.92} r={0.36} fill="#FFD98A" opacity={0.16} />
            <circle cx={cx} cy={0.92} r={0.22} fill="#FFE3A3" opacity={0.3} />
          </>
        )}
        <rect x={cx - 0.06} y={1.16} width={0.12} height={0.24} rx={0.05} fill={brass} stroke={shade(brass, -0.3)} strokeWidth={0.01} />
        <path d={`M ${cx} 1.28 C ${cx + 0.13} 1.25 ${cx + 0.1} 1.1 ${cx} 1.07`} fill="none" stroke={brass} strokeWidth={0.025} strokeLinecap="round" />
        <rect x={cx - 0.028} y={1.02} width={0.056} height={0.06} rx={0.01} fill={brass} />
        <path d={`M ${cx - 0.1} 0.78 L ${cx + 0.1} 0.78 L ${cx + 0.17} 1.04 L ${cx - 0.17} 1.04 Z`} fill={night ? "#FFE9B0" : "#F2E2C2"} stroke={night ? "#E8B860" : shade("#F2E2C2", -0.3)} strokeWidth={0.012} strokeLinejoin="round" />
        {night && <ellipse cx={cx} cy={1.04} rx={0.16} ry={0.025} fill="#FFF8E0" />}
      </g>
    );
  },

  wall_tv: (u, { night }) => (
    <g>
      <rect x={u + 0.13} y={0.76} width={1.8} height={1.02} rx={0.03} fill="rgba(0,0,0,.1)" />
      <rect x={u + 0.1} y={0.72} width={1.8} height={1.02} rx={0.03} fill="#1F2327" />
      <rect x={u + 0.14} y={0.76} width={1.72} height={0.92} fill={night ? "#1A222C" : "#2B3641"} />
      <path d={`M ${u + 0.14} 0.76 L ${u + 0.9} 0.76 L ${u + 0.5} 1.68 L ${u + 0.14} 1.68 Z`} fill="#fff" opacity={0.06} />
      <path d={`M ${u + 0.98} 0.76 L ${u + 1.1} 0.76 L ${u + 0.7} 1.68 L ${u + 0.58} 1.68 Z`} fill="#fff" opacity={0.05} />
      <rect x={u + 0.96} y={1.7} width={0.08} height={0.015} fill="#4A5056" />
      <circle cx={u + 1.83} cy={1.71} r={0.01} fill="#E0464B" />
    </g>
  ),

  wall_aircon: (u, { night }) => (
    <g>
      <rect x={u + 0.17} y={0.47} width={1.72} height={0.46} rx={0.1} fill="rgba(0,0,0,.08)" />
      <rect x={u + 0.14} y={0.42} width={1.72} height={0.46} rx={0.1} fill="#F7F7F3" stroke="#C3C8C6" strokeWidth={0.015} />
      <line x1={u + 0.22} y1={0.55} x2={u + 1.78} y2={0.55} stroke="#E2E4E1" strokeWidth={0.012} />
      <rect x={u + 0.24} y={0.73} width={1.52} height={0.1} rx={0.04} fill="#D3D8D7" />
      <line x1={u + 0.27} y1={0.795} x2={u + 1.73} y2={0.795} stroke="#A9B1B1" strokeWidth={0.022} strokeLinecap="round" />
      <rect x={u + 1.44} y={0.6} width={0.2} height={0.08} rx={0.02} fill="#E6EAE9" />
      <path d={`M ${u + 1.49} 0.61 q 0.022 0.03 0.022 0.045 a 0.022 0.022 0 0 1 -0.044 0 q 0 -0.015 0.022 -0.045 Z`} fill="#6FB2D9" />
      <rect x={u + 1.53} y={0.625} width={0.08} height={0.03} rx={0.008} fill="#B9C7CC" />
      {night && <circle cx={u + 1.72} cy={0.64} r={0.05} fill="#7CF0B0" opacity={0.3} />}
      <circle cx={u + 1.72} cy={0.64} r={0.02} fill={night ? "#8DF5BD" : "#5BC08A"} />
    </g>
  ),

  wall_round_window: (u, { night, sky }) => {
    const [cx, cy, r] = [u + 0.5, 1.06, 0.32];
    return (
      <g>
        <circle cx={cx} cy={cy} r={r + 0.08} fill={C.white} stroke={shade(C.white, -0.2)} strokeWidth={0.012} />
        <circle cx={cx} cy={cy} r={r} fill={sky} />
        {night ? (
          <>
            <circle cx={cx + 0.14} cy={cy - 0.13} r={0.075} fill="#F5EBC4" />
            <circle cx={cx + 0.17} cy={cy - 0.15} r={0.06} fill={sky} />
            <circle cx={cx - 0.15} cy={cy - 0.1} r={0.016} fill="#fff" />
            <circle cx={cx - 0.08} cy={cy + 0.16} r={0.012} fill="#fff" />
            <circle cx={cx + 0.16} cy={cy + 0.12} r={0.014} fill="#fff" />
          </>
        ) : (
          <>
            <ellipse cx={cx - 0.1} cy={cy + 0.13} rx={0.14} ry={0.05} fill="rgba(255,255,255,.85)" />
            <ellipse cx={cx + 0.13} cy={cy - 0.12} rx={0.1} ry={0.035} fill="rgba(255,255,255,.7)" />
          </>
        )}
        <rect x={cx - 0.018} y={cy - r} width={0.036} height={r * 2} fill={C.white} />
        <rect x={cx - r} y={cy - 0.018} width={r * 2} height={0.036} fill={C.white} />
        <circle cx={cx} cy={cy} r={r} fill="none" stroke={shade(C.white, -0.15)} strokeWidth={0.016} />
      </g>
    );
  },

  wall_records: (u) => {
    const sleeves: { x: number; c: string; art: (x: number) => ReactNode }[] = [
      { x: u + 0.17, c: "#F4B3C2", art: (x) => <circle cx={x + 0.25} cy={1.24} r={0.11} fill="#FCEBD0" /> },
      {
        x: u + 0.75,
        c: "#7EC4BA",
        art: (x) => (
          <path d={[1.14, 1.24, 1.34].map((y) => `M ${x + 0.06} ${y} q 0.0475 -0.04 0.095 0 t 0.095 0 t 0.095 0 t 0.095 0`).join(" ")} fill="none" stroke="#F3FBF8" strokeWidth={0.022} />
        ),
      },
      { x: u + 1.33, c: C.straw, art: (x) => <path d={heart(x + 0.25, 1.25, 0.12)} fill="#C8463F" /> },
    ];
    return (
      <g>
        {sleeves.map(({ x }) => (
          <g key={`d${x}`}>
            <circle cx={x + 0.3} cy={1.04} r={0.21} fill="#1F1F22" />
            <circle cx={x + 0.3} cy={1.04} r={0.15} fill="none" stroke="#3B3B42" strokeWidth={0.01} />
            <circle cx={x + 0.3} cy={1.04} r={0.06} fill={C.blush} />
          </g>
        ))}
        {sleeves.map(({ x, c, art }) => (
          <g key={`s${x}`}>
            <rect x={x} y={1.0} width={0.5} height={0.5} fill={c} stroke={shade(c, -0.3)} strokeWidth={0.012} />
            {art(x)}
          </g>
        ))}
        <rect x={u + 0.1} y={1.44} width={1.8} height={0.05} fill={C.woodLight} stroke={C.woodDark} strokeWidth={0.008} />
        <rect x={u + 0.08} y={1.49} width={1.84} height={0.07} fill={C.wood} stroke={C.woodDark} strokeWidth={0.01} />
        <path d={`M ${u + 0.3} 1.56 l 0 0.12 l 0.1 -0.12 Z M ${u + 1.6} 1.56 l 0 0.12 l 0.1 -0.12 Z`} fill={C.woodDark} />
      </g>
    );
  },

  wall_wreath: (u) => {
    const [cx, cy, R] = [u + 0.5, 1.1, 0.27];
    const red = "#D9434B";
    const greens = [C.leaf, C.leafDark, "#8DBB6E"];
    return (
      <g>
        <line x1={cx} y1={0.5} x2={cx} y2={cy - R} stroke={red} strokeWidth={0.03} />
        {nail(cx, 0.5)}
        <circle cx={cx} cy={cy} r={R} fill="none" stroke={C.leafDark} strokeWidth={0.12} />
        {Array.from({ length: 30 }, (_, i) => {
          const a = i * 12;
          const [x, y] = around(cx, cy, a, R + (i % 2 ? 0.045 : -0.045));
          return leaf(x, y, a + (i % 2 ? 30 : -30), greens[i % 3], 1.15, i);
        })}
        {[35, 80, 150, 215, 290, 330].flatMap((a) =>
          [-6, 0, 6].map((da, k) => {
            const [x, y] = around(cx, cy, a + da, R + (k === 1 ? 0.03 : 0));
            return <circle key={`${a}-${k}`} cx={x} cy={y} r={0.022} fill={red} stroke={shade(red, -0.3)} strokeWidth={0.006} />;
          }),
        )}
        <g fill={red} stroke={shade(red, -0.3)} strokeWidth={0.01} strokeLinejoin="round">
          <path d={`M ${cx - 0.02} ${cy + R + 0.03} L ${cx - 0.1} ${cy + R + 0.2} L ${cx - 0.06} ${cy + R + 0.18} L ${cx - 0.05} ${cy + R + 0.22} L ${cx + 0.005} ${cy + R + 0.04} Z`} />
          <path d={`M ${cx + 0.02} ${cy + R + 0.03} L ${cx + 0.1} ${cy + R + 0.19} L ${cx + 0.065} ${cy + R + 0.18} L ${cx + 0.05} ${cy + R + 0.21} L ${cx - 0.005} ${cy + R + 0.04} Z`} />
          <path d={`M ${cx} ${cy + R} C ${cx - 0.06} ${cy + R - 0.1} ${cx - 0.17} ${cy + R - 0.05} ${cx - 0.13} ${cy + R + 0.04} C ${cx - 0.1} ${cy + R + 0.08} ${cx - 0.04} ${cy + R + 0.04} ${cx} ${cy + R} Z`} />
          <path d={`M ${cx} ${cy + R} C ${cx + 0.06} ${cy + R - 0.1} ${cx + 0.17} ${cy + R - 0.05} ${cx + 0.13} ${cy + R + 0.04} C ${cx + 0.1} ${cy + R + 0.08} ${cx + 0.04} ${cy + R + 0.04} ${cx} ${cy + R} Z`} />
          <circle cx={cx} cy={cy + R} r={0.035} />
        </g>
      </g>
    );
  },

  wall_letters: (u, { night }) => {
    const [x0, y0, gap] = [u + 0.16, 0.86, 0.44];
    const body = night ? "#FFEAC2" : "#F4E8D2";
    const edge = night ? "#E7B866" : "#C8A877";
    return (
      <g strokeLinecap="round" strokeLinejoin="round" fill="none">
        {LOVE.map(({ strokes, closed }, i) => {
          const pts = strokes.map((s) => s.map(([x, y]): Pt => [x0 + i * gap + x, y0 + y]));
          const bulbs = pts.flatMap((s, k) => along(s, 0.1, closed).slice(k > 0 ? 1 : 0));
          return (
            <g key={i}>
              {night && pts.map((s, k) => <path key={`h${k}`} d={polyline(s)} stroke="#FFC24D" strokeWidth={0.26} opacity={0.16} />)}
              {pts.map((s, k) => (
                <path key={`e${k}`} d={polyline(s)} stroke={edge} strokeWidth={0.125} />
              ))}
              {pts.map((s, k) => (
                <path key={`b${k}`} d={polyline(s)} stroke={body} strokeWidth={0.095} />
              ))}
              {bulbs.map(([x, y], k) => (
                <g key={`l${k}`}>
                  {night && <circle cx={x} cy={y} r={0.05} fill="#FFD36B" opacity={0.45} />}
                  <circle cx={x} cy={y} r={0.022} fill={night ? "#FFFDF0" : "#FFF7DE"} stroke={night ? "#FFC24D" : "#BFA06A"} strokeWidth={0.007} />
                </g>
              ))}
            </g>
          );
        })}
      </g>
    );
  },

  wall_cuckoo: (u, { now }) => {
    const cx = u + 0.5;
    const cone = (x: number, y: number) => (
      <g key={x}>
        <line x1={x} y1={1.3} x2={x} y2={y} stroke="#8A7A5A" strokeWidth={0.012} strokeDasharray="0.022 0.012" />
        <rect x={x - 0.025} y={y - 0.005} width={0.05} height={0.03} rx={0.01} fill="#6B4A33" />
        <ellipse cx={x} cy={y + 0.13} rx={0.05} ry={0.11} fill="#8A5A35" stroke="#5E3B22" strokeWidth={0.01} />
        <path d={[0.06, 0.11, 0.16, 0.21].map((dy) => `M ${x - 0.035} ${y + dy} l 0.0175 0.02 l 0.0175 -0.02 l 0.0175 0.02 l 0.0175 -0.02`).join(" ")} fill="none" stroke="#5E3B22" strokeWidth={0.008} />
      </g>
    );
    const out = now.getMinutes() === 0;
    return (
      <g>
        {[cone(cx - 0.11, 1.74), cone(cx + 0.11, 1.6)]}
        <line x1={cx} y1={1.3} x2={cx} y2={1.62} stroke="#B48A3C" strokeWidth={0.014} />
        <circle cx={cx} cy={1.66} r={0.055} fill="#D9AE4E" stroke="#9C7A2E" strokeWidth={0.01} />
        <rect x={cx - 0.21} y={0.8} width={0.42} height={0.52} fill={C.wood} stroke={C.woodDark} strokeWidth={0.012} />
        <path d={`M ${cx - 0.21} 1.32 Q ${cx} 1.42 ${cx + 0.21} 1.32 Z`} fill={C.woodDark} />
        <path d={`M ${cx - 0.3} 0.9 L ${cx} 0.58 L ${cx + 0.3} 0.9`} fill="none" stroke={C.woodDark} strokeWidth={0.07} strokeLinejoin="round" strokeLinecap="round" />
        <path d={`M ${cx - 0.07} 0.55 Q ${cx - 0.05} 0.47 ${cx} 0.5 Q ${cx + 0.05} 0.47 ${cx + 0.07} 0.55 Z`} fill={C.leafDark} />
        <path d={`M ${cx - 0.045} 0.88 L ${cx - 0.045} 0.79 Q ${cx} 0.73 ${cx + 0.045} 0.79 L ${cx + 0.045} 0.88 Z`} fill={out ? "#2E2420" : "#6B4630"} stroke={C.woodDark} strokeWidth={0.008} />
        {out && (
          <g>
            <ellipse cx={cx + 0.02} cy={0.84} rx={0.06} ry={0.035} fill="#F2C94C" />
            <circle cx={cx + 0.06} cy={0.82} r={0.026} fill="#F2C94C" />
            <path d={`M ${cx + 0.083} 0.815 l 0.03 0.008 l -0.03 0.008 Z`} fill="#E8833A" />
          </g>
        )}
        <circle cx={cx} cy={1.08} r={0.14} fill={C.linen} stroke={C.woodDark} strokeWidth={0.016} />
        {[0, 90, 180, 270].map((a) => {
          const [x, y] = around(cx, 1.08, a, 0.105);
          return <circle key={a} cx={x} cy={y} r={0.013} fill={C.ink} />;
        })}
        {hands(cx, 1.08, now, 0.14)}
        <circle cx={cx} cy={1.08} r={0.016} fill={C.blush} />
      </g>
    );
  },
};
