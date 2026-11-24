// 취미·장식 물건 그림(더한 물건). 돌리지 않은 방향의 모형 좌표에 도형을 쌓는다: x 는 오른쪽 아래, y 는 왼쪽 아래,
// z 는 위. 물건의 등은 y = 0, 앞은 +y 쪽이다(src/room/model.tsx).

import type { ReactNode } from "react";
import { C, proj, shade, type Art, type Build, type P3 } from "../kit";

type B6 = [number, number, number, number, number, number];

/** 바닥 원 반지름 1 칸의 화면 가로 반지름(px). 공의 화면 세로 반지름은 이것의 0.87 배다. */
const K = Math.SQRT2 * 28;
const IRON = "#4A4F55";
const INK = C.ink;

// ── 도우미 ───────────────────────────────────────────────────────

const boundsOf = (list: P3[]): B6 => {
  const k = (i: number, f: (...n: number[]) => number) => f(...list.map((p) => p[i]));
  return [k(0, Math.min), k(1, Math.min), k(2, Math.min), k(0, Math.max), k(1, Math.max), k(2, Math.max)];
};
const mean = (list: P3[]): P3 => list.reduce<P3>((s, p) => [s[0] + p[0] / list.length, s[1] + p[1] / list.length, s[2] + p[2] / list.length], [0, 0, 0]);
const lerp = (p: P3, q: P3, t: number): P3 => [p[0] + (q[0] - p[0]) * t, p[1] + (q[1] - p[1]) * t, p[2] + (q[2] - p[2]) * t];
const rad = (deg: number) => (deg * Math.PI) / 180;

/** 다각형의 법선(꼭짓점 차례의 오른손 방향, 길이 1) */
function normal(f: P3[]): P3 {
  const n: P3 = [0, 0, 0];
  f.forEach((p, i) => {
    const q = f[(i + 1) % f.length];
    n[0] += (p[1] - q[1]) * (p[2] + q[2]);
    n[1] += (p[2] - q[2]) * (p[0] + q[0]);
    n[2] += (p[0] - q[0]) * (p[1] + q[1]);
  });
  const l = Math.hypot(...n) || 1;
  return [n[0] / l, n[1] / l, n[2] / l];
}

/**
 * 볼록한 입체(텐트, 지붕, 기운 책): 면마다 꼭짓점을 둘레 차례로 준다. 화면 쪽을 보는 면만 상자처럼 빛을 주어 칠한다.
 * fill 은 면마다 줄 수 있고, 한 면 위에 겹친 작은 면(띠)은 뒤에 두면 위에 칠해진다.
 */
function solid(a: Art, faces: P3[][], fill: string | string[]) {
  const all = faces.flat();
  const mid = mean(all);
  const out = faces.map((f) => {
    const n = normal(f);
    const m = mean(f);
    return n[0] * (m[0] - mid[0]) + n[1] * (m[1] - mid[1]) + n[2] * (m[2] - mid[2]) < 0 ? [...f].reverse() : f;
  });
  a.add(boundsOf(all), (v) => {
    const [ox] = v.at(0, 0);
    return (
      <g strokeWidth={0.6} strokeLinejoin="round">
        {out.map((f, i) => {
          const s = f.map((p) => v.p(...p));
          const area = s.reduce((acc, p, j) => acc + p[0] * s[(j + 1) % s.length][1] - s[(j + 1) % s.length][0] * p[1], 0);
          if (area <= 0.5) return null;
          const n = normal(f);
          const c = typeof fill === "string" ? fill : fill[i];
          const lit = 0.14 * n[2] - 0.16 * Math.max(0, v.at(n[0], n[1])[0] - ox);
          return <polygon key={i} points={s.map((p) => p.join(",")).join(" ")} fill={shade(c, lit)} stroke={shade(c, -0.32)} />;
        })}
      </g>
    );
  });
}

/** 같은 꼭짓점 수의 아래·위 둘레를 이은 입체의 면들: 아래, 위, 옆면(i 번째 변마다) */
const loft = (lo: P3[], hi: P3[]): P3[][] => [lo, hi, ...lo.map((p, i) => [p, lo[(i + 1) % lo.length], hi[(i + 1) % lo.length], hi[i]])];
const at = (base: [number, number][], z: number) => base.map(([x, y]): P3 => [x, y, z]);

/** 가운데 (cx, cy), 넓이 w×d 를 deg 만큼 돌린 바닥 사각형(뒤, 오른쪽, 앞, 왼쪽 변 차례) */
const turned = (cx: number, cy: number, w: number, d: number, deg: number): [number, number][] =>
  [
    [-1, -1],
    [1, -1],
    [1, 1],
    [-1, 1],
  ].map(([i, j]) => {
    const [u, t, c, s] = [(i * w) / 2, (j * d) / 2, Math.cos(rad(deg)), Math.sin(rad(deg))];
    return [cx + u * c - t * s, cy + u * s + t * c];
  });

/** 화면 좌표로 그리는 조각: p 는 모형 좌표, front 면 앞(+y)이 보일 때만, lift 는 가리는 차례를 앞으로 당긴다. */
interface Bit {
  p: P3;
  draw: (sx: number, sy: number) => ReactNode;
  front?: boolean;
  lift?: number;
}

/** 둥근 조각들(인형, 호박, 잎)을 돌린 방향에서 먼 것부터 그린다. */
function cluster(a: Art, b: B6, bits: Bit[]) {
  a.add(b, (v) =>
    bits
      .filter((t) => !t.front || v.sees(0, 1))
      .map((t) => {
        const [X, Y] = v.at(t.p[0], t.p[1]);
        return { t, d: X + Y + t.p[2] + (t.lift ?? 0) };
      })
      .sort((m, n) => m.d - n.d)
      .map(({ t }, i) => <g key={i}>{t.draw(...v.p(...t.p))}</g>),
  );
}

/** 공(화면 가운데 sx, sy, 반지름 r 칸). flat 은 세로를 줄인다. */
const ball = (sx: number, sy: number, r: number, fill: string, flat = 1) => (
  <>
    <ellipse cx={sx} cy={sy} rx={r * K} ry={r * K * 0.87 * flat} fill={fill} stroke={shade(fill, -0.3)} strokeWidth={0.6} />
    <ellipse cx={sx - r * K * 0.3} cy={sy - r * K * 0.38 * flat} rx={r * K * 0.36} ry={r * K * 0.2 * flat} fill="#fff" opacity={0.22} />
  </>
);

/** 나비 리본(화면 좌표) */
const bow = (x: number, y: number, c: string, s = 1) => (
  <g fill={c} stroke={shade(c, -0.3)} strokeWidth={0.5}>
    <path d={`M ${x} ${y} l ${-4.5 * s} ${-2.6 * s} v ${5.2 * s} Z M ${x} ${y} l ${4.5 * s} ${-2.6 * s} v ${5.2 * s} Z`} />
    <circle cx={x} cy={y} r={1.3 * s} />
  </g>
);

/** 별 꼭짓점 목록(꼭짓점 하나가 위). up 이면 면 좌표(위가 +)에 그린다. */
const star = (x: number, y: number, R: number, up = false) =>
  Array.from({ length: 10 }, (_, k) => {
    const [r, t] = [k % 2 ? R * 0.45 : R, rad(90 + k * 36)];
    return `${x + r * Math.cos(t)},${y + (up ? 1 : -1) * r * Math.sin(t)}`;
  }).join(" ");

/** 둥근 물건 아래의 옅은 그림자 */
function roundShadow(a: Art, cx: number, cy: number, r: number) {
  a.add([cx - r, cy - r, 0, cx + r, cy + r, 0], (v) => {
    const [x, y] = v.p(cx, cy);
    return <ellipse cx={x} cy={y} rx={r * K} ry={(r * K) / 2} fill="rgba(40,30,20,0.16)" />;
  });
}

/** 얇은 판(기타, 자전거): axis 면(at) 위에 (u, z) 로 그리고 앞뒤 어느 쪽에서도 보인다. front 는 +쪽이 보이는지. */
function card(a: Art, axis: "x" | "y", at: number, u: [number, number], z: [number, number], draw: (front: boolean) => ReactNode) {
  const b: B6 = axis === "y" ? [u[0], at, z[0], u[1], at, z[1]] : [at, u[0], z[0], at, u[1], z[1]];
  a.add(b, (v) => <g transform={v.faceT(axis, at)}>{draw(v.sees(axis === "x" ? 1 : 0, axis === "y" ? 1 : 0))}</g>);
}

/** 건반 윗면: x0~x1 에 흰건반 n 개(도부터), 검은건반은 뒤쪽(y0 쪽) 60% */
function keyboard(a: Art, x0: number, x1: number, y0: number, y1: number, z: number, n: number) {
  a.add([x0, y0, z, x1, y1, z], (v) => {
    const w = (x1 - x0) / n;
    const yb = y0 + (y1 - y0) * 0.6;
    const quad = (u0: number, u1: number, t0: number, t1: number) => v.pts([[u0, t0, z], [u1, t0, z], [u1, t1, z], [u0, t1, z]]);
    return (
      <g>
        <polygon points={quad(x0, x1, y0, y1)} fill={C.white} stroke="#B9B4AA" strokeWidth={0.5} />
        {Array.from({ length: n - 1 }, (_, i) => {
          const x = x0 + (i + 1) * w;
          const [p, q] = [v.p(x, y0, z), v.p(x, y1, z)];
          return (
            <g key={i}>
              <line x1={p[0]} y1={p[1]} x2={q[0]} y2={q[1]} stroke="#CFC9BE" strokeWidth={0.4} />
              {[1, 2, 4, 5, 6].includes((i + 1) % 7) && <polygon points={quad(x - w * 0.3, x + w * 0.3, y0, yb)} fill={INK} />}
            </g>
          );
        })}
      </g>
    );
  });
}

// ── 악기 ─────────────────────────────────────────────────────────

const PIANO = "#6B4532";

const piano: Build = (a) => {
  a.shadow();
  a.box({ x: 0.08, y: 0.06, w: 1.84, d: 0.46, h: 1.22, c: PIANO });
  a.face(
    "y",
    0.52,
    { u: [0.3, 1.7], z: [0.12, 0.5] },
    <g fill="none" stroke={shade(PIANO, -0.35)} strokeWidth={0.015}>
      <rect x={0.3} y={0.12} width={0.62} height={0.38} rx={0.03} />
      <rect x={1.08} y={0.12} width={0.62} height={0.38} rx={0.03} />
    </g>,
  );
  a.box({ x: 0.04, y: 0.03, z: 1.22, w: 1.92, d: 0.52, h: 0.06, c: shade(PIANO, 0.1) });
  // 건반 받침, 양쪽 볼과 다리
  a.box({ x: 0.2, y: 0.53, z: 0.6, w: 1.6, d: 0.31, h: 0.08, c: PIANO });
  for (const x of [0.1, 1.8]) {
    a.box({ x: x + 0.02, y: 0.74, w: 0.06, d: 0.07, h: 0.6, c: PIANO });
    a.box({ x, y: 0.52, z: 0.6, w: 0.1, d: 0.34, h: 0.18, c: PIANO });
  }
  keyboard(a, 0.2, 1.8, 0.53, 0.84, 0.68, 22);
  // 보면대와 악보
  a.box({ x: 0.55, y: 0.525, z: 0.84, w: 0.9, d: 0.065, h: 0.04, c: shade(PIANO, 0.1) });
  a.face(
    "y",
    0.52,
    { u: [0.66, 1.34], z: [0.88, 1.14] },
    <g>
      <rect x={0.66} y={0.88} width={0.33} height={0.26} fill={C.white} stroke="#C9C6BE" strokeWidth={0.01} />
      <rect x={1.01} y={0.88} width={0.33} height={0.26} fill={C.white} stroke="#C9C6BE" strokeWidth={0.01} />
      {[0.94, 0.99, 1.04, 1.09].map((z) => (
        <path key={z} d={`M 0.7 ${z} H 0.95 M 1.05 ${z} H 1.3`} stroke="#9AA0A6" strokeWidth={0.008} />
      ))}
    </g>,
  );
  for (const x of [0.88, 0.98, 1.08]) a.box({ x, y: 0.52, z: 0.05, w: 0.04, d: 0.1, h: 0.02, c: C.straw });
  // 뚜껑 위의 메트로놈
  solid(a, loft(at(turned(1.6, 0.26, 0.14, 0.12, 0), 1.28), at(turned(1.6, 0.26, 0.04, 0.03, 0), 1.5)), C.woodDark);
  a.line([1.6, 0.33, 1.31], [1.63, 0.33, 1.45], C.straw, 1);
};

const guitarStand: Build = (a) => {
  a.shadow({ x: 0.22, y: 0.2, w: 0.58, d: 0.6 });
  a.line([0.5, 0.16, 0], [0.5, 0.47, 0.8], IRON, 1.6);
  card(a, "y", 0.5, [0.27, 0.73], [0.08, 1.1], (front) => {
    const wood = front ? "#E7AE6E" : "#B7743F";
    return (
      <g stroke={shade(wood, -0.4)} strokeWidth={0.012}>
        <rect x={0.475} y={0.7} width={0.05} height={0.3} fill="#4A3426" />
        <path d="M 0.465 0.99 L 0.535 0.99 L 0.55 1.1 L 0.45 1.1 Z" fill="#4A3426" />
        <path d="M 0.5 0.08 C 0.22 0.08 0.2 0.36 0.38 0.46 C 0.3 0.54 0.32 0.74 0.5 0.74 C 0.68 0.74 0.7 0.54 0.62 0.46 C 0.8 0.36 0.78 0.08 0.5 0.08 Z" fill={wood} />
        {front && (
          <>
            <circle cx={0.5} cy={0.5} r={0.06} fill="#3A2618" stroke="#8A5A2E" strokeWidth={0.015} />
            <rect x={0.43} y={0.2} width={0.14} height={0.035} fill="#4A3426" stroke="none" />
            <path d="M 0.487 0.22 V 1.05 M 0.5 0.22 V 1.05 M 0.513 0.22 V 1.05" stroke="#EDE8DE" strokeWidth={0.005} />
          </>
        )}
        {[1.03, 1.075].map((z) => (
          <path key={z} d={`M 0.425 ${z} h 0.03 M 0.545 ${z} h 0.03`} stroke="#D8D4CC" strokeWidth={0.02} />
        ))}
      </g>
    );
  });
  a.line([0.26, 0.78, 0], [0.5, 0.58, 0.2], IRON, 1.6);
  a.line([0.74, 0.78, 0], [0.5, 0.58, 0.2], IRON, 1.6);
  a.line([0.34, 0.58, 0.1], [0.66, 0.58, 0.1], IRON, 2.2);
};

const keyboardSynth: Build = (a) => {
  a.shadow({ x: 0.1, y: 0.18, w: 1.8, d: 0.66 });
  for (const x of [0.42, 1.58]) {
    a.line([x, 0.22, 0], [x, 0.76, 0.64], IRON, 1.8);
    a.line([x, 0.76, 0], [x, 0.22, 0.64], IRON, 1.8);
  }
  a.line([0.42, 0.49, 0.32], [1.58, 0.49, 0.32], IRON, 1.4);
  a.box({ x: 0.08, y: 0.26, z: 0.64, w: 1.84, d: 0.48, h: 0.08, c: "#3E444B" });
  keyboard(a, 0.16, 1.84, 0.46, 0.72, 0.72, 24);
  // 조작부: 손잡이, 작은 화면, 단추
  a.add([0.12, 0.28, 0.72, 1.88, 0.44, 0.72], (v) => {
    const quad = (x0: number, x1: number, y0: number, y1: number) => v.pts([[x0, y0, 0.72], [x1, y0, 0.72], [x1, y1, 0.72], [x0, y1, 0.72]]);
    return (
      <g>
        {[0.28, 0.4, 0.52].map((x) => {
          const [sx, sy] = v.p(x, 0.36, 0.72);
          return <circle key={x} cx={sx} cy={sy} r={1.4} fill="#C9CED2" />;
        })}
        <polygon points={quad(0.7, 1.1, 0.31, 0.42)} fill={C.egg} stroke="#1E2226" strokeWidth={0.6} />
        {[C.blush, C.straw, C.egg].map((c, i) => (
          <polygon key={c} points={quad(1.22 + i * 0.08, 1.27 + i * 0.08, 0.33, 0.4)} fill={c} />
        ))}
      </g>
    );
  });
  a.dot(
    1.66,
    0.36,
    0.72,
    (x, y) => (
      <g stroke={shade(C.blush, -0.35)} strokeWidth={0.6}>
        <path d={`M ${x - 6} ${y - 1} C ${x - 6} ${y - 10} ${x + 6} ${y - 10} ${x + 6} ${y - 1}`} fill="none" stroke={INK} strokeWidth={1.6} strokeLinecap="round" />
        <ellipse cx={x - 6} cy={y} rx={2.4} ry={3} fill={C.blush} />
        <ellipse cx={x + 6} cy={y} rx={2.4} ry={3} fill={C.blush} />
      </g>
    ),
    0.14,
    0.3,
  );
};

// ── 인형 ─────────────────────────────────────────────────────────

const FUR = "#C9925E";
const MUZZLE = "#F2DDC0";

const teddyBig: Build = (a) => {
  roundShadow(a, 0.5, 0.52, 0.38);
  const fur = (p: P3, r: number, lift = 0): Bit => ({ p, lift, draw: (x, y) => ball(x, y, r, FUR) });
  const ear = (p: P3): Bit => ({ p, draw: (x, y) => <>{ball(x, y, 0.08, FUR)}<circle cx={x} cy={y} r={1.6} fill={MUZZLE} /></> });
  const pad = (p: P3): Bit => ({ p, front: true, lift: 0.4, draw: (x, y) => <ellipse cx={x} cy={y} rx={2.6} ry={3} fill={MUZZLE} /> });
  cluster(a, [0.12, 0.14, 0, 0.88, 0.9, 1.0], [
    fur([0.5, 0.42, 0.32], 0.3),
    fur([0.3, 0.72, 0.1], 0.13, 0.2),
    fur([0.7, 0.72, 0.1], 0.13, 0.2),
    fur([0.17, 0.54, 0.38], 0.1, 0.25),
    fur([0.83, 0.54, 0.38], 0.1, 0.25),
    fur([0.5, 0.44, 0.76], 0.22),
    ear([0.31, 0.42, 0.94]),
    ear([0.69, 0.42, 0.94]),
    pad([0.3, 0.84, 0.1]),
    pad([0.7, 0.84, 0.1]),
    { p: [0.5, 0.6, 0.3], front: true, draw: (x, y) => <ellipse cx={x} cy={y} rx={6} ry={6.5} fill={MUZZLE} opacity={0.75} /> },
    { p: [0.5, 0.58, 0.56], front: true, lift: 0.3, draw: (x, y) => bow(x, y, "#E0675F") },
    {
      p: [0.5, 0.57, 0.73],
      front: true,
      lift: 0.1,
      draw: (x, y) => (
        <g>
          <ellipse cx={x} cy={y + 1} rx={4.6} ry={3.4} fill={MUZZLE} />
          <ellipse cx={x} cy={y - 0.2} rx={1.6} ry={1.1} fill={INK} />
          <path d={`M ${x} ${y + 0.8} v 1.2 M ${x - 1.4} ${y + 2.2} q 1.4 1 2.8 0`} stroke={INK} strokeWidth={0.6} fill="none" />
          <circle cx={x - 3.4} cy={y - 3.6} r={1.1} fill={INK} />
          <circle cx={x + 3.4} cy={y - 3.6} r={1.1} fill={INK} />
        </g>
      ),
    },
  ]);
};

const plushPile: Build = (a) => {
  roundShadow(a, 0.5, 0.55, 0.36);
  const [BEAR, BUNNY, PINK] = ["#B98A63", "#F6F1EA", "#F2B5B0"];
  const eyes = (x: number, y: number, gap: number) => (
    <>
      <circle cx={x - gap} cy={y} r={0.8} fill={INK} />
      <circle cx={x + gap} cy={y} r={0.8} fill={INK} />
    </>
  );
  const ear = (p: P3): Bit => ({
    p,
    lift: -0.1,
    draw: (x, y) => (
      <g stroke={shade(BUNNY, -0.3)} strokeWidth={0.6}>
        <ellipse cx={x} cy={y - 4} rx={1.8} ry={4.8} fill={BUNNY} />
        <ellipse cx={x} cy={y - 4} rx={0.8} ry={3.2} fill={PINK} stroke="none" />
      </g>
    ),
  });
  cluster(a, [0.16, 0.18, 0, 0.84, 0.86, 0.52], [
    { p: [0.62, 0.36, 0.17], draw: (x, y) => ball(x, y, 0.15, BEAR) },
    { p: [0.62, 0.38, 0.38], draw: (x, y) => ball(x, y, 0.11, BEAR) },
    { p: [0.53, 0.37, 0.49], draw: (x, y) => ball(x, y, 0.045, BEAR) },
    { p: [0.71, 0.37, 0.49], draw: (x, y) => ball(x, y, 0.045, BEAR) },
    {
      p: [0.62, 0.46, 0.36],
      front: true,
      lift: 0.1,
      draw: (x, y) => (
        <>
          <ellipse cx={x} cy={y + 1} rx={2.8} ry={2} fill="#EBD3B5" />
          <ellipse cx={x} cy={y + 0.4} rx={1} ry={0.7} fill={INK} />
          {eyes(x, y - 2.4, 2.4)}
        </>
      ),
    },
    { p: [0.33, 0.6, 0.12], draw: (x, y) => ball(x, y, 0.12, BUNNY) },
    { p: [0.33, 0.62, 0.3], draw: (x, y) => ball(x, y, 0.09, BUNNY) },
    ear([0.29, 0.6, 0.4]),
    ear([0.37, 0.6, 0.4]),
    {
      p: [0.33, 0.69, 0.29],
      front: true,
      lift: 0.1,
      draw: (x, y) => (
        <>
          <ellipse cx={x} cy={y + 0.6} rx={0.9} ry={0.6} fill={PINK} />
          {eyes(x, y - 1.4, 2)}
        </>
      ),
    },
    {
      p: [0.72, 0.74, 0.1],
      draw: (x, y) => (
        <>
          {ball(x, y, 0.1, C.egg)}
          <path d={`M ${x - 3.9} ${y - 0.5} Q ${x} ${y + 2.6} ${x + 3.9} ${y - 0.5}`} stroke="#fff" strokeWidth={1.3} fill="none" />
        </>
      ),
    },
  ]);
};

// ── 탐험과 운동 ──────────────────────────────────────────────────

const globe: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.26);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.22, h: 0.05, c: C.woodDark });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.05, r: 0.035, h: 0.22, c: C.woodDark, taper: 0.01 });
  a.dot(
    0.5,
    0.5,
    0.58,
    (x, y) => {
      const [rx, ry] = [0.26 * K, 0.26 * K * 0.87];
      return (
        <g>
          <ellipse cx={x} cy={y} rx={rx} ry={ry} fill="#7EC0D6" stroke={shade("#7EC0D6", -0.3)} strokeWidth={0.6} />
          <path d={`M ${x - 6} ${y - 5} q 3 -2 5 0 q 1 3 -1 5 q -2 2 -1 5 q -3 1 -4 -3 q -2 -3 1 -7 Z`} fill="#8DBF6A" />
          <path d={`M ${x + 2} ${y - 2} q 4 -2 6 1 q 1 3 -2 4 q -1 3 -3 2 q -2 -3 -1 -7 Z`} fill="#E7C98A" />
          <path d={`M ${x + 1} ${y + 5} q 3 -1 4 1 q -1 2 -4 1 Z`} fill="#8DBF6A" />
          <ellipse cx={x - rx * 0.35} cy={y - ry * 0.45} rx={rx * 0.3} ry={ry * 0.16} fill="#fff" opacity={0.35} />
          <g transform={`translate(${x} ${y}) rotate(-20)`} fill="#C9A45C">
            <path d={`M 0 ${ry + 2} A ${rx + 2} ${ry + 2} 0 0 1 0 ${-ry - 2}`} fill="none" stroke="#C9A45C" strokeWidth={1.5} />
            <circle cx={0} cy={-ry - 2} r={1.1} />
            <circle cx={0} cy={ry + 2} r={1.1} />
          </g>
        </g>
      );
    },
    0.3,
    0.34,
  );
};

const telescope: Build = (a) => {
  roundShadow(a, 0.5, 0.46, 0.36);
  for (const [x, y] of [
    [0.2, 0.68],
    [0.8, 0.68],
    [0.5, 0.14],
  ])
    a.line([x, y, 0], [0.5, 0.5, 0.74], C.woodDark, 1.8);
  a.box({ x: 0.45, y: 0.45, z: 0.7, w: 0.1, d: 0.1, h: 0.12, c: IRON });
  const [p, q]: [P3, P3] = [
    [0.5, 0.74, 0.8],
    [0.5, 0.2, 1.24],
  ];
  const TUBE = "#3F5B8C";
  a.add([0.42, 0.14, 0.78, 0.58, 0.8, 1.32], (v) => {
    const s = (t: number) => v.p(...lerp(p, q, t));
    const [m, n] = [s(0), s(1)];
    const l = Math.hypot(n[0] - m[0], n[1] - m[1]);
    // 관 위쪽으로 비킨 선(빛, 보조 망원경)
    const [ux, uy] = n[0] > m[0] ? [(n[1] - m[1]) / l, -(n[0] - m[0]) / l] : [-(n[1] - m[1]) / l, (n[0] - m[0]) / l];
    const seg = (t0: number, t1: number, c: string, w: number, off = 0) => {
      const [e, f] = [s(t0), s(t1)];
      return <line x1={e[0] + ux * off} y1={e[1] + uy * off} x2={f[0] + ux * off} y2={f[1] + uy * off} stroke={c} strokeWidth={w} />;
    };
    return (
      <g>
        {seg(-0.14, 0, INK, 2.6)}
        {seg(0, 1, shade(TUBE, -0.35), 6.6)}
        {seg(0.01, 0.99, TUBE, 5.4)}
        {seg(0.06, 0.78, shade(TUBE, 0.3), 1.2, 1.6)}
        {seg(0.36, 0.42, "#C9A45C", 6.8)}
        {seg(0.8, 1.04, shade(TUBE, -0.35), 8.2)}
        {seg(0.81, 1.03, "#C9A45C", 7)}
        {seg(0.2, 0.46, IRON, 1.8, 5)}
      </g>
    );
  });
};

const treadmill: Build = (a) => {
  a.shadow();
  const DECK = "#C9CED2";
  a.box({ x: 0.1, y: 0.06, w: 0.8, d: 0.34, h: 0.28, c: C.egg });
  a.box({ x: 0.12, y: 0.4, w: 0.76, d: 1.52, h: 0.12, c: DECK });
  a.box({ x: 0.2, y: 0.42, z: 0.12, w: 0.6, d: 1.46, h: 0.02, c: "#3A3F45" });
  a.add([0.2, 0.42, 0.14, 0.8, 1.88, 0.14], (v) => (
    <g stroke="#566069" strokeWidth={0.6}>
      {[0.7, 1.0, 1.3, 1.6].map((y) => {
        const [m, n] = [v.p(0.2, y, 0.14), v.p(0.8, y, 0.14)];
        return <line key={y} x1={m[0]} y1={m[1]} x2={n[0]} y2={n[1]} />;
      })}
    </g>
  ));
  for (const x of [0.12, 0.8]) a.box({ x, y: 0.4, z: 0.12, w: 0.08, d: 1.52, h: 0.05, c: shade(DECK, 0.1) });
  for (const x of [0.12, 0.82]) {
    a.box({ x, y: 0.14, z: 0.28, w: 0.06, d: 0.08, h: 0.86, c: IRON });
    a.box({ x, y: 0.22, z: 0.94, w: 0.06, d: 0.46, h: 0.05, c: IRON });
  }
  a.box({ x: 0.08, y: 0.08, z: 1.06, w: 0.84, d: 0.24, h: 0.2, c: C.white });
  a.face(
    "y",
    0.32,
    { u: [0.18, 0.82], z: [1.1, 1.22] },
    <g>
      <rect x={0.18} y={1.1} width={0.64} height={0.12} rx={0.02} fill="#2B3A44" />
      <rect x={0.24} y={1.13} width={0.16} height={0.06} fill={C.egg} />
      <rect x={0.44} y={1.13} width={0.12} height={0.06} fill={C.straw} />
      <circle cx={0.68} cy={1.16} r={0.035} fill={C.blush} />
    </g>,
  );
};

const bicycle: Build = (a) => {
  a.shadow({ x: 0.1, y: 0.36, w: 1.8, d: 0.28 });
  const FRAME = C.eggDeep;
  const wheel = (cx: number) => (
    <g>
      {[0, 30, 60, 90, 120, 150].map((d) => {
        const [c, s] = [0.25 * Math.cos(rad(d)), 0.25 * Math.sin(rad(d))];
        return <line key={d} x1={cx + c} y1={0.32 + s} x2={cx - c} y2={0.32 - s} stroke="#B9C0C5" strokeWidth={0.008} />;
      })}
      <circle cx={cx} cy={0.32} r={0.285} fill="none" stroke="#3A3F45" strokeWidth={0.05} />
      <circle cx={cx} cy={0.32} r={0.03} fill="#9AA0A6" />
    </g>
  );
  card(a, "y", 0.5, [0.1, 1.9], [0, 1], () => (
    <g strokeLinecap="round" strokeLinejoin="round">
      {wheel(0.42)}
      {wheel(1.58)}
      <path d="M 0.72 0.3 L 0.66 0.02" stroke={IRON} strokeWidth={0.02} />
      <path
        d="M 0.42 0.32 L 0.92 0.28 L 0.82 0.72 M 0.42 0.32 L 0.84 0.66 L 1.41 0.7 M 0.92 0.28 L 1.44 0.62 L 1.4 0.76 M 1.44 0.62 Q 1.5 0.45 1.58 0.32"
        fill="none"
        stroke={FRAME}
        strokeWidth={0.045}
      />
      <path d="M 0.82 0.72 L 0.8 0.8" stroke="#9AA0A6" strokeWidth={0.03} />
      <path d="M 0.66 0.82 Q 0.78 0.87 0.9 0.82 Q 0.78 0.78 0.66 0.82 Z" fill="#7A4E33" stroke="#5A3826" strokeWidth={0.015} />
      <path d="M 1.4 0.76 L 1.38 0.86 L 1.26 0.88" fill="none" stroke={INK} strokeWidth={0.03} />
      <circle cx={0.92} cy={0.28} r={0.07} fill="none" stroke={IRON} strokeWidth={0.02} />
      <path d="M 0.92 0.28 L 0.98 0.15 M 0.94 0.15 H 1.03" stroke={INK} strokeWidth={0.03} />
      {/* 바구니와 꽃 */}
      {[
        [1.53, 0.88, C.blush],
        [1.61, 0.91, C.white],
        [1.69, 0.88, C.straw],
      ].map(([x, z, c]) => (
        <circle key={x} cx={x} cy={z} r={0.04} fill={c as string} stroke={shade(c as string, -0.3)} strokeWidth={0.01} />
      ))}
      <path d="M 1.46 0.85 L 1.77 0.85 L 1.73 0.66 L 1.5 0.66 Z" fill={C.straw} stroke={shade(C.straw, -0.35)} strokeWidth={0.015} />
      <path d="M 1.48 0.755 H 1.75 M 1.56 0.66 V 0.85 M 1.62 0.66 V 0.85 M 1.68 0.66 V 0.85" stroke={shade(C.straw, -0.3)} strokeWidth={0.008} />
    </g>
  ));
};

const TENT = "#F2E8D8";

const tent: Build = (a) => {
  a.shadow({ x: 0.08, y: 0.08, w: 1.88, d: 1.88 });
  const [y0, y1, top] = [0.12, 1.88, 1.45];
  const [FL, FR, FT, BL, BR, BT]: P3[] = [
    [0.12, y1, 0],
    [1.88, y1, 0],
    [1, y1, top],
    [0.12, y0, 0],
    [1.88, y0, 0],
    [1, y0, top],
  ];
  // 아랫단 띠: p→q 와 r→s 사이의 10~20%
  const band = (p: P3, q: P3, r: P3, s: P3) => [lerp(p, q, 0.1), lerp(r, s, 0.1), lerp(r, s, 0.2), lerp(p, q, 0.2)];
  const pole = (y: number) => {
    a.line([0.12, y, 0], [1.1, y, 1.62], C.woodDark, 2);
    a.line([1.88, y, 0], [0.9, y, 1.62], C.woodDark, 2);
  };
  pole(0.1);
  solid(
    a,
    [[FL, FR, FT], [BL, BR, BT], [BL, FL, FT, BT], [BR, FR, FT, BT], band(BL, BT, FL, FT), band(BR, BT, FR, FT), band(FL, FT, FR, FT), band(BL, BT, BR, BT)],
    [TENT, TENT, TENT, TENT, C.egg, C.egg, C.egg, C.egg],
  );
  // 열린 문과 안의 방석
  a.face(
    "y",
    y1,
    { u: [0.3, 1.7], z: [0, top] },
    <g stroke={shade(TENT, -0.3)} strokeWidth={0.012}>
      <path d="M 0.5 0 L 1 1.12 L 1.5 0 Z" fill="#6E5446" stroke="none" />
      <ellipse cx={1} cy={0.12} rx={0.34} ry={0.12} fill={C.blush} stroke={shade(C.blush, -0.3)} />
      <ellipse cx={0.9} cy={0.22} rx={0.13} ry={0.09} fill={C.straw} stroke={shade(C.straw, -0.3)} />
      <path d="M 1 1.12 Q 0.62 0.6 0.5 0 L 0.3 0 Q 0.56 0.55 1 1.12 Z" fill={shade(TENT, -0.06)} />
      <path d="M 1 1.12 Q 1.38 0.6 1.5 0 L 1.7 0 Q 1.44 0.55 1 1.12 Z" fill={shade(TENT, -0.12)} />
      <rect x={0.44} y={0.4} width={0.12} height={0.06} fill={C.blush} stroke="none" />
      <rect x={1.44} y={0.4} width={0.12} height={0.06} fill={C.blush} stroke="none" />
    </g>,
  );
  // 용마루를 따라 건 작은 깃발 줄
  a.add([0.95, 0.1, 1.45, 1.05, 1.9, 1.52], (v) => {
    const ys = [0.28, 0.48, 0.68, 0.88, 1.08, 1.28, 1.48, 1.68];
    const z = (y: number) => 1.47 + 0.04 * (y - 1) ** 2;
    return (
      <g>
        <polyline points={v.pts([0.1, ...ys, 1.9].map((y): P3 => [1, y, z(y)]))} fill="none" stroke={C.woodDark} strokeWidth={0.6} />
        {ys.map((y, i) => {
          const [m, n] = [v.p(1, y - 0.07, z(y)), v.p(1, y + 0.07, z(y))];
          return <path key={y} d={`M ${m[0]} ${m[1]} L ${n[0]} ${n[1]} L ${(m[0] + n[0]) / 2} ${(m[1] + n[1]) / 2 + 5} Z`} fill={[C.blush, C.straw, C.egg, "#B07BB8"][i % 4]} />;
        })}
      </g>
    );
  });
  pole(1.9);
};

// ── 여행과 계절 ──────────────────────────────────────────────────

const suitcase: Build = (a) => {
  const c = "#86B9C9";
  a.shadow({ x: 0.16, y: 0.3, w: 0.68, d: 0.42 });
  for (const x of [0.26, 0.74]) for (const y of [0.38, 0.62]) a.cyl({ cx: x, cy: y, r: 0.035, h: 0.06, c: INK });
  a.box({ x: 0.2, y: 0.34, z: 0.06, w: 0.6, d: 0.32, h: 0.66, c });
  // 위의 짧은 손잡이, 뒤로 뺀 긴 손잡이
  a.box({ x: 0.42, y: 0.47, z: 0.72, w: 0.16, d: 0.06, h: 0.03, c: IRON });
  for (const x of [0.33, 0.64]) a.box({ x, y: 0.355, z: 0.72, w: 0.03, d: 0.03, h: 0.12, c: "#9AA0A6" });
  a.box({ x: 0.31, y: 0.35, z: 0.84, w: 0.38, d: 0.04, h: 0.04, c: IRON });
  // 골과 스티커
  a.face(
    "y",
    0.66,
    { u: [0.22, 0.78], z: [0.1, 0.68] },
    <g>
      {[0.3, 0.4, 0.6, 0.7].map((x) => (
        <line key={x} x1={x} y1={0.1} x2={x} y2={0.68} stroke={shade(c, -0.14)} strokeWidth={0.014} />
      ))}
      <circle cx={0.42} cy={0.5} r={0.085} fill={C.straw} />
      <polygon points={star(0.42, 0.5, 0.06, true)} fill="#fff" />
      <g transform="rotate(-8 0.61 0.28)">
        <rect x={0.52} y={0.22} width={0.18} height={0.12} rx={0.015} fill={C.white} />
        <rect x={0.52} y={0.265} width={0.18} height={0.03} fill={C.blush} />
      </g>
      <path d="M 0.31 0.2 C 0.25 0.24 0.26 0.31 0.29 0.31 C 0.3 0.31 0.31 0.3 0.31 0.29 C 0.31 0.3 0.32 0.31 0.33 0.31 C 0.36 0.31 0.37 0.24 0.31 0.2 Z" fill={C.blush} />
    </g>,
  );
  a.face(
    "x",
    0.8,
    { u: [0.38, 0.62], z: [0.2, 0.6] },
    <g>
      <circle cx={0.5} cy={0.48} r={0.07} fill={C.blush} />
      <rect x={0.4} y={0.22} width={0.2} height={0.1} rx={0.015} fill={C.white} stroke={C.eggDeep} strokeWidth={0.015} />
    </g>,
  );
};

/** 리본 두른 상자 하나 */
function gift(a: Art, x: number, y: number, z: number, w: number, d: number, h: number, c: string, rib: string, knot = true) {
  a.box({ x, y, z, w, d, h, c });
  const [cx, cy, t] = [x + w / 2, y + d / 2, 0.03];
  const band = (u: number) => <rect x={u - t} y={z} width={t * 2} height={h} fill={rib} />;
  a.face("y", y + d, { u: [cx - t, cx + t], z: [z, z + h] }, band(cx));
  a.face("y", y, { u: [cx - t, cx + t], z: [z, z + h], out: -1 }, band(cx));
  a.face("x", x + w, { u: [cy - t, cy + t], z: [z, z + h] }, band(cy));
  a.face("x", x, { u: [cy - t, cy + t], z: [z, z + h], out: -1 }, band(cy));
  a.add([x, y, z + h, x + w, y + d, z + h], (v) => {
    const q = (x0: number, x1: number, y0: number, y1: number) => v.pts([[x0, y0, z + h], [x1, y0, z + h], [x1, y1, z + h], [x0, y1, z + h]]);
    return (
      <g fill={rib}>
        <polygon points={q(x, x + w, cy - t, cy + t)} />
        <polygon points={q(cx - t, cx + t, y, y + d)} />
      </g>
    );
  });
  if (knot) a.dot(cx, cy, z + h, (sx, sy) => bow(sx, sy - 2, rib, 0.8), 0.06, 0.1);
}

const giftBoxes: Build = (a) => {
  a.shadow({ x: 0.06, y: 0.1, w: 0.9, d: 0.82 });
  gift(a, 0.1, 0.14, 0, 0.46, 0.44, 0.34, C.blush, C.white, false);
  gift(a, 0.18, 0.22, 0.34, 0.3, 0.28, 0.18, C.egg, C.straw);
  gift(a, 0.6, 0.5, 0, 0.32, 0.36, 0.26, C.straw, "#D9665B");
};

const TREE = "#3F7D4E";
const BULBS = ["#FFD36B", "#FF9AA2", "#9FE3D6", "#FFF4D6"];

const xmasTree: Build = (a, { night }) => {
  roundShadow(a, 0.5, 0.5, 0.42);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.17, h: 0.2, c: "#C96A52", taper: -0.03 });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.2, r: 0.05, h: 0.12, c: C.woodDark });
  const tiers: [number, number, number, number][] = [
    [0.28, 0.42, 0.6, 0.36],
    [0.62, 0.34, 0.54, 0.34],
    [0.94, 0.26, 0.48, 0.4],
    [1.24, 0.18, 0.46, 0.55],
  ];
  tiers.forEach(([z, r, h, reach], i) => {
    a.cyl({ cx: 0.5, cy: 0.5, z, r, h, c: TREE, taper: r - 0.02 });
    // 장식은 돌린 방향의 앞쪽(+X+Y)에만: 나무는 둥글어 어느 방향에서도 같다.
    a.add([0.5 - r, 0.5 - r, z, 0.5 + r, 0.5 + r, z + h], (v) => {
      const [X, Y] = v.at(0.5, 0.5);
      const pt = (deg: number, t: number) => {
        const [rr, ang] = [r - (r - 0.02) * t, rad(45 + deg)];
        return proj(X + rr * Math.cos(ang), Y + rr * Math.sin(ang), z + t * h);
      };
      const wire = [-80, -60, -40, -20, 0, 20, 40, 60, 80].map((deg, k) => pt(deg, 0.1 + (reach - 0.12) * (k / 8)));
      return (
        <g>
          <polyline points={wire.map((p) => p.join(",")).join(" ")} fill="none" stroke="#2B5A36" strokeWidth={0.5} />
          {wire.map(([x, y], k) =>
            k % 2 ? null : (
              <g key={k}>
                {night && <circle cx={x} cy={y} r={3.4} fill={BULBS[(k / 2 + i) % 4]} opacity={0.45} />}
                <circle cx={x} cy={y} r={night ? 1.5 : 1.2} fill={night ? "#FFFBEA" : BULBS[(k / 2 + i) % 4]} stroke={night ? BULBS[(k / 2 + i) % 4] : "none"} strokeWidth={0.8} />
              </g>
            ),
          )}
          {[
            [-35, reach * 0.55],
            [45, reach * 0.35],
          ].map(([deg, t], k) => {
            const [x, y] = pt(deg, t);
            const c = ["#D9665B", "#E7B74A", "#5B8FB0", "#D9665B"][(i + k * 2) % 4];
            return (
              <g key={deg}>
                <circle cx={x} cy={y + 1} r={1.9} fill={c} stroke={shade(c, -0.3)} strokeWidth={0.5} />
                <circle cx={x - 0.6} cy={y + 0.4} r={0.6} fill="#fff" opacity={0.6} />
              </g>
            );
          })}
        </g>
      );
    });
  });
  a.dot(
    0.5,
    0.5,
    1.7,
    (x, y) => (
      <g>
        {night && <circle cx={x} cy={y - 4} r={8} fill="#FFE9A8" opacity={0.45} />}
        <polygon points={star(x, y - 4, 5.2)} fill={night ? "#FFE36B" : "#F2C94C"} stroke={shade("#F2C94C", -0.35)} strokeWidth={0.6} />
      </g>
    ),
    0.08,
    0.3,
  );
  gift(a, 0.72, 0.72, 0, 0.18, 0.18, 0.13, C.blush, C.white);
};

/** 호박 하나(화면 가운데 x, y 에 가로 rx, 세로 ry px) */
const pumpkinBody = (x: number, y: number, rx: number, ry: number, c: string) => (
  <g stroke={shade(c, -0.32)} strokeWidth={0.6}>
    {[-0.55, 0.55, -0.28, 0.28, 0].map((o) => (
      <ellipse key={o} cx={x + o * rx} cy={y} rx={rx * (0.5 - Math.abs(o) * 0.1)} ry={ry * (1 - Math.abs(o) * 0.15)} fill={shade(c, -Math.abs(o) * 0.18)} />
    ))}
    <path d={`M ${x} ${y - ry + 1} q -0.6 -3 1.6 -4.4`} stroke="#6B5A2E" strokeWidth={2} fill="none" strokeLinecap="round" />
  </g>
);

const pumpkin: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.4);
  cluster(a, [0.08, 0.08, 0, 0.94, 0.94, 0.6], [
    {
      p: [0.4, 0.42, 0.2],
      draw: (x, y) => (
        <>
          {pumpkinBody(x, y, 13, 7.6, "#E8833A")}
          <ellipse cx={x + 4.5} cy={y - 8.6} rx={3.2} ry={1.4} transform={`rotate(-18 ${x + 4.5} ${y - 8.6})`} fill={C.leaf} stroke={C.leafDark} strokeWidth={0.5} />
          <path d={`M ${x - 1} ${y - 7} q -3 -1 -3.4 -3.4 q 0 -1.6 1.4 -1`} stroke={C.leafDark} strokeWidth={0.6} fill="none" />
        </>
      ),
    },
    { p: [0.76, 0.7, 0.12], draw: (x, y) => pumpkinBody(x, y, 8, 4.8, "#F1E3C8") },
  ]);
};

const VASE = "#D8A68A";

const vasePampas: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.2);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.15, h: 0.3, c: VASE, taper: 0.05 });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.3, r: 0.1, h: 0.09, c: VASE, taper: -0.025, top: shade(VASE, -0.4) });
  // 솜털 이삭: 줄기 끝에서 바깥으로 조금 더 눕고, 겹친 동그라미가 끝으로 갈수록 가늘어진다.
  a.dot(
    0.5,
    0.5,
    0.39,
    (x, y) => (
      <g>
        {(
          [
            [-34, 8, 12, "#E6D4B2"],
            [38, 6, 11, "#EFE3CB"],
            [-15, 11, 13, "#F3EADA"],
            [20, 10, 13, "#DCC6A0"],
            [2, 12, 13, "#EFE3CB"],
          ] as const
        ).map(([d, s, l, c]) => {
          const dir = (g: number): [number, number] => [Math.sin(rad(g)), -Math.cos(rad(g))];
          const [dx, dy] = dir(d);
          const [ex, ey] = dir(d * 1.25);
          const [sx, sy] = [x + dx * s, y + dy * s];
          const fluff = [2.2, 3, 3.2, 3, 2.6, 2, 1.3].map((r, k): [number, number, number] => [sx + (ex * l * k) / 6, sy + (ey * l * k) / 6, r]);
          return (
            <g key={d}>
              <path d={`M ${x + dx} ${y - 1} Q ${x + dx * s * 0.2} ${y + dy * s * 0.6} ${sx} ${sy}`} stroke="#B89F78" strokeWidth={0.7} fill="none" />
              {fluff.map(([fx, fy, r], k) => (
                <circle key={`s${k}`} cx={fx + 0.5} cy={fy + 0.6} r={r} fill={shade(c, -0.14)} />
              ))}
              {fluff.map(([fx, fy, r], k) => (
                <circle key={k} cx={fx} cy={fy} r={r} fill={c} />
              ))}
              {fluff.slice(1, 5).map(([fx, fy, r], k) => (
                <circle key={`h${k}`} cx={fx - r * 0.35} cy={fy - r * 0.3} r={r * 0.4} fill="#fff" opacity={0.4} />
              ))}
            </g>
          );
        })}
      </g>
    ),
    0.3,
    0.92,
  );
};

// ── 식물 ─────────────────────────────────────────────────────────

const FIG = ["#2F5E35", "#3D6E3A", "#4C7A3C"];

const treeFig: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.26);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.21, h: 0.4, c: "#E8E1D3", taper: -0.03, top: C.soil });
  // 줄기 마디와 잎을 돌린 방향에서 먼 것부터
  a.add([0.08, 0.08, 0.4, 0.92, 0.92, 1.9], (v) => {
    const parts: { d: number; n: ReactNode }[] = [];
    const [X, Y] = v.at(0.5, 0.5);
    for (let z = 0.4; z < 1.66; z += 0.12) {
      const [m, n] = [v.p(0.5, 0.5, z), v.p(0.5, 0.5, z + 0.13)];
      parts.push({ d: X + Y + z + 0.06, n: <line x1={m[0]} y1={m[1]} x2={n[0]} y2={n[1]} stroke="#7A5A3C" strokeWidth={2.4 - z * 0.7} strokeLinecap="round" /> });
    }
    for (let i = 0; i < 18; i++) {
      const [z, th, L] = [0.6 + i * 0.062, rad(i * 137.5 + 20), 0.36 - i * 0.008];
      const b: P3 = [0.5 + 0.03 * Math.cos(th), 0.5 + 0.03 * Math.sin(th), z];
      const t: P3 = [0.5 + L * Math.cos(th), 0.5 + L * Math.sin(th), z + (i < 9 ? -0.02 : 0.08)];
      const [p, q] = [v.p(...b), v.p(...t)];
      const [mx, my, len] = [(p[0] + q[0]) / 2, (p[1] + q[1]) / 2, Math.hypot(q[0] - p[0], q[1] - p[1])];
      const ang = (Math.atan2(q[1] - p[1], q[0] - p[0]) * 180) / Math.PI;
      const [cX, cY] = v.at((b[0] + t[0]) / 2, (b[1] + t[1]) / 2);
      const c = FIG[i % 3];
      parts.push({
        d: cX + cY + (b[2] + t[2]) / 2,
        n: (
          <g transform={`rotate(${ang} ${mx} ${my})`}>
            <ellipse cx={mx} cy={my} rx={Math.max(len / 2 + 1.5, 4.5)} ry={3.6} fill={c} stroke={shade(c, -0.3)} strokeWidth={0.5} />
            <line x1={mx - len / 2} y1={my} x2={mx + len / 2} y2={my} stroke="#7FA36B" strokeWidth={0.5} />
            <ellipse cx={mx + 1} cy={my - 1.5} rx={len / 4 + 0.6} ry={0.8} fill="#fff" opacity={0.22} />
          </g>
        ),
      });
    }
    const [tx, ty] = v.p(0.5, 0.5, 1.66);
    parts.push({ d: X + Y + 1.8, n: <ellipse cx={tx} cy={ty - 3} rx={1.3} ry={3.4} fill="#C25B4E" /> });
    return <g>{parts.sort((m, n) => m.d - n.d).map((p, i) => <g key={i}>{p.n}</g>)}</g>;
  });
};

const bonsai: Build = (a) => {
  a.shadow({ x: 0.12, y: 0.22, w: 0.76, d: 0.56 });
  const POT = "#4F6B7A";
  a.box({ x: 0.22, y: 0.32, w: 0.56, d: 0.36, h: 0.07, c: POT });
  a.box({ x: 0.2, y: 0.3, z: 0.07, w: 0.6, d: 0.4, h: 0.03, c: shade(POT, 0.1), top: "#6B5A3E" });
  a.dot(
    0.36,
    0.6,
    0.1,
    (x, y) => (
      <>
        <ellipse cx={x} cy={y} rx={5} ry={1.8} fill="#7FA35A" />
        <ellipse cx={x + 9} cy={y - 2} rx={2} ry={1.1} fill="#D9D4C7" stroke="#A9A28F" strokeWidth={0.4} />
      </>
    ),
    0.12,
    0.02,
  );
  const trunk: P3[] = [
    [0.47, 0.52, 0.1],
    [0.41, 0.5, 0.2],
    [0.47, 0.48, 0.3],
    [0.58, 0.5, 0.37],
    [0.54, 0.5, 0.46],
  ];
  a.add(boundsOf(trunk), (v) => (
    <g fill="none" stroke="#6B4A33" strokeLinecap="round" strokeLinejoin="round">
      <polyline points={v.pts(trunk)} strokeWidth={3.6} />
      <polyline points={v.pts(trunk.slice(0, 3))} strokeWidth={1} stroke="#9A7552" />
      <polyline points={v.pts([[0.42, 0.5, 0.22], [0.31, 0.55, 0.3]])} strokeWidth={1.6} />
      <polyline points={v.pts([[0.57, 0.5, 0.37], [0.68, 0.47, 0.4]])} strokeWidth={1.4} />
    </g>
  ));
  const pad = (s: number) => (x: number, y: number) => (
    <g>
      <ellipse cx={x} cy={y} rx={9 * s} ry={3.6 * s} fill="#4E7A3E" stroke="#3B5E2F" strokeWidth={0.5} />
      <ellipse cx={x - 3 * s} cy={y - 2.2 * s} rx={4.6 * s} ry={2.8 * s} fill="#6E9E58" />
      <ellipse cx={x + 3.2 * s} cy={y - 1.8 * s} rx={4.2 * s} ry={2.6 * s} fill="#6E9E58" />
      <ellipse cx={x - s} cy={y - 3.4 * s} rx={2.6 * s} ry={1.4 * s} fill="#8DBF6A" />
    </g>
  );
  cluster(a, [0.2, 0.3, 0.28, 0.8, 0.7, 0.62], [
    { p: [0.31, 0.55, 0.32], draw: pad(0.6) },
    { p: [0.68, 0.47, 0.41], draw: pad(0.7) },
    { p: [0.53, 0.5, 0.48], draw: pad(0.85) },
  ]);
};

/** 부채꼴로 벌린 잎 n 장(화면 좌표, 길이 len px) */
const sprout = (x: number, y: number, n: number, len: number, c: string) =>
  Array.from({ length: n }, (_, i) => {
    const deg = n > 1 ? -55 + (110 * i) / (n - 1) : 0;
    const [cx, cy] = [x + Math.sin(rad(deg)) * len * 0.5, y - Math.cos(rad(deg)) * len * 0.5];
    return <ellipse key={i} cx={cx} cy={cy} rx={len * 0.18} ry={len * 0.5} transform={`rotate(${deg} ${cx} ${cy})`} fill={c} stroke={shade(c, -0.3)} strokeWidth={0.4} />;
  });

const terrarium: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.3);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.27, h: 0.06, c: C.wood });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.06, r: 0.24, h: 0.04, c: "#D9CBB0" });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.1, r: 0.24, h: 0.05, c: C.soil, top: "#7FA35A" });
  cluster(a, [0.28, 0.28, 0.15, 0.72, 0.72, 0.45], [
    { p: [0.4, 0.42, 0.15], draw: (x, y) => <g>{sprout(x, y, 5, 9, C.leaf)}</g> },
    {
      p: [0.62, 0.44, 0.15],
      draw: (x, y) => (
        <g>
          {sprout(x, y, 4, 5, "#8FB9A8")}
          <ellipse cx={x} cy={y - 1.6} rx={2} ry={1.4} fill="#A9CDBE" />
        </g>
      ),
    },
    {
      p: [0.42, 0.62, 0.15],
      draw: (x, y) => (
        <g>
          <rect x={x - 0.9} y={y - 4} width={1.8} height={4} fill={C.white} />
          <path d={`M ${x - 3.6} ${y - 3.6} Q ${x} ${y - 9} ${x + 3.6} ${y - 3.6} Z`} fill="#E0675F" stroke={shade("#E0675F", -0.3)} strokeWidth={0.4} />
          <circle cx={x - 1.2} cy={y - 5.6} r={0.6} fill="#fff" />
          <circle cx={x + 1.4} cy={y - 4.8} r={0.5} fill="#fff" />
        </g>
      ),
    },
    {
      p: [0.62, 0.64, 0.15],
      draw: (x, y) => (
        <g fill="#C9C2B0" stroke="#9E978A" strokeWidth={0.4}>
          <ellipse cx={x - 2} cy={y} rx={2.4} ry={1.5} />
          <ellipse cx={x + 1.8} cy={y + 0.6} rx={1.6} ry={1.1} fill="#E1DACA" />
        </g>
      ),
    },
  ]);
  // 유리 덮개는 맨 나중에
  a.add([0.24, 0.24, 0.06, 0.76, 0.76, 0.64], (v) => {
    const [sx, s0] = v.p(0.5, 0.5, 0.08);
    const [, s1] = v.p(0.5, 0.5, 0.3);
    const [rx, ry, rd] = [0.25 * K, 0.125 * K, 0.6 * 0.25 * K];
    return (
      <g>
        <path
          d={`M ${sx - rx} ${s1} L ${sx - rx} ${s0} A ${rx} ${ry} 0 0 0 ${sx + rx} ${s0} L ${sx + rx} ${s1} A ${rx} ${rd} 0 0 0 ${sx - rx} ${s1} Z`}
          fill="rgba(205,232,240,0.28)"
          stroke="rgba(110,150,160,0.8)"
          strokeWidth={0.7}
        />
        <path d={`M ${sx - rx * 0.62} ${s0 - 1} L ${sx - rx * 0.62} ${s1} Q ${sx - rx * 0.55} ${s1 - rd * 0.75} ${sx - rx * 0.1} ${s1 - rd * 0.92}`} fill="none" stroke="#fff" strokeWidth={1.3} opacity={0.7} strokeLinecap="round" />
        <circle cx={sx} cy={s1 - rd - 1.6} r={2} fill={C.woodDark} />
      </g>
    );
  });
};

// ── 반려동물 ─────────────────────────────────────────────────────

const dogHouse: Build = (a) => {
  a.shadow({ x: 0.06, y: 0.12, w: 0.9, d: 0.84 });
  const [WALL, ROOF] = ["#E8C9A0", "#C9604E"];
  const [y0, y1] = [0.22, 0.78];
  const gable = (y: number): P3[] => [
    [0.18, y, 0],
    [0.82, y, 0],
    [0.82, y, 0.6],
    [0.5, y, 0.856],
    [0.18, y, 0.6],
  ];
  solid(a, loft(gable(y1), gable(y0)), WALL);
  // 판자 줄, 둥근 문, 문패
  const planks = (u0: number, u1: number) =>
    [0.15, 0.3, 0.45].map((z) => <line key={z} x1={u0} y1={z} x2={u1} y2={z} stroke={shade(WALL, -0.2)} strokeWidth={0.012} />);
  a.face(
    "y",
    y1,
    { u: [0.18, 0.82], z: [0, 0.72] },
    <g>
      {planks(0.18, 0.82)}
      <path d="M 0.35 0 L 0.35 0.27 C 0.35 0.47 0.65 0.47 0.65 0.27 L 0.65 0 Z" fill={shade(WALL, -0.25)} />
      <path d="M 0.37 0 L 0.37 0.27 C 0.37 0.44 0.63 0.44 0.63 0.27 L 0.63 0 Z" fill="#4A3428" />
      <rect x={0.42} y={0.6} width={0.16} height={0.09} rx={0.02} fill={C.white} stroke={C.woodDark} strokeWidth={0.01} />
      <circle cx={0.5} cy={0.645} r={0.022} fill={C.blush} />
    </g>,
  );
  a.face("y", y0, { u: [0.18, 0.82], z: [0, 0.5], out: -1 }, <g>{planks(0.18, 0.82)}</g>);
  a.face("x", 0.82, { u: [y0, y1], z: [0, 0.5] }, <g>{planks(y0, y1)}</g>);
  a.face("x", 0.18, { u: [y0, y1], z: [0, 0.5], out: -1 }, <g>{planks(y0, y1)}</g>);
  // 지붕 두 장: 처마 0.06/0.94, 용마루 0.5
  const slab = (eave: number) => {
    const sec = (y: number): P3[] => [
      [eave, y, 0.504],
      [0.5, y, 0.856],
      [0.5, y, 0.926],
      [eave, y, 0.574],
    ];
    solid(a, loft(sec(0.14), sec(0.86)), ROOF);
  };
  slab(0.06);
  slab(0.94);
  a.cyl({ cx: 0.82, cy: 0.9, r: 0.07, h: 0.05, c: C.blush, top: "#8A5A3C" });
  a.dot(
    0.24,
    0.9,
    0,
    (x, y) => (
      <g transform={`rotate(-18 ${x} ${y})`} fill="#F6F1E6" stroke="#C9C2B0" strokeWidth={0.5}>
        <rect x={x - 4} y={y - 1} width={8} height={2} />
        {[-4, 4].flatMap((dx) => [-1, 1].map((dy) => <circle key={`${dx}${dy}`} cx={x + dx} cy={y + dy} r={1.3} />))}
      </g>
    ),
    0.08,
    0.03,
  );
};

const catBed: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.42);
  const [RIM, PAD] = ["#E9AFA6", "#FBEDE4"];
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.4, h: 0.16, c: RIM });
  a.dot(
    0.5,
    0.5,
    0.16,
    (x, y) => (
      <g>
        <ellipse cx={x} cy={y} rx={12.6} ry={6.2} fill="none" stroke="#fff" strokeWidth={1} opacity={0.35} />
        <ellipse cx={x} cy={y + 0.4} rx={9.8} ry={4.8} fill={shade(PAD, -0.16)} />
        <ellipse cx={x} cy={y + 1.6} rx={9} ry={3.9} fill={PAD} />
      </g>
    ),
    0.4,
    0.02,
  );
  a.dot(
    0.86,
    0.86,
    0,
    (x, y) => (
      <g stroke="#B8902E" strokeWidth={0.5} fill="none">
        <circle cx={x} cy={y - 2.8} r={2.8} fill={C.straw} />
        <path d={`M ${x - 2.4} ${y - 4} q 2.4 1.6 4.8 0 M ${x - 2.6} ${y - 2.4} q 2.6 1.8 5.2 0`} />
        <path d={`M ${x + 2.6} ${y - 1.6} q 3 1 2 3 q -1 2 2 2.4`} />
      </g>
    ),
    0.06,
    0.2,
  );
};

// ── 생활 ─────────────────────────────────────────────────────────

const PAGES = "#F6EFDF";

const bookStack: Build = (a) => {
  a.shadow({ x: 0.18, y: 0.2, w: 0.64, d: 0.62 });
  let z = 0;
  // 가운데, 넓이, 두께, 기운 각도, 표지 색, 책등이 있는 옆면(0 뒤, 1 오른쪽, 2 앞, 3 왼쪽)
  for (const [cx, cy, w, d, t, deg, c, spine] of [
    [0.5, 0.5, 0.6, 0.44, 0.08, 4, "#5B8FB0", 3],
    [0.48, 0.52, 0.54, 0.4, 0.07, -8, "#E7B74A", 2],
    [0.52, 0.5, 0.58, 0.42, 0.09, 10, "#D9665B", 1],
    [0.5, 0.48, 0.5, 0.38, 0.06, -3, "#6E9E58", 2],
    [0.51, 0.5, 0.46, 0.36, 0.07, 14, "#B07BB8", 3],
  ] as const) {
    const base = turned(cx, cy, w, d, deg);
    solid(a, loft(at(base, z), at(base, z + t)), [c, c, ...[0, 1, 2, 3].map((i) => (i === spine ? c : PAGES))]);
    z += t;
  }
  a.cyl({ cx: 0.5, cy: 0.5, z, r: 0.075, h: 0.11, c: C.white, top: "#8A5A3C" });
  a.add([0.575, 0.49, z + 0.02, 0.62, 0.51, z + 0.09], (v) => {
    const [sx, sy] = v.p(0.6, 0.5, z + 0.055);
    return <ellipse cx={sx} cy={sy} rx={2.2} ry={2.6} fill="none" stroke="#C9C6BE" strokeWidth={1.2} />;
  });
};

const chessTable: Build = (a) => {
  a.shadow({ x: 0.12, y: 0.12, w: 0.76, d: 0.76 });
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.22, h: 0.04, c: C.woodDark });
  a.cyl({ cx: 0.5, cy: 0.5, z: 0.04, r: 0.05, h: 0.54, c: C.woodDark });
  a.box({ x: 0.12, y: 0.12, z: 0.58, w: 0.76, d: 0.76, h: 0.06, c: C.wood });
  const s = 0.075;
  a.add([0.18, 0.18, 0.64, 0.82, 0.82, 0.64], (v) => {
    const sq = (x: number, y: number, w: number) => v.pts([[x, y, 0.64], [x + w, y, 0.64], [x + w, y + w, 0.64], [x, y + w, 0.64]]);
    return (
      <g>
        <polygon points={sq(0.18, 0.18, 0.64)} fill={C.woodDark} />
        {Array.from({ length: 64 }, (_, k) => (
          <polygon key={k} points={sq(0.2 + (k % 8) * s, 0.2 + Math.floor(k / 8) * s, s)} fill={(k + Math.floor(k / 8)) % 2 ? "#8A5A3C" : "#F3E6CC"} />
        ))}
      </g>
    );
  });
  // 말: 흰 말이 앞(+y), 검은 말이 뒤
  const piece = (i: number, j: number, white: boolean, kind: "p" | "r" | "q" | "k") => {
    const [f, st] = white ? ["#FBF7EE", "#A8A090"] : ["#3A3F45", "#1E2226"];
    const tall = { p: 4, r: 5, q: 6.5, k: 7 }[kind];
    a.dot(
      0.2 + (i + 0.5) * s,
      0.2 + (j + 0.5) * s,
      0.64,
      (x, y) => (
        <g fill={f} stroke={st} strokeWidth={0.5}>
          <ellipse cx={x} cy={y} rx={2.2} ry={1.1} />
          <path d={`M ${x - 1.4} ${y} L ${x - 0.8} ${y - tall} L ${x + 0.8} ${y - tall} L ${x + 1.4} ${y} Z`} />
          {kind === "r" ? <rect x={x - 1.5} y={y - tall - 1.6} width={3} height={1.8} /> : <circle cx={x} cy={y - tall - 0.8} r={kind === "p" ? 1.5 : 1.2} />}
          {kind === "k" && <path d={`M ${x} ${y - tall - 2} v -2.4 M ${x - 1} ${y - tall - 3.2} h 2`} />}
          {kind === "q" && <circle cx={x} cy={y - tall - 2.6} r={0.6} />}
        </g>
      ),
      0.03,
      (tall + 3) / 28,
    );
  };
  piece(4, 7, true, "k");
  piece(3, 6, true, "p");
  piece(5, 5, true, "p");
  piece(0, 7, true, "r");
  piece(3, 0, false, "q");
  piece(2, 1, false, "p");
  piece(6, 2, false, "p");
  piece(7, 0, false, "r");
};

const punchingBag: Build = (a) => {
  a.shadow();
  const BAG = "#D9675A";
  for (const x of [0.12, 0.8]) a.box({ x, y: 0.08, w: 0.08, d: 0.84, h: 0.05, c: IRON });
  a.box({ x: 0.2, y: 0.08, w: 0.6, d: 0.12, h: 0.05, c: IRON });
  for (const x of [0.3, 0.7]) a.cyl({ cx: x, cy: 0.14, z: 0.05, r: 0.08, h: 0.06, c: "#2E3438" });
  a.box({ x: 0.46, y: 0.1, z: 0.05, w: 0.08, d: 0.08, h: 1.8, c: IRON });
  a.box({ x: 0.46, y: 0.18, z: 1.77, w: 0.08, d: 0.52, h: 0.08, c: IRON });
  // 사슬: 팔 끝의 고리에서 가방 테두리 세 곳으로
  a.add([0.35, 0.56, 1.42, 0.65, 0.77, 1.77], (v) => {
    const [h0, h1] = [v.p(0.5, 0.62, 1.77), v.p(0.5, 0.62, 1.6)];
    return (
      <g stroke="#8A9096" strokeWidth={1} strokeDasharray="1.4 0.8" fill="none">
        <line x1={h0[0]} y1={h0[1]} x2={h1[0]} y2={h1[1]} strokeDasharray="none" strokeWidth={1.6} />
        {(
          [
            [0.36, 0.56],
            [0.64, 0.56],
            [0.5, 0.77],
          ] as const
        ).map(([x, y]) => {
          const e = v.p(x, y, 1.42);
          return <line key={`${x}${y}`} x1={h1[0]} y1={h1[1]} x2={e[0]} y2={e[1]} />;
        })}
      </g>
    );
  });
  a.cyl({ cx: 0.5, cy: 0.62, z: 0.4, r: 0.17, h: 1.02, c: BAG, top: shade(BAG, 0.1) });
  for (const z of [0.5, 1.28]) a.cyl({ cx: 0.5, cy: 0.62, z, r: 0.175, h: 0.06, c: "#3A3F45" });
};

const WICKER = "#D9B77E";

const laundryBasket: Build = (a) => {
  roundShadow(a, 0.5, 0.5, 0.36);
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.3, h: 0.4, c: WICKER, taper: -0.04, top: shade(WICKER, -0.45) });
  // 엮은 줄과 손잡이 구멍: 둥글어서 어느 방향에서도 앞쪽 반만 그린다.
  a.add([0.16, 0.16, 0, 0.84, 0.84, 0.4], (v) => {
    const [sx, sy] = v.p(0.5, 0.5, 0);
    const rAt = (t: number) => (0.3 + 0.04 * t) * K;
    const yAt = (t: number) => sy - 0.4 * 28 * t;
    return (
      <g fill="none" stroke={shade(WICKER, -0.25)} strokeWidth={0.6}>
        {[0.25, 0.5, 0.75].map((t) => (
          <path key={t} d={`M ${sx - rAt(t)} ${yAt(t)} A ${rAt(t)} ${rAt(t) / 2} 0 0 0 ${sx + rAt(t)} ${yAt(t)}`} />
        ))}
        {[-60, -30, 0, 30, 60].map((d) => {
          const [sn, cs] = [Math.sin(rad(d)), Math.cos(rad(d))];
          return <line key={d} x1={sx + rAt(0) * sn} y1={yAt(0) + (rAt(0) / 2) * cs} x2={sx + rAt(1) * sn} y2={yAt(1) + (rAt(1) / 2) * cs} />;
        })}
        {[-1, 1].map((k) => (
          <rect key={k} x={sx + k * rAt(0.85) * 0.8 - 2.2} y={yAt(0.85) + rAt(0.85) * 0.3 - 1} width={4.4} height={2} rx={1} fill={shade(WICKER, -0.5)} stroke="none" />
        ))}
      </g>
    );
  });
  // 빨래: 위로 솟고 앞 테두리 너머로 소매 하나가 늘어진다.
  a.dot(
    0.5,
    0.5,
    0.4,
    (x, y) => (
      <g strokeWidth={0.5}>
        <ellipse cx={x - 4.5} cy={y - 1.5} rx={7} ry={3.8} fill={C.egg} stroke={shade(C.egg, -0.3)} />
        <ellipse cx={x + 4.5} cy={y - 2.5} rx={6.5} ry={3.6} fill={C.blush} stroke={shade(C.blush, -0.3)} />
        <ellipse cx={x} cy={y - 4.5} rx={5} ry={2.8} fill={C.white} stroke="#C9C6BE" />
        <path d={`M ${x - 3} ${y - 5.5} h 6 M ${x - 3.5} ${y - 4} h 7`} stroke={C.slate} opacity={0.5} />
        <path d={`M ${x - 13.4} ${y} A 13.4 6.7 0 0 0 ${x + 13.4} ${y}`} fill="none" stroke={shade(WICKER, 0.1)} strokeWidth={1.6} />
        <path d={`M ${x + 3} ${y + 4} q 2 3 1 7 l 3.4 0.4 q 0.6 -4 -1.4 -7.6 Z`} fill={C.blush} stroke={shade(C.blush, -0.3)} />
      </g>
    ),
    0.34,
    0.16,
  );
};

const trashBin: Build = (a) => {
  a.shadow({ x: 0.1, y: 0.16, w: 0.84, d: 0.76 });
  const [GRAY, MINT] = ["#BFC7CC", C.eggDeep];
  // 일반 쓰레기: 발판 달린 둥근 통
  a.cyl({ cx: 0.3, cy: 0.38, r: 0.17, h: 0.38, c: GRAY, taper: -0.02 });
  a.cyl({ cx: 0.3, cy: 0.38, z: 0.38, r: 0.2, h: 0.04, c: shade(GRAY, -0.18) });
  a.cyl({ cx: 0.3, cy: 0.38, z: 0.42, r: 0.05, h: 0.03, c: shade(GRAY, -0.18) });
  a.box({ x: 0.25, y: 0.575, w: 0.1, d: 0.06, h: 0.03, c: IRON });
  // 분리수거: 투입구 뚫린 네모 통
  a.box({ x: 0.54, y: 0.48, w: 0.34, d: 0.38, h: 0.36, c: MINT });
  a.box({ x: 0.52, y: 0.46, z: 0.36, w: 0.38, d: 0.42, h: 0.05, c: shade(MINT, 0.18) });
  a.add([0.62, 0.62, 0.41, 0.8, 0.7, 0.41], (v) => <polygon points={v.pts([[0.62, 0.62, 0.41], [0.8, 0.62, 0.41], [0.8, 0.7, 0.41], [0.62, 0.7, 0.41]])} fill={shade(MINT, -0.4)} />);
  const mark = (u: number) => <path d={`M ${u} 0.28 L ${u + 0.07} 0.16 L ${u - 0.07} 0.16 Z`} fill="none" stroke="#fff" strokeWidth={0.022} strokeLinejoin="round" strokeDasharray="0.09 0.03" />;
  a.face("y", 0.86, { u: [0.62, 0.8], z: [0.14, 0.3] }, mark(0.71));
  a.face("x", 0.88, { u: [0.58, 0.76], z: [0.14, 0.3] }, mark(0.67));
};

// ── 그림 고르기 ──────────────────────────────────────────────────

export const BUILDS: Record<string, Build> = {
  piano,
  guitar_stand: guitarStand,
  keyboard_synth: keyboardSynth,
  teddy_big: teddyBig,
  plush_pile: plushPile,
  globe,
  telescope,
  treadmill,
  bicycle,
  tent,
  suitcase,
  gift_boxes: giftBoxes,
  xmas_tree: xmasTree,
  pumpkin,
  vase_pampas: vasePampas,
  tree_fig: treeFig,
  bonsai,
  terrarium,
  dog_house: dogHouse,
  cat_bed: catBed,
  book_stack: bookStack,
  chess_table: chessTable,
  punching_bag: punchingBag,
  laundry_basket: laundryBasket,
  trash_bin: trashBin,
};
