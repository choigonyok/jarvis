// 가구 그림(더한 물건). 돌리지 않은 방향의 모형 좌표에 도형을 쌓는다: x 는 오른쪽 아래, y 는 왼쪽 아래, z 는 위.
// 물건의 등은 y = 0, 앞은 +y 쪽이다(src/room/model.tsx).

import type { ReactNode } from "react";
import { C, legs, mixHex, shade, type Art, type Build, type P3 } from "../kit";

type B6 = [number, number, number, number, number, number];

/** 바닥 원 반지름 1 칸의 화면 가로 반지름(px) */
const K = Math.SQRT2 * 28;
const IRON = "#4A4F55";
const STEEL = "#A9B0B6";
const SPINES = ["#D9665B", "#E7B74A", "#5B8FB0", "#6E9E58", "#B07BB8", "#F3EFE7", "#4FA79C", "#E59A7B"];

// ── 도우미 ───────────────────────────────────────────────────────

const boundsOf = (list: P3[]): B6 => {
  const k = (i: number, f: (...n: number[]) => number) => f(...list.map((p) => p[i]));
  return [k(0, Math.min), k(1, Math.min), k(2, Math.min), k(0, Math.max), k(1, Math.max), k(2, Math.max)];
};
const lerp = (p: P3, q: P3, t: number): P3 => [p[0] + (q[0] - p[0]) * t, p[1] + (q[1] - p[1]) * t, p[2] + (q[2] - p[2]) * t];

/** 둥근 물건 아래의 옅은 그림자 */
function roundShadow(a: Art, cx: number, cy: number, r: number) {
  a.add([cx - r, cy - r, 0, cx + r, cy + r, 0], (v) => {
    const [x, y] = v.p(cx, cy);
    return <ellipse cx={x} cy={y} rx={r * K} ry={(r * K) / 2} fill="rgba(40,30,20,0.16)" />;
  });
}

/** 3D 점을 잇는 막대(흔들의자 발, 그네 팔) */
function curve(a: Art, list: P3[], stroke: string, width = 2.4) {
  a.add(boundsOf(list), (v) => (
    <polyline points={v.pts(list)} fill="none" stroke={stroke} strokeWidth={width} strokeLinecap="round" strokeLinejoin="round" />
  ));
}

/** 얇은 천이나 판(옷, 거울): axis 면(at) 위에 (u, z) 로 그리고 앞뒤 어느 쪽에서도 보인다. front 는 +쪽이 보이는지. */
function card(a: Art, axis: "x" | "y", at: number, u: [number, number], z: [number, number], draw: (front: boolean) => ReactNode) {
  const b: B6 = axis === "y" ? [u[0], at, z[0], u[1], at, z[1]] : [at, u[0], z[0], at, u[1], z[1]];
  a.add(b, (v) => <g transform={v.faceT(axis, at)}>{draw(v.sees(axis === "x" ? 1 : 0, axis === "y" ? 1 : 0))}</g>);
}

/** 윗면의 점무늬 */
function speckle(a: Art, x0: number, y0: number, x1: number, y1: number, z: number, fill: string, step = 0.24) {
  a.add([x0, y0, z, x1, y1, z], (v) => {
    const out: ReactNode[] = [];
    for (let i = 0, x = x0 + step / 2; x < x1; i++, x += step)
      for (let y = y0 + ((i % 2) + 1) * (step / 2); y < y1; y += step) {
        const [sx, sy] = v.p(x, y, z);
        out.push(<ellipse key={`${i}-${y.toFixed(2)}`} cx={sx} cy={sy} rx={1.7} ry={0.85} fill={fill} />);
      }
    return <g>{out}</g>;
  });
}

/** 작은 화분 */
function sprig(a: Art, x: number, y: number, z: number, pot: string = C.white) {
  a.cyl({ cx: x, cy: y, z, r: 0.07, h: 0.1, c: pot, top: C.soil, taper: -0.015 });
  a.dot(
    x,
    y,
    z + 0.1,
    (sx, sy) => (
      <g fill={C.leaf} stroke={C.leafDark} strokeWidth={0.5}>
        <ellipse cx={sx - 3.2} cy={sy - 3.4} rx={3.4} ry={1.7} transform={`rotate(-35 ${sx - 3.2} ${sy - 3.4})`} />
        <ellipse cx={sx + 3.2} cy={sy - 4.2} rx={3.4} ry={1.7} transform={`rotate(35 ${sx + 3.2} ${sy - 4.2})`} />
        <ellipse cx={sx} cy={sy - 6.2} rx={1.7} ry={3.6} />
      </g>
    ),
    0.08,
    0.3,
  );
}

/** 손잡이 달린 컵(s 배) */
function mug(a: Art, x: number, y: number, z: number, c: string, s = 1, drink = "#8A5A3C") {
  const r = 0.05 * s;
  const h = 0.09 * s;
  a.cyl({ cx: x, cy: y, z, r, h, c, top: drink });
  a.add([x + r, y - 0.01, z + h * 0.2, x + r + 0.04 * s, y + 0.01, z + h * 0.8], (v) => {
    const [sx, sy] = v.p(x + r + 0.018 * s, y, z + h / 2);
    return <ellipse cx={sx} cy={sy} rx={2 * s} ry={2.4 * s} fill="none" stroke={shade(c, -0.3)} strokeWidth={1.1} />;
  });
}

/** 귤 하나(화면 좌표) */
const orange = (sx: number, sy: number, leaf = false) => (
  <>
    <circle cx={sx} cy={sy - 3} r={4} fill="#F29A3F" stroke={shade("#F29A3F", -0.3)} strokeWidth={0.6} />
    {leaf && <ellipse cx={sx + 1.5} cy={sy - 6.6} rx={1.8} ry={0.9} fill={C.leaf} />}
  </>
);

/** 바닥까지 퍼진 천(이불 자락): 바닥은 [x0, x1]×[y0, y1], 높이 h 의 윗변은 k 만큼 안쪽. 아랫단은 hem 색. */
function drape(a: Art, x0: number, y0: number, x1: number, y1: number, k: number, h: number, c: string, hem: string) {
  a.add([x0, y0, 0, x1, y1, h], (v) => {
    const lo: P3[] = [[x0, y0, 0], [x1, y0, 0], [x1, y1, 0], [x0, y1, 0]];
    const hi: P3[] = [[x0 + k, y0 + k, h], [x1 - k, y0 + k, h], [x1 - k, y1 - k, h], [x0 + k, y1 - k, h]];
    const [ox] = v.at(0, 0);
    const sides = (
      [
        [0, -1],
        [1, 0],
        [0, 1],
        [-1, 0],
      ] as const
    ).map(([dx, dy], i) => {
      if (!v.sees(dx, dy)) return null;
      const j = (i + 1) % 4;
      const dark = v.at(dx, dy)[0] - ox > 0.5;
      const tone = (col: string) => (dark ? shade(col, -0.16) : col);
      return (
        <g key={i}>
          <polygon points={v.pts([lo[i], lo[j], hi[j], hi[i]])} fill={tone(c)} />
          <polygon points={v.pts([lo[i], lo[j], lerp(lo[j], hi[j], 0.24), lerp(lo[i], hi[i], 0.24)])} fill={tone(hem)} />
          {[0.2, 0.45, 0.7].map((t) => (
            <polyline key={t} points={v.pts([lerp(hi[i], hi[j], t), lerp(lo[i], lo[j], t + 0.04)])} fill="none" stroke={shade(c, -0.22)} />
          ))}
        </g>
      );
    });
    return (
      <g stroke={shade(c, -0.32)} strokeWidth={0.6} strokeLinejoin="round">
        {sides}
        <polygon points={v.pts(hi)} fill={shade(c, 0.14)} />
      </g>
    );
  });
}

/** 책등 한 줄(면 좌표): [x0, x1] 을 채우고 바닥은 z0, 칸 높이는 h. seed 로 색과 키를 바꾼다. */
function spines(x0: number, x1: number, z0: number, h: number, seed: number): ReactNode[] {
  const out: ReactNode[] = [];
  for (let i = 0, x = x0; ; i++) {
    const w = 0.06 + ((i * 7 + seed * 5) % 4) * 0.012;
    if (x + w > x1 + 1e-6) break;
    const t = h * (0.62 + ((i * 5 + seed * 3) % 5) * 0.07);
    out.push(<rect key={`${seed}-${i}`} x={x} y={z0} width={w} height={t} fill={SPINES[(i * 3 + seed) % SPINES.length]} stroke="rgba(0,0,0,.25)" strokeWidth={0.01} />);
    x += w + 0.004;
  }
  return out;
}

/** 서랍 앞판(면 좌표): 넓으면 손잡이 둘 */
function drawerFront(x0: number, x1: number, z0: number, z1: number, c: string, knob: string, bar = false) {
  const w = x1 - x0;
  const cz = (z0 + z1) / 2;
  const at = w > 0.9 ? [x0 + w * 0.25, x1 - w * 0.25] : [(x0 + x1) / 2];
  return (
    <g key={`${x0}-${z0}`}>
      <rect x={x0} y={z0} width={w} height={z1 - z0} fill={shade(c, 0.08)} stroke={shade(c, -0.25)} strokeWidth={0.015} />
      {at.map((kx) => (bar ? <rect key={kx} x={kx - 0.08} y={cz - 0.015} width={0.16} height={0.03} rx={0.01} fill={knob} /> : <circle key={kx} cx={kx} cy={cz} r={0.028} fill={knob} />))}
    </g>
  );
}

/** 면 위의 작은 화분(면 좌표): 바닥 가운데 (x, z) */
const potFace = (x: number, z: number) => (
  <g>
    <ellipse cx={x - 0.06} cy={z + 0.2} rx={0.08} ry={0.04} transform={`rotate(25 ${x - 0.06} ${z + 0.2})`} fill={C.leaf} />
    <ellipse cx={x + 0.06} cy={z + 0.22} rx={0.08} ry={0.04} transform={`rotate(-25 ${x + 0.06} ${z + 0.22})`} fill={C.leafDark} />
    <ellipse cx={x} cy={z + 0.26} rx={0.04} ry={0.09} fill={C.leaf} />
    <path d={`M ${x - 0.07} ${z} L ${x + 0.07} ${z} L ${x + 0.09} ${z + 0.13} L ${x - 0.09} ${z + 0.13} Z`} fill="#CF8A63" />
  </g>
);

/** 면 위의 작은 액자(면 좌표) */
const frameFace = (x: number, z: number, w: number, h: number, sky: string = "#CFE8F2") => (
  <g>
    <rect x={x} y={z} width={w} height={h} fill={C.woodDark} />
    <rect x={x + 0.025} y={z + 0.025} width={w - 0.05} height={h - 0.05} fill={sky} />
    <path d={`M ${x + 0.025} ${z + 0.025} L ${x + w * 0.4} ${z + h * 0.5} L ${x + w * 0.6} ${z + h * 0.32} L ${x + w - 0.025} ${z + h * 0.55} L ${x + w - 0.025} ${z + 0.025} Z`} fill={C.leaf} />
    <circle cx={x + w * 0.7} cy={z + h * 0.74} r={Math.min(w, h) * 0.1} fill="#F5C66A" />
  </g>
);

// ── 침대·요 ──────────────────────────────────────────────────────

const bedSingle: Build = (a) => {
  a.shadow();
  a.box({ w: 1, d: 0.14, h: 1, c: C.wood });
  a.face("y", 0.14, { u: [0.12, 0.88], z: [0.64, 0.9] }, <rect x={0.12} y={0.64} width={0.76} height={0.26} rx={0.05} fill={shade(C.wood, 0.1)} stroke={shade(C.wood, -0.25)} strokeWidth={0.02} />);
  a.box({ y: 0.14, w: 1, d: 1.86, h: 0.3, c: C.woodLight });
  // 요는 머리 쪽만: 발치는 담요가 덮는다
  a.box({ x: 0.05, y: 0.18, z: 0.3, w: 0.9, d: 0.56, h: 0.15, c: C.linen });
  a.box({ x: 0.17, y: 0.24, z: 0.45, w: 0.66, d: 0.34, h: 0.13, c: C.white });
  a.box({ x: 0.03, y: 0.74, z: 0.3, w: 0.94, d: 1.24, h: 0.22, c: C.straw });
  a.box({ x: 0.03, y: 0.74, z: 0.52, w: 0.94, d: 0.16, h: 0.02, c: C.white, edge: false });
  speckle(a, 0.03, 0.92, 0.97, 1.98, 0.52, C.white);
};

const bedBunk: Build = (a) => {
  a.shadow();
  legs(a, 0, 0, 0.92, 1.92, 2.3, C.wood, 0.08);
  for (const [z, quilt] of [
    [0.1, C.egg],
    [1.36, C.blush],
  ] as const) {
    const up = z > 1;
    a.box({ x: 0.08, y: 0.02, z, w: 0.84, d: 0.04, h: 0.62, c: C.woodLight });
    a.box({ x: 0.08, y: 0.06, z, w: 0.84, d: 1.88, h: 0.18, c: C.woodLight });
    a.box({ x: 0.12, y: 0.06, z: z + 0.18, w: 0.76, d: 0.62, h: 0.12, c: C.linen });
    a.box({ x: 0.22, y: 0.12, z: z + 0.3, w: 0.56, d: 0.3, h: 0.1, c: C.white });
    a.box({ x: up ? 0.12 : 0.1, y: 0.68, z: z + 0.18, w: up ? 0.76 : 0.8, d: 1.24, h: 0.16, c: quilt });
  }
  // 위층 난간
  for (const x of [0.08, 0.88]) {
    a.box({ x, y: 0.06, z: 1.9, w: 0.04, d: 1.88, h: 0.06, c: C.wood });
    for (const y of [0.5, 0.98, 1.46]) a.box({ x, y, z: 1.54, w: 0.04, d: 0.04, h: 0.36, c: C.wood });
  }
  // 발치 사다리
  for (const x of [0.28, 0.67]) a.box({ x, y: 1.94, w: 0.05, d: 0.05, h: 1.9, c: C.wood });
  for (const z of [0.52, 0.88, 1.24, 1.6]) a.box({ x: 0.33, y: 1.95, z, w: 0.34, d: 0.03, h: 0.04, c: C.woodLight });
  a.box({ x: 0.08, y: 1.94, z: 1.9, w: 0.84, d: 0.05, h: 0.06, c: C.wood });
};

const futon: Build = (a) => {
  const quilt = "#EBA39B";
  a.shadow({ x: 0.02, y: 0.02, w: 1.96, d: 1.96 });
  a.box({ x: 0.12, y: 0.06, w: 1.76, d: 0.78, h: 0.12, c: "#F6EEE2" });
  a.box({ x: 0.26, y: 0.14, z: 0.12, w: 0.6, d: 0.32, h: 0.12, c: "#CFE3EA" });
  a.box({ x: 1.14, y: 0.14, z: 0.12, w: 0.6, d: 0.32, h: 0.12, c: "#F6DCA8" });
  a.box({ x: 0.06, y: 0.84, w: 1.88, d: 1.12, h: 0.2, c: quilt });
  a.box({ x: 0.06, y: 0.84, z: 0.2, w: 1.88, d: 0.3, h: 0.06, c: mixHex(quilt, C.white, 0.6) });
  speckle(a, 0.06, 1.16, 1.94, 1.96, 0.2, C.white, 0.22);
};

// ── 소파·의자 ────────────────────────────────────────────────────

const sofaCorner: Build = (a) => {
  const body = "#8FA6BC";
  const seat = "#B9C9D8";
  a.add([0, 0, 0, 2, 2, 0], (v) => (
    <polygon points={v.pts([[0.04, 0.04, 0], [1.98, 0.04, 0], [1.98, 1.02, 0], [1.02, 1.02, 0], [1.02, 1.98, 0], [0.04, 1.98, 0]])} fill="rgba(40,30,20,0.16)" />
  ));
  a.box({ w: 2, d: 0.3, h: 0.85, c: body });
  a.box({ y: 0.3, w: 0.3, d: 1.7, h: 0.85, c: body });
  a.box({ x: 0.3, y: 0.3, w: 1.5, d: 0.7, h: 0.3, c: shade(body, -0.08) });
  a.box({ x: 0.3, y: 1, w: 0.7, d: 0.8, h: 0.3, c: shade(body, -0.08) });
  a.box({ x: 1.8, y: 0.3, w: 0.2, d: 0.7, h: 0.55, c: body });
  a.box({ x: 0.3, y: 1.8, w: 0.7, d: 0.2, h: 0.55, c: body });
  a.box({ x: 0.33, y: 0.33, z: 0.3, w: 0.64, d: 0.64, h: 0.14, c: seat });
  a.box({ x: 1.01, y: 0.33, z: 0.3, w: 0.76, d: 0.64, h: 0.14, c: seat });
  a.box({ x: 0.33, y: 1.01, z: 0.3, w: 0.64, d: 0.76, h: 0.14, c: seat });
  a.box({ x: 1.32, y: 0.31, z: 0.44, w: 0.3, d: 0.12, h: 0.28, c: C.straw });
  a.box({ x: 0.31, y: 1.34, z: 0.44, w: 0.12, d: 0.3, h: 0.28, c: C.blush });
};

const armchair: Build = (a) => {
  const c = "#E39A86";
  a.shadow({ x: 0.08, y: 0.06, w: 0.86, d: 0.88 });
  legs(a, 0.16, 0.14, 0.78, 0.82, 0.1, C.woodDark, 0.06);
  a.box({ x: 0.12, y: 0.1, z: 0.1, w: 0.76, d: 0.26, h: 0.85, c });
  a.box({ x: 0.12, y: 0.36, z: 0.1, w: 0.18, d: 0.56, h: 0.5, c });
  a.box({ x: 0.7, y: 0.36, z: 0.1, w: 0.18, d: 0.56, h: 0.5, c });
  a.box({ x: 0.3, y: 0.36, z: 0.1, w: 0.4, d: 0.56, h: 0.22, c: shade(c, -0.08) });
  a.box({ x: 0.31, y: 0.37, z: 0.32, w: 0.38, d: 0.54, h: 0.13, c: mixHex(c, C.white, 0.35) });
  a.box({ x: 0.36, y: 0.37, z: 0.45, w: 0.28, d: 0.1, h: 0.26, c: C.straw });
};

const beanbag: Build = (a) => {
  const c = "#E9A06A";
  const line = shade(c, -0.32);
  roundShadow(a, 0.5, 0.55, 0.42);
  // 앉는 자리(앞)와 등 둔덕(뒤)을 한 덩어리로: 테두리를 먼저 그리고 면을 덮어 안쪽 선을 지운다
  a.add([0.08, 0.06, 0, 0.92, 0.98, 0.62], (v) => {
    const front = v.sees(0, 1);
    const ell = (x: number, y: number, z: number, r: number, h: number) => {
      const [cx, cy] = v.p(x, y, z);
      const rx = r * K;
      return { cx, cy, rx, ry: Math.hypot(rx / 2, h * 28) };
    };
    const seat = ell(0.5, 0.6, 0.16, 0.41, 0.16);
    const hump = ell(0.5, 0.33, 0.32, 0.34, 0.26);
    const near = front ? seat : hump;
    const arc = (e: typeof seat, a0: number, a1: number) => {
      const pt = (t: number) => `${e.cx + e.rx * Math.cos((t * Math.PI) / 180)} ${e.cy + e.ry * Math.sin((t * Math.PI) / 180)}`;
      return `M ${pt(a0)} A ${e.rx} ${e.ry} 0 0 1 ${pt(a1)}`;
    };
    return (
      <g>
        {[hump, seat].map((e, i) => (
          <ellipse key={i} {...e} fill={c} stroke={line} strokeWidth={1.3} />
        ))}
        {[hump, seat].map((e, i) => (
          <ellipse key={i} {...e} fill={c} />
        ))}
        <ellipse cx={hump.cx - hump.rx * 0.25} cy={hump.cy - hump.ry * 0.42} rx={hump.rx * 0.5} ry={hump.ry * 0.3} fill={shade(c, 0.18)} />
        {front ? (
          <>
            <path d={arc(seat, 196, 344)} fill="none" stroke={shade(c, -0.2)} strokeWidth={0.8} />
            <ellipse cx={seat.cx} cy={seat.cy - seat.ry * 0.2} rx={seat.rx * 0.55} ry={seat.ry * 0.3} fill={shade(c, -0.08)} />
          </>
        ) : (
          <path d={arc(near, 18, 162)} fill="none" stroke={shade(c, -0.2)} strokeWidth={0.8} />
        )}
        <ellipse cx={near.cx - near.rx * 0.3} cy={near.cy + near.ry * 0.35} rx={near.rx * 0.32} ry={near.ry * 0.16} fill={shade(c, 0.12)} />
      </g>
    );
  });
};

const rockingChair: Build = (a) => {
  a.shadow({ x: 0.12, y: 0.04, w: 0.76, d: 0.92 });
  for (const x of [0.18, 0.82])
    curve(
      a,
      Array.from({ length: 9 }, (_, i): P3 => {
        const y = 0.04 + i * 0.115;
        return [x, y, 0.16 * ((y - 0.5) / 0.46) ** 2];
      }),
      C.woodDark,
      3,
    );
  for (const x of [0.15, 0.79]) {
    a.box({ x, y: 0.22, z: 0.02, w: 0.06, d: 0.06, h: 0.44, c: C.wood });
    a.box({ x, y: 0.72, z: 0.02, w: 0.06, d: 0.06, h: 0.7, c: C.wood });
  }
  a.box({ x: 0.21, y: 0.2, z: 0.4, w: 0.58, d: 0.62, h: 0.06, c: C.wood });
  a.box({ x: 0.25, y: 0.26, z: 0.46, w: 0.5, d: 0.52, h: 0.05, c: C.blush });
  // 뒤로 기운 등받이
  for (const x of [0.18, 0.82]) a.line([x, 0.25, 0.46], [x, 0.08, 1.06], C.woodDark, 3);
  for (const x of [0.36, 0.5, 0.64]) a.line([x, 0.24, 0.51], [x, 0.1, 0.98], C.wood, 2.4);
  a.add([0.16, 0.07, 0.94, 0.84, 0.11, 1.1], (v) => (
    <polygon points={v.pts([[0.16, 0.11, 0.94], [0.84, 0.11, 0.94], [0.84, 0.07, 1.1], [0.16, 0.07, 1.1]])} fill={C.wood} stroke={shade(C.wood, -0.32)} strokeWidth={0.6} />
  ));
  for (const x of [0.14, 0.78]) a.box({ x, y: 0.2, z: 0.72, w: 0.08, d: 0.62, h: 0.05, c: C.wood });
};

const stool: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.3);
  const corners = [
    [-1, -1],
    [1, -1],
    [-1, 1],
    [1, 1],
  ];
  for (const [dx, dy] of corners) a.line([0.5 + dx * 0.13, 0.5 + dy * 0.13, 0.4], [0.5 + dx * 0.2, 0.5 + dy * 0.2, 0], C.woodDark, 2.6);
  const s = 0.174;
  for (const [p, q] of [
    [[-s, -s], [s, -s]],
    [[-s, -s], [-s, s]],
    [[s, -s], [s, s]],
    [[-s, s], [s, s]],
  ])
    a.line([0.5 + p[0], 0.5 + p[1], 0.15], [0.5 + q[0], 0.5 + q[1], 0.15], C.woodDark, 1.6);
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.4, r: 0.26, h: 0.08, c: C.wood, top: C.woodLight });
};

const bench: Build = (a) => {
  a.shadow({ x: 0.04, y: 0.16, w: 1.92, d: 0.68 });
  const arch = <path d="M 0.36 0 L 0.36 0.1 Q 0.5 0.22 0.64 0.1 L 0.64 0 Z" fill={shade(C.woodDark, -0.35)} />;
  for (const x of [0.18, 1.72]) {
    a.box({ x, y: 0.24, w: 0.1, d: 0.52, h: 0.42, c: C.woodDark });
    a.face("x", x + 0.1, { u: [0.36, 0.64], z: [0, 0.2] }, arch);
    a.face("x", x, { u: [0.36, 0.64], z: [0, 0.2], out: -1 }, arch);
  }
  a.box({ x: 0.28, y: 0.47, z: 0.24, w: 1.44, d: 0.06, h: 0.06, c: C.woodDark });
  for (const y of [0.2, 0.41, 0.62]) a.box({ x: 0.04, y, z: 0.42, w: 1.92, d: 0.18, h: 0.07, c: C.wood });
  a.box({ x: 1.32, y: 0.26, z: 0.49, w: 0.5, d: 0.48, h: 0.07, c: C.egg });
};

const gamingChair: Build = (a) => {
  const ink = "#34393E";
  const accent = C.blush;
  roundShadow(a, 0.5, 0.55, 0.38);
  for (let i = 0; i < 5; i++) {
    const t = Math.PI / 2 + (i * 2 * Math.PI) / 5;
    const [ex, ey] = [0.5 + Math.cos(t) * 0.33, 0.55 + Math.sin(t) * 0.33];
    a.dot(ex, ey, 0, (sx, sy) => <circle cx={sx} cy={sy - 1.6} r={1.7} fill={C.ink} />, 0.03, 0.05);
    a.line([0.5, 0.55, 0.1], [ex, ey, 0.06], IRON, 2.6);
  }
  a.cyl({ cx: 0.5, cy: 0.55, z: 0.1, r: 0.035, h: 0.28, c: STEEL });
  a.box({ x: 0.28, y: 0.32, z: 0.38, w: 0.44, d: 0.56, h: 0.12, c: ink });
  for (const x of [0.2, 0.72]) a.box({ x, y: 0.32, z: 0.38, w: 0.08, d: 0.56, h: 0.15, c: accent });
  a.box({ x: 0.27, y: 0.14, z: 0.46, w: 0.46, d: 0.14, h: 0.99, c: ink });
  for (const x of [0.19, 0.73]) a.box({ x, y: 0.12, z: 0.46, w: 0.08, d: 0.18, h: 0.95, c: accent });
  a.box({ x: 0.36, y: 0.28, z: 1.16, w: 0.28, d: 0.06, h: 0.14, c: accent });
  a.box({ x: 0.34, y: 0.28, z: 0.56, w: 0.32, d: 0.07, h: 0.16, c: shade(ink, 0.18) });
  for (const x of [0.12, 0.82]) {
    a.box({ x, y: 0.52, z: 0.4, w: 0.06, d: 0.08, h: 0.32, c: IRON });
    a.box({ x: x - 0.01, y: 0.4, z: 0.72, w: 0.08, d: 0.34, h: 0.05, c: ink });
  }
};

// ── 탁자 ─────────────────────────────────────────────────────────

const tableRound: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.42);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.2, h: 0.04, c: IRON });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.04, r: 0.035, h: 0.64, c: IRON });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.68, r: 0.4, h: 0.05, c: C.woodLight });
  mug(a, 0.37, 0.6, 0.73, C.white, 1.3);
  a.cyl({ cx: 0.62, cy: 0.38, z: 0.73, r: 0.06, h: 0.16, c: C.egg, taper: 0.018 });
  a.dot(
    0.62,
    0.38,
    0.89,
    (sx, sy) => (
      <>
        <path d={`M ${sx} ${sy} q 1.4 -5 0 -10 M ${sx} ${sy - 4} q -3 -1 -4 -4`} stroke={C.leafDark} strokeWidth={1.1} fill="none" />
        <circle cx={sx} cy={sy - 11.5} r={3.4} fill={C.blush} stroke={shade(C.blush, -0.3)} strokeWidth={0.6} />
        <circle cx={sx} cy={sy - 11.5} r={1.3} fill={C.straw} />
      </>
    ),
    0.05,
    0.5,
  );
};

const tableCoffee: Build = (a) => {
  a.shadow({ x: 0.04, y: 0.1, w: 1.92, d: 0.82 });
  legs(a, 0.12, 0.18, 1.81, 0.75, 0.38, C.woodDark);
  a.box({ x: 0.19, y: 0.2, z: 0.1, w: 1.62, d: 0.6, h: 0.04, c: C.wood });
  a.box({ x: 0.36, y: 0.32, z: 0.14, w: 0.38, d: 0.3, h: 0.025, c: C.blush });
  a.box({ x: 0.42, y: 0.37, z: 0.165, w: 0.36, d: 0.28, h: 0.025, c: C.egg });
  a.box({ x: 1.2, y: 0.3, z: 0.14, w: 0.34, d: 0.36, h: 0.12, c: "#C9A26B" });
  a.box({ x: 0.06, y: 0.12, z: 0.38, w: 1.88, d: 0.78, h: 0.07, c: C.woodLight });
  a.box({ x: 1.18, y: 0.36, z: 0.45, w: 0.08, d: 0.24, h: 0.03, c: C.ink });
  a.dot(1.22, 0.41, 0.48, (sx, sy) => <circle cx={sx} cy={sy} r={0.9} fill="#E5484D" />, 0.01, 0.01);
  mug(a, 0.62, 0.5, 0.45, C.straw);
};

const tableSide: Build = (a) => {
  a.shadow({ x: 0.12, y: 0.12, w: 0.76, d: 0.76 });
  legs(a, 0.2, 0.2, 0.74, 0.74, 0.06, C.woodDark, 0.06);
  a.box({ x: 0.18, y: 0.18, z: 0.06, w: 0.64, d: 0.64, h: 0.42, c: C.woodLight });
  a.face(
    "y",
    0.82,
    { u: [0.22, 0.78], z: [0.1, 0.44] },
    <>
      <rect x={0.22} y={0.1} width={0.56} height={0.16} fill={shade(C.woodLight, -0.45)} />
      <rect x={0.26} y={0.1} width={0.05} height={0.13} fill={C.egg} />
      <rect x={0.31} y={0.1} width={0.04} height={0.12} fill={C.blush} />
      <rect x={0.42} y={0.1} width={0.3} height={0.04} fill={C.straw} />
      {drawerFront(0.22, 0.78, 0.3, 0.44, C.woodLight, C.woodDark)}
    </>,
  );
  a.box({ x: 0.15, y: 0.15, z: 0.48, w: 0.7, d: 0.7, h: 0.04, c: C.wood });
  sprig(a, 0.36, 0.4, 0.52);
  mug(a, 0.6, 0.62, 0.52, C.blush);
};

const tableFloor: Build = (a) => {
  a.shadow({ x: 0.06, y: 0.1, w: 1.88, d: 0.82 });
  legs(a, 0.16, 0.2, 1.77, 0.73, 0.25, C.woodDark, 0.07);
  a.box({ x: 0.08, y: 0.12, z: 0.25, w: 1.84, d: 0.76, h: 0.07, c: "#B97A4E" });
  for (const [x, y] of [
    [0.5, 0.52],
    [1.36, 0.48],
  ]) {
    a.cyl({ cx: x, cy: y, z: 0.32, r: 0.08, h: 0.06, c: "#DCE6EA", top: C.white, taper: -0.02 });
    a.dot(x, y, 0.38, (sx, sy) => <ellipse cx={sx} cy={sy - 0.8} rx={3.4} ry={2.2} fill={C.white} stroke="#D6D2CA" strokeWidth={0.4} />, 0.06, 0.04);
    // 숟가락과 젓가락
    a.line([x + 0.15, y - 0.08, 0.32], [x + 0.15, y + 0.2, 0.32], STEEL, 1.1);
    a.dot(x + 0.15, y - 0.12, 0.32, (sx, sy) => <ellipse cx={sx} cy={sy} rx={1.8} ry={1.1} fill={STEEL} />, 0.03, 0.005);
    a.line([x + 0.2, y - 0.16, 0.32], [x + 0.2, y + 0.2, 0.32], STEEL, 0.9);
    a.line([x + 0.23, y - 0.16, 0.32], [x + 0.23, y + 0.2, 0.32], STEEL, 0.9);
  }
  a.cyl({ cx: 0.95, cy: 0.4, z: 0.32, r: 0.11, h: 0.025, c: C.white });
  a.dot(
    0.95,
    0.4,
    0.345,
    (sx, sy) => (
      <>
        <ellipse cx={sx} cy={sy - 0.6} rx={3.2} ry={1.8} fill="#D9573F" />
        <path d={`M ${sx - 2} ${sy - 1} q 2 -1 4 0`} stroke="#F2A07E" strokeWidth={0.6} fill="none" />
      </>
    ),
    0.06,
    0.03,
  );
};

const warmTable: Build = (a) => {
  a.shadow({ inset: 0.02 });
  // 이불이 상판 아래에서 바닥까지 퍼지고, 상판 둘레로 이불 윗면이 조금 보인다
  drape(a, 0.02, 0.02, 1.98, 1.98, 0.26, 0.34, "#E58F7B", "#F3D9B0");
  a.box({ x: 0.34, y: 0.34, z: 0.34, w: 1.32, d: 1.32, h: 0.08, c: C.woodLight });
  a.cyl({ cx: 1, cy: 0.98, z: 0.42, r: 0.22, h: 0.07, c: "#C9A26B", top: "#B08850", taper: -0.03 });
  for (const [x, y, z, leaf] of [
    [0.92, 0.92, 0.49, 0],
    [1.08, 0.98, 0.49, 1],
    [0.96, 1.1, 0.49, 0],
    [1.0, 0.96, 0.55, 1],
  ])
    a.dot(x, y, z, (sx, sy) => orange(sx, sy, leaf === 1), 0.05, 0.05);
  mug(a, 0.58, 1.38, 0.42, C.white, 1.2, "#C98B5A");
};

// ── 책상·화장대 ──────────────────────────────────────────────────

const deskComputer: Build = (a) => {
  const panel = "#E9E4DC";
  a.shadow({ y: 0.04, d: 0.94 });
  for (const x of [0.06, 1.86]) a.box({ x, y: 0.1, w: 0.08, d: 0.82, h: 0.7, c: panel });
  a.box({ x: 0.14, y: 0.1, z: 0.32, w: 1.72, d: 0.04, h: 0.38, c: panel });
  a.box({ x: 1.5, y: 0.3, w: 0.3, d: 0.56, h: 0.52, c: C.ink });
  a.face(
    "y",
    0.86,
    { u: [1.54, 1.76], z: [0.06, 0.48] },
    <>
      {[0.12, 0.17, 0.22, 0.27].map((z) => (
        <rect key={z} x={1.56} y={z} width={0.18} height={0.018} fill="#4B535A" />
      ))}
      <circle cx={1.65} cy={0.42} r={0.03} fill={C.egg} />
    </>,
  );
  a.box({ x: 0.02, y: 0.06, z: 0.7, w: 1.96, d: 0.9, h: 0.06, c: C.woodLight });
  for (const cx of [0.56, 1.44]) {
    a.box({ x: cx - 0.12, y: 0.16, z: 0.76, w: 0.24, d: 0.16, h: 0.02, c: IRON });
    a.box({ x: cx - 0.03, y: 0.14, z: 0.78, w: 0.06, d: 0.06, h: 0.2, c: IRON });
    a.box({ x: cx - 0.4, y: 0.2, z: 0.9, w: 0.8, d: 0.05, h: 0.45, c: C.ink });
  }
  a.face(
    "y",
    0.25,
    { u: [0.19, 0.93], z: [0.93, 1.32] },
    <>
      <rect x={0.19} y={0.93} width={0.74} height={0.39} fill="#9ED3EA" />
      <path d="M 0.19 0.93 L 0.19 1.04 Q 0.38 1.16 0.56 1.06 T 0.93 1.08 L 0.93 0.93 Z" fill="#7DBB6A" />
      <circle cx={0.8} cy={1.22} r={0.045} fill="#F5D36A" />
      <rect x={0.42} y={1.07} width={0.05} height={0.08} fill={C.blush} />
    </>,
  );
  a.face(
    "y",
    0.25,
    { u: [1.07, 1.81], z: [0.93, 1.32] },
    <>
      <rect x={1.07} y={0.93} width={0.74} height={0.39} fill="#26313B" />
      {[C.egg, C.straw, C.blush, C.sky, C.egg].map((c, i) => (
        <rect key={i} x={1.12 + (i % 2) * 0.06} y={1.24 - i * 0.065} width={0.18 + ((i * 3) % 4) * 0.08} height={0.025} fill={c} />
      ))}
    </>,
  );
  a.box({ x: 0.62, y: 0.46, z: 0.76, w: 0.66, d: 0.2, h: 0.03, c: "#D8DCDF" });
  a.add([0.66, 0.5, 0.79, 1.24, 0.62, 0.79], (v) => (
    <g stroke="#AEB4B9" strokeWidth={0.5}>
      {[0.53, 0.56, 0.59].map((y) => (
        <polyline key={y} points={v.pts([[0.66, y, 0.79], [1.24, y, 0.79]])} />
      ))}
    </g>
  ));
  a.box({ x: 1.38, y: 0.42, z: 0.76, w: 0.34, d: 0.3, h: 0.006, c: C.blush, edge: false });
  a.dot(1.55, 0.57, 0.766, (sx, sy) => <ellipse cx={sx} cy={sy - 1.2} rx={2.6} ry={1.9} fill={C.white} stroke="#AEB4B9" strokeWidth={0.6} />, 0.04, 0.03);
};

const vanity: Build = (a) => {
  const body = "#F4EEE6";
  const trim = "#D8B46A";
  a.shadow({ x: 0.08, y: 0.18, w: 0.84, d: 0.72 });
  legs(a, 0.13, 0.25, 0.8, 0.78, 0.5, shade(body, -0.06), 0.07);
  a.box({ x: 0.1, y: 0.22, z: 0.5, w: 0.8, d: 0.66, h: 0.16, c: body });
  a.face("y", 0.88, { u: [0.16, 0.84], z: [0.53, 0.63] }, drawerFront(0.16, 0.84, 0.53, 0.63, body, trim, true));
  // 기울어지는 거울: 양옆 기둥 사이의 타원
  for (const x of [0.2, 0.76]) a.box({ x, y: 0.28, z: 0.66, w: 0.04, d: 0.04, h: 0.5, c: trim });
  card(a, "y", 0.3, [0.24, 0.76], [0.72, 1.56], (front) => (
    <>
      <ellipse cx={0.5} cy={1.14} rx={0.25} ry={0.4} fill={front ? "#CFE8F2" : shade(body, -0.06)} stroke={trim} strokeWidth={0.045} />
      {front && <path d="M 0.36 1.24 L 0.46 1.4 M 0.4 1.12 L 0.54 1.34" stroke="rgba(255,255,255,.85)" strokeWidth={0.025} strokeLinecap="round" />}
    </>
  ));
  a.cyl({ cx: 0.68, cy: 0.6, z: 0.66, r: 0.04, h: 0.12, c: C.blush });
  a.cyl({ cx: 0.68, cy: 0.6, z: 0.78, r: 0.018, h: 0.04, c: trim });
  a.cyl({ cx: 0.79, cy: 0.72, z: 0.66, r: 0.03, h: 0.08, c: C.straw });
  a.cyl({ cx: 0.3, cy: 0.66, z: 0.66, r: 0.05, h: 0.04, c: C.egg });
};

// ── 수납 ─────────────────────────────────────────────────────────

const dresser: Build = (a) => {
  const body = C.woodLight;
  a.shadow({ x: 0.02, y: 0.1, w: 1.96, d: 0.82 });
  legs(a, 0.1, 0.18, 1.82, 0.76, 0.06, C.woodDark, 0.08);
  a.box({ x: 0.06, y: 0.14, z: 0.06, w: 1.88, d: 0.72, h: 0.86, c: body });
  a.face(
    "y",
    0.86,
    { u: [0.1, 1.9], z: [0.1, 0.88] },
    <>
      {drawerFront(0.1, 0.98, 0.64, 0.88, body, C.woodDark)}
      {drawerFront(1.02, 1.9, 0.64, 0.88, body, C.woodDark)}
      {drawerFront(0.1, 1.9, 0.37, 0.61, body, C.woodDark)}
      {drawerFront(0.1, 1.9, 0.1, 0.34, body, C.woodDark)}
    </>,
  );
  a.box({ x: 0.03, y: 0.11, z: 0.92, w: 1.94, d: 0.78, h: 0.05, c: C.wood });
  a.box({ x: 0.34, y: 0.36, z: 0.97, w: 0.3, d: 0.04, h: 0.24, c: C.woodDark });
  a.face("y", 0.4, { u: [0.34, 0.64], z: [0.97, 1.21] }, frameFace(0.34, 0.97, 0.3, 0.24, "#F6D9D2"));
  sprig(a, 1.5, 0.5, 0.97, C.egg);
};

const drawerTall: Build = (a) => {
  const body = "#EFE9E0";
  a.shadow({ x: 0.08, y: 0.12, w: 0.84, d: 0.78 });
  legs(a, 0.15, 0.2, 0.77, 0.76, 0.06, C.woodDark, 0.08);
  a.box({ x: 0.12, y: 0.16, z: 0.06, w: 0.76, d: 0.68, h: 1.48, c: body });
  a.face(
    "y",
    0.84,
    { u: [0.16, 0.84], z: [0.1, 1.5] },
    <>{[0, 1, 2, 3, 4].map((i) => drawerFront(0.16, 0.84, 0.1 + i * 0.28, 0.36 + i * 0.28, body, C.woodDark, true))}</>,
  );
  a.box({ x: 0.1, y: 0.14, z: 1.54, w: 0.8, d: 0.72, h: 0.06, c: C.wood });
};

const cabinetLow: Build = (a) => {
  const body = "#B9D3C4";
  a.shadow({ x: 0.02, y: 0.12, w: 1.96, d: 0.8 });
  legs(a, 0.12, 0.2, 1.8, 0.74, 0.08, C.woodDark, 0.08);
  a.box({ x: 0.06, y: 0.16, z: 0.08, w: 1.88, d: 0.68, h: 0.58, c: body });
  a.face(
    "y",
    0.84,
    { u: [0.12, 1.88], z: [0.14, 0.6] },
    <>
      {[0.12, 1.02].map((x) => (
        <g key={x}>
          <rect x={x} y={0.14} width={0.86} height={0.46} fill={shade(body, 0.08)} stroke={shade(body, -0.25)} strokeWidth={0.015} />
          <rect x={x + 0.08} y={0.21} width={0.7} height={0.32} fill="none" stroke={shade(body, -0.15)} strokeWidth={0.012} />
        </g>
      ))}
      <rect x={0.9} y={0.3} width={0.03} height={0.14} rx={0.01} fill={C.woodDark} />
      <rect x={1.07} y={0.3} width={0.03} height={0.14} rx={0.01} fill={C.woodDark} />
    </>,
  );
  a.box({ x: 0.03, y: 0.13, z: 0.66, w: 1.94, d: 0.74, h: 0.05, c: C.woodLight });
  // 꽃병, 초, 액자
  a.cyl({ cx: 0.42, cy: 0.48, z: 0.71, r: 0.08, h: 0.2, c: C.white, taper: 0.03 });
  a.dot(
    0.42,
    0.48,
    0.91,
    (sx, sy) => (
      <g stroke="#B38A5E" strokeWidth={0.9} fill="none" strokeLinecap="round">
        <path d={`M ${sx} ${sy} q -2 -8 -7 -13`} />
        <path d={`M ${sx} ${sy} q 1 -9 5 -15`} />
        <path d={`M ${sx} ${sy} q 0 -6 1 -10`} />
        <circle cx={sx - 7} cy={sy - 13} r={1.4} fill="#E9D7B8" stroke="none" />
        <circle cx={sx + 5} cy={sy - 15} r={1.4} fill="#E9D7B8" stroke="none" />
      </g>
    ),
    0.06,
    0.5,
  );
  a.cyl({ cx: 0.78, cy: 0.6, z: 0.71, r: 0.04, h: 0.1, c: "#F6EFE2" });
  a.dot(0.78, 0.6, 0.81, (sx, sy) => <path d={`M ${sx} ${sy - 1} q -1.6 -2.4 0 -5 q 1.6 2.6 0 5 Z`} fill="#F5B94A" />, 0.02, 0.15);
  a.box({ x: 1.34, y: 0.38, z: 0.71, w: 0.32, d: 0.04, h: 0.25, c: C.woodDark });
  a.face("y", 0.42, { u: [1.34, 1.66], z: [0.71, 0.96] }, frameFace(1.34, 0.71, 0.32, 0.25));
};

const shoeRack: Build = (a) => {
  a.shadow({ x: 0.04, y: 0.16, w: 0.92, d: 0.72 });
  for (const x of [0.08, 0.86]) a.box({ x, y: 0.2, w: 0.06, d: 0.64, h: 1.1, c: C.wood });
  for (const z of [0.06, 0.42, 0.78]) a.box({ x: 0.14, y: 0.2, z, w: 0.72, d: 0.64, h: 0.04, c: C.woodLight });
  a.box({ x: 0.14, y: 0.2, z: 1.04, w: 0.72, d: 0.64, h: 0.06, c: C.woodLight });
  // 신발 한 켤레: 앞코가 +y. 뒤축 윗면을 어둡게 칠해 발 넣는 구멍으로 보이게 한다.
  const pair = (x0: number, z: number, c: string, kind: "sneaker" | "flat" | "boot", sole = C.white) => {
    const hole = shade(c, -0.45);
    for (const x of [x0, x0 + 0.14]) {
      a.box({ x, y: 0.28, z, w: 0.12, d: 0.46, h: 0.035, c: sole });
      const zz = z + 0.035;
      const [back, tall] = kind === "boot" ? [0.22, 0.24] : kind === "flat" ? [0.2, 0.05] : [0.24, 0.11];
      a.box({ x, y: 0.29, z: zz, w: 0.12, d: back, h: tall, c, top: hole });
      a.box({ x: x + 0.01, y: 0.29 + back, z: zz, w: 0.1, d: 0.44 - back, h: kind === "flat" ? 0.045 : 0.06, c });
    }
  };
  pair(0.19, 0.82, C.blush, "sneaker");
  pair(0.52, 0.82, C.white, "sneaker", "#D9D2C6");
  pair(0.19, 0.46, C.ink, "flat", "#6B4A33");
  pair(0.52, 0.46, C.straw, "sneaker");
  pair(0.19, 0.1, C.woodDark, "boot", C.ink);
  pair(0.52, 0.1, C.egg, "sneaker");
};

const coatRack: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.28);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.24, h: 0.05, c: C.woodDark });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.05, r: 0.035, h: 1.67, c: C.wood });
  a.cyl({ cx: 0.5, cy: 0.5, z: 1.72, r: 0.055, h: 0.06, c: C.woodDark });
  for (const [dx, dy] of [
    [1, 0],
    [-1, 0],
    [0, 1],
    [0, -1],
  ])
    a.line([0.5 + dx * 0.04, 0.5 + dy * 0.04, 1.56], [0.5 + dx * 0.17, 0.5 + dy * 0.17, 1.66], C.woodDark, 2);
  // 뒤쪽 갈고리의 목도리
  const scarf = "#8CCFC6";
  card(a, "x", 0.33, [0.43, 0.57], [1.02, 1.62], () => (
    <>
      <path d="M 0.44 1.62 L 0.56 1.62 L 0.55 1.06 L 0.5 1.06 L 0.5 1.5 L 0.46 1.5 L 0.46 1.12 L 0.44 1.12 Z" fill={scarf} stroke={shade(scarf, -0.3)} strokeWidth={0.012} />
      {[1.2, 1.32].map((z) => (
        <rect key={z} x={0.5} y={z} width={0.05} height={0.03} fill={C.white} />
      ))}
    </>
  ));
  // 앞쪽 갈고리의 외투
  const coat = "#C98A58";
  card(a, "y", 0.62, [0.28, 0.72], [0.74, 1.56], (front) => (
    <g stroke={shade(coat, -0.32)} strokeWidth={0.012} strokeLinejoin="round">
      <path d="M 0.42 1.56 L 0.58 1.56 L 0.66 1.5 L 0.68 0.76 L 0.32 0.76 L 0.34 1.5 Z" fill={coat} />
      <path d="M 0.34 1.5 L 0.29 1.42 L 0.29 1.0 L 0.36 1.0 L 0.37 1.36 Z" fill={shade(coat, -0.06)} />
      <path d="M 0.66 1.5 L 0.71 1.42 L 0.71 1.0 L 0.64 1.0 L 0.63 1.36 Z" fill={shade(coat, -0.06)} />
      {front && (
        <>
          <path d="M 0.43 1.56 L 0.5 1.3 L 0.57 1.56 Z" fill={shade(coat, -0.2)} />
          <line x1={0.5} y1={1.3} x2={0.5} y2={0.76} />
          {[1.18, 1.04, 0.9].map((z) => (
            <circle key={z} cx={0.53} cy={z} r={0.014} fill={C.woodDark} />
          ))}
          <line x1={0.38} y1={0.96} x2={0.45} y2={0.96} />
          <line x1={0.55} y1={0.96} x2={0.62} y2={0.96} />
        </>
      )}
    </g>
  ));
  // 오른쪽 갈고리의 가방
  a.line([0.67, 0.5, 1.66], [0.67, 0.33, 1.22], C.ink, 1);
  a.line([0.67, 0.5, 1.66], [0.67, 0.51, 1.22], C.ink, 1);
  a.box({ x: 0.62, y: 0.31, z: 0.92, w: 0.1, d: 0.22, h: 0.3, c: C.blush });
  a.face("x", 0.72, { u: [0.31, 0.53], z: [1.04, 1.22] }, <path d="M 0.31 1.22 L 0.53 1.22 L 0.53 1.1 Q 0.42 1.02 0.31 1.1 Z" fill={shade(C.blush, -0.12)} />);
};

const hangerRack: Build = (a) => {
  a.shadow({ x: 0.06, y: 0.14, w: 1.88, d: 0.74 });
  for (const x of [0.1, 1.82]) {
    a.box({ x, y: 0.16, w: 0.08, d: 0.68, h: 0.06, c: IRON });
    a.box({ x: x + 0.01, y: 0.47, z: 0.06, w: 0.06, d: 0.06, h: 1.54, c: STEEL });
  }
  a.box({ x: 0.17, y: 0.475, z: 1.5, w: 1.66, d: 0.05, h: 0.05, c: STEEL });
  // 옷: 봉에 걸린 옷걸이 하나씩(봉과 직각인 면)
  const shapes: Record<string, string> = {
    tee: "M 0.4 1.38 Q 0.5 1.32 0.6 1.38 L 0.76 1.28 L 0.71 1.2 L 0.66 1.24 L 0.66 0.82 L 0.34 0.82 L 0.34 1.24 L 0.29 1.2 L 0.24 1.28 Z",
    dress: "M 0.4 1.38 Q 0.5 1.33 0.6 1.38 L 0.63 1.1 L 0.74 0.6 L 0.26 0.6 L 0.37 1.1 Z",
    coat: "M 0.4 1.38 L 0.6 1.38 L 0.72 1.3 L 0.74 0.66 L 0.26 0.66 L 0.28 1.3 Z",
    shirt: "M 0.4 1.38 L 0.6 1.38 L 0.72 1.3 L 0.74 0.92 L 0.67 0.92 L 0.66 0.8 L 0.34 0.8 L 0.33 0.92 L 0.26 0.92 L 0.28 1.3 Z",
    pants: "M 0.34 1.38 L 0.66 1.38 L 0.66 0.72 L 0.53 0.72 L 0.5 1.1 L 0.47 0.72 L 0.34 0.72 Z",
  };
  const clothes: [number, string, string][] = [
    [0.36, "coat", "#8FA6BC"],
    [0.58, "tee", C.blush],
    [0.8, "dress", C.straw],
    [1.04, "shirt", C.white],
    [1.28, "pants", "#5B7FA6"],
    [1.52, "tee", C.egg],
  ];
  for (const [x, kind, c] of clothes)
    card(a, "x", x, [0.22, 0.78], [0.58, 1.5], () => (
      <g strokeLinejoin="round">
        <path d="M 0.5 1.5 Q 0.535 1.47 0.5 1.44 M 0.3 1.38 L 0.5 1.44 L 0.7 1.38 Z" fill="none" stroke={IRON} strokeWidth={0.015} />
        <path d={shapes[kind]} fill={c} stroke={shade(c, -0.3)} strokeWidth={0.012} />
        {kind === "shirt" && <line x1={0.5} y1={1.36} x2={0.5} y2={0.8} stroke={shade(c, -0.25)} strokeWidth={0.012} />}
        {kind === "dress" && <rect x={0.37} y={1.06} width={0.26} height={0.04} fill={shade(c, -0.15)} />}
      </g>
    ));
};

const bookshelfLow: Build = (a) => {
  const c = C.wood;
  a.shadow({ y: 0.16, d: 0.72 });
  a.box({ x: 0.04, y: 0.22, w: 1.92, d: 0.58, h: 0.86, c });
  const back = shade(c, -0.45);
  a.face(
    "y",
    0.8,
    { u: [0.1, 1.9], z: [0.06, 0.8] },
    <>
      {[0.06, 0.46].map((z) => [0.1, 1.03].map((x) => <rect key={`${x}-${z}`} x={x} y={z} width={0.87} height={0.34} fill={back} />))}
      {/* 만화책 전집: 같은 색 책등에 번호 띠 */}
      {Array.from({ length: 10 }, (_, i) => (
        <g key={i}>
          <rect x={0.12 + i * 0.083} y={0.46} width={0.079} height={0.29} fill={i < 5 ? "#5B8FB0" : "#D9665B"} stroke="rgba(0,0,0,.25)" strokeWidth={0.01} />
          <rect x={0.12 + i * 0.083} y={0.66} width={0.079} height={0.035} fill={C.white} />
        </g>
      ))}
      {spines(1.05, 1.7, 0.46, 0.34, 2)}
      <rect x={1.74} y={0.46} width={0.07} height={0.26} transform="rotate(-18 1.74 0.46)" fill={C.straw} stroke="rgba(0,0,0,.25)" strokeWidth={0.01} />
      {spines(0.12, 0.95, 0.06, 0.34, 5)}
      {spines(1.05, 1.5, 0.06, 0.34, 3)}
      <rect x={1.56} y={0.06} width={0.32} height={0.08} fill={C.egg} stroke="rgba(0,0,0,.25)" strokeWidth={0.01} />
      <rect x={1.58} y={0.14} width={0.28} height={0.07} fill={C.blush} stroke="rgba(0,0,0,.25)" strokeWidth={0.01} />
    </>,
  );
  sprig(a, 1.62, 0.5, 0.86, "#CF8A63");
  // 토끼 인형
  a.dot(
    0.5,
    0.52,
    0.86,
    (sx, sy) => (
      <g fill={C.white} stroke="#C9C6BE" strokeWidth={0.6}>
        <ellipse cx={sx - 1.6} cy={sy - 10} rx={1.3} ry={3.4} />
        <ellipse cx={sx + 1.6} cy={sy - 10} rx={1.3} ry={3.4} />
        <ellipse cx={sx} cy={sy - 3} rx={4} ry={3.4} />
        <circle cx={sx} cy={sy - 6.6} r={2.9} />
        <circle cx={sx + 1.2} cy={sy - 6.6} r={0.5} fill={C.ink} stroke="none" />
      </g>
    ),
    0.06,
    0.5,
  );
};

const bookcaseWide: Build = (a) => {
  const c = C.woodLight;
  const back = shade(c, -0.45);
  a.shadow({ y: 0.14, d: 0.74 });
  a.box({ x: 0.04, y: 0.2, w: 1.92, d: 0.6, h: 2.2, c });
  const rows = [0.1, 0.62, 1.14, 1.66];
  a.face(
    "y",
    0.8,
    { u: [0.1, 1.9], z: [0.1, 2.12] },
    <>
      {rows.map((z) => [0.1, 1.03].map((x) => <rect key={`${x}-${z}`} x={x} y={z} width={0.87} height={0.46} fill={back} />))}
      {/* 1층: 책과 바구니 */}
      {spines(0.12, 0.95, 0.1, 0.46, 1)}
      <rect x={1.08} y={0.1} width={0.56} height={0.26} fill="#C9A26B" stroke="#9C7A4A" strokeWidth={0.015} />
      {[0.17, 0.24, 0.31].map((z) => (
        <line key={z} x1={1.08} y1={z} x2={1.64} y2={z} stroke="#9C7A4A" strokeWidth={0.012} />
      ))}
      {spines(1.68, 1.88, 0.1, 0.46, 6)}
      {/* 2층: 책과 화분 */}
      {spines(0.12, 0.66, 0.62, 0.46, 2)}
      {potFace(0.82, 0.62)}
      {spines(1.05, 1.88, 0.62, 0.46, 3)}
      {/* 3층: 액자와 기댄 책 */}
      {frameFace(0.16, 1.14, 0.3, 0.32)}
      {spines(0.52, 0.95, 1.14, 0.46, 4)}
      {spines(1.05, 1.5, 1.14, 0.46, 7)}
      <rect x={1.54} y={1.14} width={0.07} height={0.32} transform="rotate(-20 1.54 1.14)" fill={C.egg} stroke="rgba(0,0,0,.25)" strokeWidth={0.01} />
      <path d="M 1.74 1.14 L 1.84 1.14 L 1.86 1.3 Q 1.79 1.36 1.72 1.3 Z" fill={C.white} stroke="#C9C6BE" strokeWidth={0.01} />
      {/* 4층: 책과 지구본 */}
      {spines(0.12, 0.95, 1.66, 0.46, 0)}
      <rect x={1.12} y={1.66} width={0.2} height={0.03} fill={C.woodDark} />
      <line x1={1.22} y1={1.69} x2={1.22} y2={1.74} stroke={C.woodDark} strokeWidth={0.02} />
      <circle cx={1.22} cy={1.86} r={0.13} fill="#7FB8D9" stroke={C.woodDark} strokeWidth={0.015} />
      <path d="M 1.14 1.9 q 0.05 0.06 0.1 0 t 0.08 -0.06 M 1.16 1.8 q 0.06 -0.03 0.1 0.02" stroke={C.leaf} strokeWidth={0.035} fill="none" />
      {spines(1.42, 1.88, 1.66, 0.46, 5)}
    </>,
  );
};

// ── 그 밖 ────────────────────────────────────────────────────────

function barStool(a: Art, cx: number, cy: number) {
  const top = 0.66;
  const corners = [
    [-1, -1],
    [1, -1],
    [-1, 1],
    [1, 1],
  ];
  for (const [dx, dy] of corners) a.line([cx + dx * 0.08, cy + dy * 0.08, top], [cx + dx * 0.12, cy + dy * 0.12, 0], C.woodDark, 2);
  const s = 0.08 + 0.04 * (1 - 0.24 / top);
  for (const [p, q] of [
    [[-s, -s], [s, -s]],
    [[-s, -s], [-s, s]],
    [[s, -s], [s, s]],
    [[-s, s], [s, s]],
  ])
    a.line([cx + p[0], cy + p[1], 0.24], [cx + q[0], cy + q[1], 0.24], C.woodDark, 1.4);
  a.cyl({ cx, cy, z: top, r: 0.15, h: 0.05, c: C.woodDark });
  a.cyl({ cx, cy, z: top + 0.05, r: 0.14, h: 0.04, c: C.straw });
}

const islandBar: Build = (a) => {
  const body = "#EFE8DD";
  a.shadow({ x: 0.02, y: 0.02, w: 1.96, d: 0.96 });
  a.box({ x: 0.08, y: 0.06, w: 1.84, d: 0.42, h: 0.98, c: body });
  a.face(
    "y",
    0.48,
    { u: [0.08, 1.92], z: [0.04, 0.96] },
    <g stroke={shade(body, -0.18)} strokeWidth={0.012}>
      {Array.from({ length: 13 }, (_, i) => (
        <line key={i} x1={0.22 + i * 0.13} y1={0.06} x2={0.22 + i * 0.13} y2={0.94} />
      ))}
    </g>,
  );
  a.box({ x: 0.02, y: 0.02, z: 0.98, w: 1.96, d: 0.54, h: 0.07, c: C.wood });
  for (const [x, y] of [
    [0.36, 0.3],
    [0.54, 0.38],
  ])
    a.cyl({ cx: x, cy: y, z: 1.05, r: 0.04, h: 0.11, c: "#CFE8F2", top: "#F3D27A" });
  sprig(a, 1.7, 0.26, 1.05);
  for (const cx of [0.55, 1.45]) barStool(a, cx, 0.8);
};

const swingChair: Build = (a) => {
  const rattan = "#D9B27C";
  roundShadow(a, 0.5, 0.52, 0.36);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.34, h: 0.05, c: IRON });
  // 뒤에서 올라와 앞으로 굽는 기둥. 굽은 팔은 의자보다 높이 있어 늘 의자 뒤에 그린다.
  a.box({ x: 0.47, y: 0.12, z: 0.05, w: 0.06, d: 0.05, h: 1.55, c: IRON });
  curve(
    a,
    Array.from({ length: 9 }, (_, i): P3 => {
      const t = Math.PI - (i / 8) * (Math.PI / 2);
      return [0.5, 0.52 + 0.375 * Math.cos(t), 1.6 + 0.28 * Math.sin(t)];
    }),
    IRON,
    3.2,
  );
  a.line([0.5, 0.52, 1.86], [0.5, 0.52, 1.58], "#8C9096", 1.2);
  a.add([0.2, 0.25, 0.42, 0.8, 0.85, 1.58], (v) => {
    const [ex, ey] = v.p(0.5, 0.55, 1.02);
    const front = v.sees(0, 1);
    const fx = Math.sign(v.p(0.5, 0.65, 1.02)[0] - ex);
    const egg = `M ${ex} ${ey - 16} C ${ex + 8} ${ey - 16} ${ex + 12.5} ${ey - 6} ${ex + 12.5} ${ey + 3} C ${ex + 12.5} ${ey + 11} ${ex + 7} ${ey + 15} ${ex} ${ey + 15} C ${ex - 7} ${ey + 15} ${ex - 12.5} ${ey + 11} ${ex - 12.5} ${ey + 3} C ${ex - 12.5} ${ey - 6} ${ex - 8} ${ey - 16} ${ex} ${ey - 16} Z`;
    const weave = [-10, -5, 0, 5, 10].map((k) => {
      const hw = 12.5 * Math.sqrt(Math.max(0, 1 - ((k - 2) / 16) ** 2));
      return <path key={k} d={`M ${ex - hw} ${ey + k} Q ${ex} ${ey + k + 3} ${ex + hw} ${ey + k}`} fill="none" stroke={shade(rattan, -0.22)} strokeWidth={0.6} />;
    });
    const ox = ex + fx * 3.2;
    return (
      <g>
        <path d={egg} fill={rattan} stroke={shade(rattan, -0.35)} strokeWidth={0.7} />
        {weave}
        {front && (
          <>
            <ellipse cx={ox} cy={ey + 1.5} rx={8.6} ry={12} fill="#7A5634" stroke={shade(rattan, -0.3)} strokeWidth={1.4} />
            <ellipse cx={ox - fx * 0.5} cy={ey - 1.5} rx={6.4} ry={7.4} fill={C.blush} />
            <ellipse cx={ox} cy={ey + 8} rx={7.6} ry={3.8} fill={C.linen} stroke="#D6D0C4" strokeWidth={0.5} />
          </>
        )}
      </g>
    );
  });
};

export const BUILDS: Record<string, Build> = {
  bed_single: bedSingle,
  bed_bunk: bedBunk,
  futon,
  sofa_corner: sofaCorner,
  armchair,
  beanbag,
  rocking_chair: rockingChair,
  stool,
  bench,
  gaming_chair: gamingChair,
  table_round: tableRound,
  table_coffee: tableCoffee,
  table_side: tableSide,
  table_floor: tableFloor,
  warm_table: warmTable,
  desk_computer: deskComputer,
  vanity,
  dresser,
  drawer_tall: drawerTall,
  cabinet_low: cabinetLow,
  shoe_rack: shoeRack,
  coat_rack: coatRack,
  hanger_rack: hangerRack,
  bookshelf_low: bookshelfLow,
  bookcase_wide: bookcaseWide,
  island_bar: islandBar,
  swing_chair: swingChair,
};
