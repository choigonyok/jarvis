// 러그·매트 그림. a.floor 안에서 모형 바닥 좌표 (x, y) 로 그리고(네 방향으로는 저절로 돌아간다), 가장자리는 0.06 쯤 비운다.
// 하트·구름·무지개는 돌리지 않은 방향에서 화면에 똑바로 서 보이도록 대각선 틀(UP)에 그린다.

import type { ReactNode } from "react";
import { C, shade, type Build } from "../kit";
import { heart } from "./walls";

/** 2×2 의 가운데가 원점이고 +x 는 화면 오른쪽, +y 는 화면 아래쪽(앞)인 틀. 러그 안은 |x| + |y| ≤ 1.33. */
const UP = "translate(1 1) rotate(-45)";

type Puff = [number, number, number];

/** 동그라미 여럿을 겹친 덩어리: 윤곽을 조금 크게 먼저 깔고 위에 채워서 겹친 자리에 선이 남지 않는다. */
function puffs(list: Puff[], fill: string, edge: string, w = 0.03): ReactNode {
  return (
    <>
      {list.map(([x, y, r], i) => (
        <circle key={`e${i}`} cx={x} cy={y} r={r + w} fill={edge} />
      ))}
      {list.map(([x, y, r], i) => (
        <circle key={`f${i}`} cx={x} cy={y} r={r} fill={fill} />
      ))}
    </>
  );
}

const check: Build = (a) => {
  const [n, x0, edge] = [6, 0.26, "#7F9DBC"];
  const s = (2 - 2 * x0) / n;
  a.floor(
    <>
      <rect x={0.06} y={0.06} width={1.88} height={1.88} rx={0.08} fill={edge} stroke={shade(edge, -0.25)} strokeWidth={0.025} />
      <rect x={0.155} y={0.155} width={1.69} height={1.69} rx={0.03} fill="none" stroke={C.linen} strokeWidth={0.022} strokeDasharray="0.08 0.05" />
      {Array.from({ length: n * n }, (_, i) => (
        <rect key={i} x={x0 + (i % n) * s} y={x0 + Math.floor(i / n) * s} width={s} height={s} fill={(i + Math.floor(i / n)) % 2 ? "#B3CAE0" : C.linen} />
      ))}
    </>,
  );
};

const heartRug: Build = (a) => {
  const pink = "#EE8FA7";
  a.floor(
    <g transform={UP}>
      <path d={heart(0, 0, 0.84)} fill={pink} stroke={shade(pink, -0.25)} strokeWidth={0.025} />
      <path d={heart(0, 0.01, 0.66)} fill="none" stroke="#FCD7E1" strokeWidth={0.045} />
    </g>,
  );
};

const CLOUD: Puff[] = [
  [-0.55, -0.12, 0.33],
  [-0.04, -0.36, 0.42],
  [0.5, -0.17, 0.35],
  [-0.8, 0.08, 0.23],
  [0.8, 0.08, 0.23],
  [-0.45, 0.3, 0.28],
  [0.08, 0.3, 0.34],
  [0.56, 0.28, 0.28],
  [0, 0, 0.5],
];

const cloud: Build = (a) => {
  a.floor(
    <g transform={UP}>
      {puffs(
        CLOUD.map(([x, y, r]) => [x, y + 0.04, r]),
        "#D8E9F5",
        "#A6C4DA",
      )}
      {CLOUD.map(([x, y, r], i) => (
        <circle key={i} cx={x} cy={y - 0.02} r={r - 0.02} fill={C.white} />
      ))}
    </g>,
  );
};

const runner: Build = (a) => {
  const [rust, cream, navy] = ["#CF7A5F", "#F4E6CF", "#4F6B8C"];
  const dia = (cx: number, cy: number, rx: number, ry: number) => `M ${cx} ${cy - ry} L ${cx + rx} ${cy} L ${cx} ${cy + ry} L ${cx - rx} ${cy} Z`;
  const fringe = (y: number) =>
    Array.from({ length: 12 }, (_, i) => <line key={`${y}-${i}`} x1={0.15 + i * 0.064} y1={y} x2={0.15 + i * 0.064} y2={y + 0.1} />);
  const motifs = [0.56, 1.18, 1.82, 2.44];
  a.floor(
    <>
      <g stroke={cream} strokeWidth={0.022} strokeLinecap="round">
        {fringe(0.05)}
        {fringe(2.85)}
      </g>
      <rect x={0.1} y={0.14} width={0.8} height={2.72} rx={0.03} fill={rust} stroke={shade(rust, -0.25)} strokeWidth={0.02} />
      <rect x={0.17} y={0.21} width={0.66} height={2.58} fill="none" stroke={cream} strokeWidth={0.03} />
      {motifs.map((cy, i) => (
        <g key={cy}>
          <path d={`M 0.185 ${cy - 0.08} L 0.27 ${cy} L 0.185 ${cy + 0.08} Z M 0.815 ${cy - 0.08} L 0.73 ${cy} L 0.815 ${cy + 0.08} Z`} fill={cream} />
          <path d={dia(0.5, cy, 0.22, 0.27)} fill={i % 2 ? navy : C.straw} />
          <path d={dia(0.5, cy, 0.12, 0.15)} fill={cream} />
          <path d={dia(0.5, cy, 0.05, 0.06)} fill={rust} />
        </g>
      ))}
      {[0.87, 1.5, 2.13].map((cy) => (
        <path key={cy} d={dia(0.5, cy, 0.06, 0.05)} fill={cream} />
      ))}
    </>,
  );
};

const RAINBOW = ["#F2A3AF", "#F6C39A", "#F3DC8C", "#A9D9BF", "#A7C8E8"];

const rainbow: Build = (a) => {
  a.floor(
    <>
      <rect x={0.06} y={0.06} width={1.88} height={1.88} rx={0.12} fill="#F7EFDF" stroke="#DCCDB2" strokeWidth={0.025} />
      <g transform={UP}>
        {RAINBOW.map((c, i) => {
          const r = 0.8 - i * 0.11;
          return <path key={c} d={`M ${-r} 0.34 A ${r} ${r} 0 0 1 ${r} 0.34`} fill="none" stroke={c} strokeWidth={0.11} />;
        })}
        {[-0.58, 0.58].map((x) => (
          <g key={x}>
            {puffs(
              [
                [x - 0.1, 0.37, 0.09],
                [x + 0.08, 0.36, 0.1],
                [x, 0.29, 0.1],
              ],
              C.white,
              "#D9D0C0",
              0.02,
            )}
          </g>
        ))}
      </g>
    </>,
  );
};

const WAVES = ["#D9EDF7", "#B3D9EE", "#8EC3E3", "#6EA9D5", "#548FC3", "#3E77AD"];

const wave: Build = (a, { id }) => {
  // 물마루 한 줄: 파장 0.5, 높이 ±0.05, 줄마다 반 파장씩 어긋난다
  const crest = (y: number, shift: number) => {
    let d = `M ${-shift} ${y}`;
    for (let x = -shift; x < 2; x += 0.5) d += ` Q ${x + 0.125} ${y - 0.1} ${x + 0.25} ${y} T ${x + 0.5} ${y}`;
    return d;
  };
  const rows = WAVES.map((c, i) => ({ c, y: 0.12 + i * 0.47, shift: (i % 2) * 0.25 }));
  a.floor(
    <>
      <clipPath id={`wave-${id}`}>
        <rect x={0.06} y={0.06} width={1.88} height={2.88} rx={0.1} />
      </clipPath>
      <g clipPath={`url(#wave-${id})`}>
        <rect x={0} y={0} width={2} height={3} fill={WAVES[0]} />
        {rows.slice(1).map(({ c, y, shift }) => (
          <path key={c} d={`${crest(y, shift)} L 2.5 3 L -0.5 3 Z`} fill={c} />
        ))}
        {rows.slice(1).map(({ y, shift }) => (
          <path key={y} d={crest(y, shift)} fill="none" stroke="#fff" strokeWidth={0.03} opacity={0.75} />
        ))}
      </g>
      <rect x={0.06} y={0.06} width={1.88} height={2.88} rx={0.1} fill="none" stroke="#3B6A96" strokeWidth={0.025} />
    </>,
  );
};

const doormat: Build = (a) => {
  const coir = "#CDA56C";
  a.floor(
    <>
      <rect x={0.08} y={0.17} width={0.84} height={0.66} rx={0.04} fill={coir} stroke={shade(coir, -0.35)} strokeWidth={0.02} />
      <g stroke={shade(coir, -0.12)} strokeWidth={0.01}>
        {Array.from({ length: 13 }, (_, i) => (
          <line key={i} x1={0.14 + i * 0.06} y1={0.21} x2={0.14 + i * 0.06} y2={0.79} />
        ))}
      </g>
      <rect x={0.15} y={0.24} width={0.7} height={0.52} rx={0.03} fill="none" stroke="#8A5B3A" strokeWidth={0.035} />
      <path d={heart(0.5, 0.5, 0.12)} fill="#B4513F" />
    </>,
  );
};

const yogaMat: Build = (a) => {
  const mat = "#9C88CF";
  a.floor(
    <>
      <rect x={0.2} y={0.15} width={0.6} height={1.79} rx={0.04} fill={mat} stroke={shade(mat, -0.28)} strokeWidth={0.02} />
      <rect x={0.26} y={0.32} width={0.48} height={1.54} rx={0.02} fill="none" stroke={shade(mat, 0.3)} strokeWidth={0.012} />
    </>,
  );
  // 덜 펴진 끝: 바닥에 누운 낮은 원통(굵기는 화면 px)
  const ends: [[number, number, number], [number, number, number]] = [
    [0.24, 0.15, 0.075],
    [0.76, 0.15, 0.075],
  ];
  a.line(...ends, shade(mat, -0.3), 5.4);
  a.line(...ends, mat, 4.2);
  a.line([0.26, 0.13, 0.11], [0.74, 0.13, 0.11], shade(mat, 0.35), 1);
};

export const BUILDS: Record<string, Build> = {
  rug_check: check,
  rug_heart: heartRug,
  rug_cloud: cloud,
  rug_runner: runner,
  rug_rainbow: rainbow,
  rug_wave: wave,
  rug_doormat: doormat,
  yoga_mat: yogaMat,
};
