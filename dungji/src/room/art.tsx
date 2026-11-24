// 물건 그림. 바닥 물건은 돌리지 않은 방향의 모형 좌표에 도형을 쌓아 만들고(src/room/model.tsx), 그릴 때 돌린
// 방향에 맞춰 뒤에서 앞으로 줄 세운다. 물건의 등은 y = 0 쪽, 앞은 +y 쪽(왼쪽 아래)이다. 벽 장식은 벽 좌표에 그린다.
// 새로 더한 물건들의 그림은 src/room/items/ 에 분류마다 있다.

import type { ReactNode } from "react";
import { fishDef, itemDef } from "../../shared/catalog";
import type { FishItem } from "../../shared/api";
import { Box, mixHex, proj, shade } from "./iso";
import { BUILDS as FURNITURE } from "./items/furniture";
import { BUILDS as HOBBY } from "./items/hobby";
import { BUILDS as HOME } from "./items/home";
import { BUILDS as RUGS } from "./items/rugs";
import { WALLS } from "./items/walls";
import { C, legs, type Build, type DrawCtx } from "./kit";
import { Art, paint, type Bounds, type Turn } from "./model";

export { C, type DrawCtx };

// ── 바닥 가구 ────────────────────────────────────────────────────

const bed: Build = (a) => {
  a.shadow();
  a.box({ w: 2, d: 0.16, h: 1.05, c: C.woodDark });
  a.box({ y: 0.16, w: 2, d: 2.84, h: 0.32, c: C.wood });
  a.box({ x: 0.08, y: 0.22, z: 0.32, w: 1.84, d: 2.7, h: 0.16, c: C.linen });
  a.box({ x: 0.16, y: 0.3, z: 0.48, w: 0.74, d: 0.44, h: 0.14, c: C.white });
  a.box({ x: 1.1, y: 0.3, z: 0.48, w: 0.74, d: 0.44, h: 0.14, c: C.white });
  a.box({ x: 0.04, y: 1.15, z: 0.4, w: 1.92, d: 1.82, h: 0.12, c: C.egg });
  a.box({ x: 0.04, y: 1.15, z: 0.52, w: 1.92, d: 0.2, h: 0.02, c: C.white, edge: false });
};

const sofa: Build = (a) => {
  a.shadow();
  a.box({ w: 2, d: 0.3, h: 0.85, c: C.eggDeep });
  a.box({ y: 0.3, w: 0.2, d: 0.7, h: 0.55, c: C.eggDeep });
  a.box({ x: 0.2, y: 0.3, w: 1.6, d: 0.7, h: 0.3, c: shade(C.eggDeep, -0.08) });
  a.box({ x: 0.24, y: 0.32, z: 0.3, w: 0.76, d: 0.64, h: 0.14, c: C.egg });
  a.box({ x: 1.0, y: 0.32, z: 0.3, w: 0.76, d: 0.64, h: 0.14, c: C.egg });
  a.box({ x: 1.8, y: 0.3, w: 0.2, d: 0.7, h: 0.55, c: C.eggDeep });
};

const tableLow: Build = (a) => {
  a.shadow();
  legs(a, 0.12, 0.12, 0.81, 0.81, 0.3, C.woodDark);
  a.box({ x: 0.05, y: 0.05, z: 0.3, w: 0.9, d: 0.9, h: 0.07, c: C.wood });
  const orange = (x: number, y: number, leaf: boolean) =>
    a.dot(x, y, 0.4, (sx, sy) => (
      <>
        <circle cx={sx} cy={sy - 3} r={4.2} fill="#F29A3F" stroke={shade("#F29A3F", -0.3)} strokeWidth={0.6} />
        {leaf && <circle cx={sx + 1} cy={sy - 6.4} r={1} fill={C.leaf} />}
      </>
    ));
  orange(0.4, 0.45, true);
  orange(0.62, 0.55, false);
};

const tableDining: Build = (a) => {
  a.shadow();
  legs(a, 0.1, 0.1, 1.83, 0.83, 0.72, C.woodDark);
  a.box({ x: 0.04, y: 0.04, z: 0.72, w: 1.92, d: 0.92, h: 0.06, c: C.woodLight });
  for (const gx of [0.55, 1.45]) a.dot(gx, 0.5, 0.8, (x, y) => <ellipse cx={x} cy={y} rx={9} ry={4.5} fill={C.white} stroke="#C9C6BE" strokeWidth={0.6} />, 0.18);
};

const chair: Build = (a) => {
  a.shadow();
  a.box({ x: 0.14, y: 0.1, w: 0.08, d: 0.08, h: 1.02, c: C.woodDark });
  a.box({ x: 0.78, y: 0.1, w: 0.08, d: 0.08, h: 1.02, c: C.woodDark });
  a.box({ x: 0.14, y: 0.8, w: 0.08, d: 0.08, h: 0.42, c: C.woodDark });
  a.box({ x: 0.78, y: 0.8, w: 0.08, d: 0.08, h: 0.42, c: C.woodDark });
  a.box({ x: 0.1, y: 0.18, z: 0.42, w: 0.8, d: 0.74, h: 0.07, c: C.wood });
  a.box({ x: 0.14, y: 0.1, z: 0.62, w: 0.72, d: 0.08, h: 0.36, c: C.wood });
};

const BOOKS = ["#D9665B", "#E7B74A", "#5B8FB0", "#6E9E58", "#B07BB8", "#F3EFE7", "#4FA79C"];
const shelf: Build = (a) => {
  a.shadow({ w: 1, d: 0.6 });
  a.box({ x: 0.04, w: 0.92, d: 0.56, h: 2.2, c: C.woodDark });
  a.face(
    "y",
    0.56,
    { u: [0.1, 0.9], z: [0.12, 2.12] },
    [0.12, 0.82, 1.52].map((z0, row) => (
      <g key={z0}>
        <rect x={0.1} y={z0} width={0.8} height={0.6} fill={shade(C.woodDark, -0.35)} />
        {Array.from({ length: 6 }, (_, i) => (
          <rect
            key={i}
            x={0.12 + i * 0.125}
            y={z0}
            width={0.11}
            height={0.38 + ((i * 7 + row * 3) % 5) * 0.035}
            fill={BOOKS[(i + row * 2) % BOOKS.length]}
            stroke="rgba(0,0,0,.25)"
            strokeWidth={0.01}
          />
        ))}
      </g>
    )),
  );
};

const lamp: Build = (a, { night }) => {
  a.cyl({ cx: 0.5, cy: 0.5, r: 0.2, h: 0.05, c: C.ink });
  a.box({ x: 0.47, y: 0.47, z: 0.05, w: 0.06, d: 0.06, h: 1.38, c: C.ink });
  a.cyl({ cx: 0.5, cy: 0.5, z: 1.38, r: 0.3, h: 0.4, c: night ? "#FFE3A3" : "#F2E2C2", top: night ? "#FFF2CC" : undefined, taper: 0.1 });
};

const desk: Build = (a) => {
  a.shadow();
  a.box({ x: 0.06, y: 0.1, w: 0.08, d: 0.8, h: 0.7, c: C.woodDark });
  a.box({ x: 1.36, y: 0.1, w: 0.58, d: 0.8, h: 0.7, c: C.wood });
  a.face(
    "y",
    0.9,
    { u: [1.42, 1.88], z: [0.42, 0.6] },
    <>
      <rect x={1.42} y={0.42} width={0.46} height={0.18} fill="none" stroke={C.woodDark} strokeWidth={0.02} />
      <rect x={1.6} y={0.49} width={0.1} height={0.03} fill={C.woodDark} />
    </>,
  );
  a.box({ x: 0.02, y: 0.04, z: 0.7, w: 1.96, d: 0.92, h: 0.06, c: C.woodLight });
  a.box({ x: 0.42, y: 0.24, z: 0.76, w: 0.66, d: 0.06, h: 0.42, c: C.slate });
  a.box({ x: 0.42, y: 0.3, z: 0.76, w: 0.66, d: 0.42, h: 0.03, c: "#9AA6AE" });
  a.face("y", 0.3, { u: [0.46, 1.04], z: [0.82, 1.14] }, <rect x={0.46} y={0.82} width={0.58} height={0.32} fill="#BFE0EE" opacity={0.9} />);
  a.cyl({ cx: 1.55, cy: 0.55, z: 0.76, r: 0.08, h: 0.16, c: C.blush });
};

const fridge: Build = (a) => {
  a.shadow();
  a.box({ x: 0.08, y: 0.06, w: 0.84, d: 0.8, h: 1.42, c: "#E6ECEE" });
  a.face(
    "y",
    0.86,
    { u: [0.08, 0.92], z: [0.5, 1.33] },
    <>
      <line x1={0.08} y1={0.95} x2={0.92} y2={0.95} stroke="#9FAAB0" strokeWidth={0.02} />
      <rect x={0.78} y={1.05} width={0.05} height={0.25} fill="#9FAAB0" />
      <rect x={0.78} y={0.55} width={0.05} height={0.25} fill="#9FAAB0" />
      <circle cx={0.3} cy={1.2} r={0.05} fill={C.blush} />
      <circle cx={0.45} cy={1.28} r={0.04} fill={C.straw} />
      <rect x={0.22} y={0.98} width={0.18} height={0.12} fill={C.white} stroke="#C9C6BE" strokeWidth={0.01} />
    </>,
  );
};

const recordPlayer: Build = (a) => {
  a.shadow();
  a.box({ x: 0.05, y: 0.1, w: 0.9, d: 0.8, h: 0.55, c: C.woodDark });
  a.box({ x: 0.1, y: 0.15, z: 0.55, w: 0.8, d: 0.7, h: 0.06, c: C.ink });
  a.dot(
    0.47,
    0.52,
    0.62,
    (cx, cy) => (
      <>
        <ellipse cx={cx} cy={cy} rx={13} ry={6.5} fill="#1C1C1F" />
        <ellipse cx={cx} cy={cy} rx={9} ry={4.5} fill="none" stroke="#3A3A40" strokeWidth={0.6} />
        <ellipse cx={cx} cy={cy} rx={3.4} ry={1.7} fill={C.blush} />
      </>
    ),
    0.3,
    0.01,
  );
  a.line([0.82, 0.26, 0.7], [0.6, 0.5, 0.66], "#C9C6BE", 1.4);
};

const tvStand: Build = (a) => {
  a.shadow();
  a.box({ y: 0.12, w: 2, d: 0.78, h: 0.45, c: C.woodDark });
  a.face("y", 0.9, { u: [0.98, 1.02], z: [0.04, 0.41] }, <line x1={1} y1={0.04} x2={1} y2={0.41} stroke={shade(C.woodDark, -0.3)} strokeWidth={0.015} />);
  a.box({ x: 0.9, y: 0.27, z: 0.45, w: 0.2, d: 0.14, h: 0.06, c: C.ink });
  a.box({ x: 0.18, y: 0.3, z: 0.51, w: 1.64, d: 0.08, h: 0.9, c: C.ink });
  a.face(
    "y",
    0.38,
    { u: [0.24, 1.76], z: [0.57, 1.35] },
    <>
      <rect x={0.24} y={0.57} width={1.52} height={0.78} fill="#24303A" />
      <path d="M 0.5 1.35 L 0.9 1.35 L 0.5 0.95 Z" fill="rgba(255,255,255,.08)" />
    </>,
  );
};

const wardrobe: Build = (a) => {
  a.shadow({ w: 0.75, d: 2 });
  a.box({ y: 0.02, w: 0.72, d: 1.96, h: 2.4, c: C.woodLight });
  a.face(
    "x",
    0.72,
    { u: [0.08, 1.92], z: [0.1, 2.3] },
    <>
      <line x1={1} y1={0.1} x2={1} y2={2.3} stroke={C.woodDark} strokeWidth={0.02} />
      <rect x={0.08} y={0.1} width={0.86} height={2.2} fill="none" stroke={shade(C.woodLight, -0.2)} strokeWidth={0.015} />
      <rect x={1.06} y={0.1} width={0.86} height={2.2} fill="none" stroke={shade(C.woodLight, -0.2)} strokeWidth={0.015} />
      <rect x={0.88} y={1.1} width={0.04} height={0.25} fill={C.woodDark} />
      <rect x={1.08} y={1.1} width={0.04} height={0.25} fill={C.woodDark} />
    </>,
  );
};

const catTower: Build = (a) => {
  a.shadow();
  a.box({ x: 0.08, y: 0.08, w: 0.84, d: 0.84, h: 0.12, c: C.linen });
  a.cyl({ cx: 0.32, cy: 0.32, z: 0.12, r: 0.08, h: 1.7, c: "#CDB38A" });
  a.box({ x: 0.08, y: 0.08, z: 1.82, w: 0.5, d: 0.5, h: 0.08, c: C.linen });
  a.cyl({ cx: 0.33, cy: 0.33, z: 1.9, r: 0.2, h: 0.1, c: C.blush });
  a.cyl({ cx: 0.7, cy: 0.66, z: 0.12, r: 0.08, h: 0.98, c: "#CDB38A" });
  a.box({ x: 0.46, y: 0.42, z: 1.1, w: 0.48, d: 0.48, h: 0.08, c: C.linen });
};

const easel: Build = (a) => {
  a.line([0.5, 0.12, 0], [0.5, 0.4, 1.55], C.woodDark);
  a.line([0.22, 0.62, 0], [0.48, 0.42, 1.6], C.woodDark);
  a.line([0.78, 0.62, 0], [0.52, 0.42, 1.6], C.woodDark);
  a.box({ x: 0.2, y: 0.42, z: 0.57, w: 0.6, d: 0.04, h: 0.78, c: "#E4DED3" });
  a.box({ x: 0.16, y: 0.44, z: 0.52, w: 0.68, d: 0.06, h: 0.05, c: C.wood });
  a.face(
    "y",
    0.46,
    { u: [0.2, 0.8], z: [0.57, 1.35] },
    <>
      <rect x={0.2} y={0.57} width={0.6} height={0.78} fill={C.white} stroke="#C9C6BE" strokeWidth={0.01} />
      <circle cx={0.62} cy={1.12} r={0.08} fill={C.straw} />
      <path d="M 0.22 0.62 Q 0.4 0.9 0.5 0.75 T 0.78 0.85 L 0.78 0.6 L 0.22 0.6 Z" fill={C.egg} />
    </>,
  );
};

const cushion: Build = (a) => {
  a.box({ x: 0.06, y: 0.06, w: 0.46, d: 0.46, h: 0.14, c: C.blush });
  a.box({ x: 0.48, y: 0.48, w: 0.46, d: 0.46, h: 0.14, c: C.straw });
};

const snackBox: Build = (a) => {
  a.shadow({ inset: 0.2 });
  a.box({ x: 0.2, y: 0.24, z: 0.32, w: 0.6, d: 0.04, h: 0.28, c: shade(C.straw, -0.1) });
  a.box({ x: 0.2, y: 0.28, w: 0.6, d: 0.5, h: 0.32, c: C.straw });
  a.dot(
    0.5,
    0.52,
    0.34,
    (x, y) => (
      <>
        <circle cx={x - 5} cy={y - 2} r={2.4} fill={C.blush} />
        <rect x={x - 1} y={y - 5} width={5} height={3} rx={1} fill="#7B4E35" />
        <circle cx={x + 6} cy={y} r={2} fill={C.white} />
      </>
    ),
    0.2,
    0.02,
  );
};

const rugRound: Build = (a) => {
  a.floor(
    <>
      <circle cx={1} cy={1} r={0.95} fill={C.blush} />
      <circle cx={1} cy={1} r={0.72} fill="none" stroke={C.white} strokeWidth={0.05} strokeDasharray="0.12 0.08" />
    </>,
  );
};

const rugStripe: Build = (a) => {
  a.floor(
    <>
      <rect x={0.06} y={0.06} width={1.88} height={2.88} rx={0.06} fill={C.linen} />
      {[0.35, 0.85, 1.35, 1.85, 2.35].map((y) => (
        <rect key={y} x={0.06} y={y} width={1.88} height={0.22} fill={C.eggDeep} opacity={0.75} />
      ))}
    </>,
  );
};

// ── 화분 ─────────────────────────────────────────────────────────

const POTS: Record<string, string> = {
  plant_tulip: "#9FC7D9",
  plant_sunflower: "#E3B341",
  plant_monstera: "#5B6770",
  plant_cactus: "#CF8A63",
  plant_tomato: "#7FA36B",
};

function leafShape(x: number, y: number, len: number, angle: number, fill: string) {
  return (
    <ellipse
      cx={x + Math.cos((angle * Math.PI) / 180) * len * 0.5}
      cy={y - Math.sin((angle * Math.PI) / 180) * len * 0.5}
      rx={len * 0.5}
      ry={len * 0.2}
      transform={`rotate(${-angle} ${x + Math.cos((angle * Math.PI) / 180) * len * 0.5} ${y - Math.sin((angle * Math.PI) / 180) * len * 0.5})`}
      fill={fill}
      stroke={shade(fill, -0.3)}
      strokeWidth={0.5}
    />
  );
}

function plantBody(key: string, stage: number, wilted: boolean, x: number, y: number): ReactNode {
  const leaf = wilted ? "#A09466" : C.leaf;
  const dark = wilted ? "#7E7448" : C.leafDark;
  const droop = wilted ? 1 : 0;
  const stem = (h: number) => (
    <path
      d={`M ${x} ${y} Q ${x + droop * 4} ${y - h * 0.6} ${x + droop * 9} ${y - h + droop * h * 0.35}`}
      fill="none"
      stroke={dark}
      strokeWidth={1.6}
      strokeLinecap="round"
    />
  );
  const tip = (h: number): [number, number] => [x + droop * 9, y - h + droop * h * 0.35];
  if (stage === 0)
    return (
      <>
        {leafShape(x, y, 6, 140, leaf)}
        {leafShape(x, y, 6, 40, leaf)}
      </>
    );
  switch (key) {
    case "plant_tulip": {
      const h = [0, 14, 22, 28, 30][stage];
      const [tx, ty] = tip(h);
      return (
        <>
          {stem(h)}
          {leafShape(x, y - 2, 12 + stage * 2, 115 - droop * 40, leaf)}
          {leafShape(x, y - 2, 11 + stage * 2, 65 - droop * 40, leaf)}
          {stage === 3 && <ellipse cx={tx} cy={ty - 3} rx={3} ry={4.5} fill={wilted ? "#9C8A5E" : "#8DBF6A"} />}
          {stage === 4 && (
            <path
              d={`M ${tx - 6} ${ty - 8} L ${tx - 3} ${ty - 3} L ${tx} ${ty - 9} L ${tx + 3} ${ty - 3} L ${tx + 6} ${ty - 8} Q ${tx + 6} ${ty + 2} ${tx} ${ty + 2} Q ${tx - 6} ${ty + 2} ${tx - 6} ${ty - 8} Z`}
              fill={wilted ? "#B28A7A" : "#E8565B"}
              stroke={shade("#E8565B", -0.3)}
              strokeWidth={0.6}
            />
          )}
        </>
      );
    }
    case "plant_sunflower": {
      const h = [0, 16, 26, 34, 38][stage];
      const [tx, ty] = tip(h);
      return (
        <>
          {stem(h)}
          {leafShape(x, y - h * 0.35, 10 + stage * 2.5, 150, leaf)}
          {leafShape(x, y - h * 0.55, 9 + stage * 2.5, 30, leaf)}
          {stage === 3 && <circle cx={tx} cy={ty} r={4.5} fill={wilted ? "#8C845A" : "#7FA35A"} />}
          {stage === 4 && (
            <g>
              {Array.from({ length: 12 }, (_, i) => (
                <ellipse
                  key={i}
                  cx={tx + Math.cos((i * Math.PI) / 6) * 7}
                  cy={ty + Math.sin((i * Math.PI) / 6) * 7}
                  rx={3.6}
                  ry={1.7}
                  transform={`rotate(${i * 30} ${tx + Math.cos((i * Math.PI) / 6) * 7} ${ty + Math.sin((i * Math.PI) / 6) * 7})`}
                  fill={wilted ? "#C9B26A" : "#F2C230"}
                />
              ))}
              <circle cx={tx} cy={ty} r={5} fill="#6B4227" />
            </g>
          )}
        </>
      );
    }
    case "plant_monstera": {
      const n = [0, 1, 2, 3, 4][stage];
      const angles = [100, 45, 140, 70];
      return (
        <>
          {Array.from({ length: n }, (_, i) => {
            const a = angles[i] - droop * 35;
            const len = 14 + stage * 3 - i * 1.5;
            const ex = x + Math.cos((a * Math.PI) / 180) * len;
            const ey = y - Math.sin((a * Math.PI) / 180) * len;
            return (
              <g key={i}>
                <line x1={x} y1={y} x2={ex} y2={ey} stroke={dark} strokeWidth={1.2} />
                <ellipse cx={ex} cy={ey} rx={8 + stage} ry={5.5 + stage * 0.6} fill={i === n - 1 && stage === 4 && !wilted ? "#9CC97A" : leaf} stroke={dark} strokeWidth={0.6} />
                <line x1={ex - 4} y1={ey} x2={ex + 4} y2={ey} stroke={dark} strokeWidth={0.5} />
              </g>
            );
          })}
        </>
      );
    }
    case "plant_cactus": {
      const h = [0, 9, 14, 19, 21][stage];
      const body = wilted ? "#8E9A62" : "#5E9E6A";
      return (
        <>
          <rect x={x - 4.5} y={y - h} width={9} height={h} rx={4.5} fill={body} stroke={shade(body, -0.3)} strokeWidth={0.6} />
          {stage >= 3 && <rect x={x + 3} y={y - h * 0.7} width={6} height={h * 0.35} rx={3} fill={body} stroke={shade(body, -0.3)} strokeWidth={0.6} />}
          {stage === 4 && <circle cx={x} cy={y - h - 1} r={3.2} fill={wilted ? "#B89A9A" : "#F08BB0"} />}
        </>
      );
    }
    default: {
      // 방울토마토
      const h = [0, 14, 24, 30, 32][stage];
      const [tx, ty] = tip(h);
      const fruit = stage === 4 ? (wilted ? "#A86A5A" : "#E04B3A") : "#8DBF6A";
      return (
        <>
          {stem(h)}
          {leafShape(x, y - h * 0.3, 10 + stage * 2, 155, leaf)}
          {leafShape(x, y - h * 0.5, 10 + stage * 2, 25, leaf)}
          {leafShape(tx, ty, 8, 90, leaf)}
          {stage >= 3 && (
            <>
              <circle cx={x - 6} cy={y - h * 0.55} r={2.8} fill={fruit} />
              <circle cx={x + 6} cy={y - h * 0.7} r={2.8} fill={fruit} />
              <circle cx={x - 3} cy={y - h * 0.82} r={2.6} fill={fruit} />
            </>
          )}
        </>
      );
    }
  }
}

function plantBuild(key: string): Build {
  return (a, { plant }) => {
    a.shadow({ inset: 0.22 });
    a.cyl({ cx: 0.5, cy: 0.5, r: 0.22, h: 0.42, c: POTS[key] ?? C.slate, taper: -0.04, top: C.soil });
    a.dot(0.5, 0.5, 0.42, (sx, sy) => plantBody(key, plant?.stage ?? 1, Boolean(plant?.wilted), sx, sy));
  };
}

// ── 어항 ─────────────────────────────────────────────────────────

function Fish({ f, x, y, span, delay }: { f: FishItem; x: number; y: number; span: number; delay: number }) {
  const def = fishDef(f.species);
  const size = 0.65 + 0.35 * Math.min(1, f.grown);
  const body = f.sick ? mixHex(def?.body ?? "#F08A3C", "#9AA0A0", 0.6) : (def?.body ?? "#F08A3C");
  const fin = f.sick ? mixHex(def?.fin ?? "#F6B46B", "#B0B4B4", 0.6) : (def?.fin ?? "#F6B46B");
  const dur = f.sick ? 9 : 5 + (delay % 3);
  return (
    <g transform={`translate(${x} ${y + (f.sick ? 4 : 0)})`}>
      <g className="fish-swim" style={{ animationDuration: `${dur}s`, animationDelay: `${-delay}s`, ["--span" as string]: `${span}px` }}>
        <g transform={`scale(${size})`}>
          <path d="M -5 0 L -9 -3.2 L -8.2 0 L -9 3.2 Z" fill={fin} />
          <ellipse cx={0} cy={0} rx={5} ry={2.9} fill={body} />
          <path d="M -1 -2.6 L 1.6 -4.4 L 2.4 -2.4 Z" fill={fin} />
          <circle cx={2.8} cy={-0.6} r={0.7} fill="#1C1C1F" />
        </g>
      </g>
    </g>
  );
}

function tankBuild(w: number): Build {
  return (a, { tank, id }) => {
    const clarity = tank?.clarity ?? 100;
    const water = mixHex("#8C8A55", "#9FD7E6", clarity / 100);
    const [x0, x1, y0, y1] = [0.1, w - 0.1, 0.16, 0.84];
    const base = 0.56;
    const top = 1.3;
    const fish = tank?.fish ?? [];
    a.shadow();
    a.box({ x: 0.06, y: 0.12, w: w - 0.12, d: 0.76, h: base, c: C.woodDark });
    a.box({ x: x0, y: y0, z: base, w: x1 - x0, d: y1 - y0, h: top - base - 0.1, c: water, edge: false });
    // 돌과 물풀
    const n = Math.round(w * 4);
    for (let i = 0; i < n; i++)
      a.dot(x0 + 0.15 + (i * (x1 - x0 - 0.3)) / Math.max(1, n - 1), y1 - 0.08, base + 0.02, (px, py) => (
        <ellipse cx={px} cy={py} rx={2.6} ry={1.5} fill={["#C9C2B0", "#A9A28F", "#E1DACA"][i % 3]} />
      ));
    a.dot(x0 + 0.2, y1 - 0.15, base, (px, py) => <path d={`M ${px} ${py} q -3 -8 1 -14 q 3 -6 -1 -12`} stroke="#4E8F5A" strokeWidth={1.6} fill="none" strokeLinecap="round" />);
    // 물고기: 물의 윤곽(돌린 뒤의 상자) 안에서만
    const wet: Bounds = [x0, y0, base, x1, y1, top - 0.1];
    a.add(wet, (v) => {
      const [X0, Y0, , X1, Y1] = v.bounds(wet);
      const t = top - 0.1;
      const clip = (
        [
          [X0, Y0, t],
          [X1, Y0, t],
          [X1, Y0, base],
          [X1, Y1, base],
          [X0, Y1, base],
          [X0, Y1, t],
        ] as const
      )
        .map(([X, Y, Z]) => proj(X, Y, Z).join(","))
        .join(" ");
      const [lx] = proj(X0, Y1, 0);
      const [rx] = proj(X1, Y0, 0);
      const [cx] = proj((X0 + X1) / 2, (Y0 + Y1) / 2, 0);
      return (
        <>
          <clipPath id={`water-${id}`}>
            <polygon points={clip} />
          </clipPath>
          <g clipPath={`url(#water-${id})`}>
            {fish.map((f, i) => {
              const [, fy] = proj((X0 + X1) / 2, (Y0 + Y1) / 2, base + 0.22 + (i % 3) * 0.15);
              return <Fish key={f.id} f={f} x={cx + ((i % 2) * 2 - 1) * 2} y={fy} span={(rx - lx) * 0.26} delay={i * 1.7} />;
            })}
          </g>
        </>
      );
    });
    // 유리
    const glass: Bounds = [x0, y0, base, x1, y1, top];
    a.add(glass, (v) => {
      const [X0, Y0, , X1, Y1] = v.bounds(glass);
      return (
        <g opacity={0.35}>
          <Box x={X0} y={Y0} z={base} w={X1 - X0} d={Y1 - Y0} h={top - base} c="#E8F6FA" top="rgba(232,246,250,0.4)" edge />
        </g>
      );
    });
  };
}

// ── 그림 고르기 ──────────────────────────────────────────────────

const OWN: Record<string, Build> = {
  bed_wood: bed,
  sofa_two: sofa,
  table_low: tableLow,
  table_dining: tableDining,
  chair_wood: chair,
  shelf_book: shelf,
  lamp_floor: lamp,
  desk,
  fridge_mini: fridge,
  record_player: recordPlayer,
  tv_stand: tvStand,
  wardrobe,
  cat_tower: catTower,
  easel,
  cushion,
  snack_box: snackBox,
  rug_round: rugRound,
  rug_stripe: rugStripe,
  plant_tulip: plantBuild("plant_tulip"),
  plant_sunflower: plantBuild("plant_sunflower"),
  plant_monstera: plantBuild("plant_monstera"),
  plant_cactus: plantBuild("plant_cactus"),
  plant_tomato: plantBuild("plant_tomato"),
  tank_small: tankBuild(1),
  tank_big: tankBuild(2),
};

const BUILDS: Record<string, Build> = { ...OWN, ...FURNITURE, ...HOME, ...HOBBY, ...RUGS };

/** 그림이 있나(시험·점검 화면) */
export const hasArt = (key: string) => key in BUILDS || key in WALLS;

/** 바닥 물건 하나의 도형들(돌리기 전). 그림이 없으면 null. */
export function buildArt(key: string, ctx: DrawCtx): Art | null {
  const def = itemDef(key);
  const build = BUILDS[key];
  if (!def || !build) return null;
  const art = new Art(def.w, def.d);
  build(art, ctx);
  return art;
}

/** 바닥 물건 하나를 turn 방향으로 돌려 그린다. 원점은 돌린 넓이의 안쪽 모서리. */
export function drawItem(key: string, ctx: DrawCtx, turn: Turn = 0): ReactNode {
  const art = buildArt(key, ctx);
  return art ? paint(art, turn) : <Box w={1} d={1} h={0.5} c={C.slate} />;
}

// ── 벽 장식: 벽 좌표(u 는 벽을 따라, w 는 위에서 아래로)에서 그린다 ──

export function drawWall(key: string, u: number, ctx: DrawCtx): ReactNode {
  const more = WALLS[key];
  if (more) return more(u, ctx);
  switch (key) {
    case "wall_window": {
      const night = ctx.night;
      return (
        <g>
          <rect x={u + 0.12} y={0.62} width={1.76} height={1.36} fill={C.white} />
          <rect x={u + 0.2} y={0.7} width={1.6} height={1.2} fill={ctx.sky} />
          {night ? (
            <>
              <circle cx={u + 1.4} cy={0.98} r={0.13} fill="#F5EBC4" />
              <circle cx={u + 0.5} cy={0.9} r={0.02} fill="#fff" />
              <circle cx={u + 0.8} cy={1.25} r={0.015} fill="#fff" />
            </>
          ) : (
            <>
              <ellipse cx={u + 0.62} cy={1.55} rx={0.42} ry={0.12} fill="rgba(255,255,255,.85)" />
              <ellipse cx={u + 1.35} cy={1.05} rx={0.3} ry={0.08} fill="rgba(255,255,255,.7)" />
            </>
          )}
          <rect x={u + 0.98} y={0.7} width={0.04} height={1.2} fill={C.white} />
          <rect x={u + 0.2} y={1.28} width={1.6} height={0.04} fill={C.white} />
          <rect x={u + 0.04} y={1.98} width={1.92} height={0.08} fill={shade(C.white, -0.12)} />
          <path d={`M ${u + 0.14} 0.56 L ${u + 0.42} 0.56 Q ${u + 0.38} 1.3 ${u + 0.5} 2.0 L ${u + 0.14} 2.0 Z`} fill={C.egg} opacity={0.95} />
          <path d={`M ${u + 1.86} 0.56 L ${u + 1.58} 0.56 Q ${u + 1.62} 1.3 ${u + 1.5} 2.0 L ${u + 1.86} 2.0 Z`} fill={C.egg} opacity={0.95} />
          <rect x={u + 0.06} y={0.52} width={1.88} height={0.05} fill={C.woodDark} />
        </g>
      );
    }
    case "wall_clock": {
      const h = ctx.now.getHours() % 12;
      const m = ctx.now.getMinutes();
      const cx = u + 0.5;
      const cy = 1.0;
      const hand = (deg: number, len: number, wdt: number) => (
        <line
          x1={cx}
          y1={cy}
          x2={cx + Math.sin((deg * Math.PI) / 180) * len}
          y2={cy - Math.cos((deg * Math.PI) / 180) * len}
          stroke={C.ink}
          strokeWidth={wdt}
          strokeLinecap="round"
        />
      );
      return (
        <g>
          <circle cx={cx} cy={cy} r={0.34} fill={C.white} stroke={C.woodDark} strokeWidth={0.06} />
          {[0, 90, 180, 270].map((a) => (
            <circle key={a} cx={cx + Math.sin((a * Math.PI) / 180) * 0.25} cy={cy - Math.cos((a * Math.PI) / 180) * 0.25} r={0.025} fill={C.ink} />
          ))}
          {hand(h * 30 + m * 0.5, 0.16, 0.045)}
          {hand(m * 6, 0.24, 0.03)}
          <circle cx={cx} cy={cy} r={0.03} fill={C.blush} />
        </g>
      );
    }
    case "wall_frame":
      return (
        <g>
          <rect x={u + 0.16} y={0.72} width={0.68} height={0.74} fill={C.woodDark} />
          <rect x={u + 0.22} y={0.78} width={0.56} height={0.62} fill="#CFE8F2" />
          <circle cx={u + 0.62} cy={0.95} r={0.08} fill="#F5C66A" />
          <rect x={u + 0.22} y={1.12} width={0.56} height={0.28} fill="#4F8FB5" />
          <path d={`M ${u + 0.22} 1.2 q 0.07 -0.04 0.14 0 t 0.14 0 t 0.14 0 t 0.14 0`} stroke="#fff" strokeWidth={0.02} fill="none" />
        </g>
      );
    case "wall_shelf":
      return (
        <g>
          <rect x={u + 0.08} y={1.4} width={0.84} height={0.07} fill={C.wood} />
          <path d={`M ${u + 0.2} 1.47 l 0 0.12 l 0.1 -0.12 Z M ${u + 0.7} 1.47 l 0 0.12 l 0.1 -0.12 Z`} fill={C.woodDark} />
          <rect x={u + 0.2} y={1.22} width={0.18} height={0.18} fill="#9FC7D9" />
          <ellipse cx={u + 0.24} cy={1.12} rx={0.09} ry={0.05} fill={C.leaf} />
          <ellipse cx={u + 0.36} cy={1.08} rx={0.08} ry={0.05} fill={C.leaf} />
          <rect x={u + 0.6} y={1.26} width={0.14} height={0.14} rx={0.02} fill={C.blush} />
        </g>
      );
    case "wall_garland": {
      const colors = [C.straw, C.blush, C.egg, C.white, C.eggDeep, C.blush];
      return (
        <g>
          <path d={`M ${u + 0.05} 0.42 Q ${u + 1} 0.82 ${u + 1.95} 0.42`} stroke={C.woodDark} strokeWidth={0.02} fill="none" />
          {colors.map((col, i) => {
            const t = (i + 0.5) / colors.length;
            const x = u + 0.05 + 1.9 * t;
            const y = 0.42 + 0.8 * t * (1 - t) * 1.0;
            return <path key={i} d={`M ${x - 0.11} ${y} L ${x + 0.11} ${y} L ${x} ${y + 0.26} Z`} fill={col} />;
          })}
        </g>
      );
    }
    case "wall_polaroid":
      return (
        <g>
          <path d={`M ${u + 0.05} 0.7 Q ${u + 1} 0.86 ${u + 1.95} 0.7`} stroke={C.woodDark} strokeWidth={0.015} fill="none" />
          {[0.3, 1.0, 1.7].map((x, i) => (
            <g key={x} transform={`rotate(${[-6, 3, -2][i]} ${u + x} 0.8)`}>
              <rect x={u + x - 0.2} y={0.78} width={0.4} height={0.46} fill={C.white} stroke="#D8D4CC" strokeWidth={0.01} />
              <rect x={u + x - 0.16} y={0.82} width={0.32} height={0.3} fill={[C.egg, C.blush, C.straw][i]} />
              <rect x={u + x - 0.03} y={0.74} width={0.06} height={0.08} fill={C.woodDark} />
            </g>
          ))}
        </g>
      );
    case "wall_poster":
      return (
        <g>
          <rect x={u + 0.2} y={0.55} width={0.6} height={0.95} fill="#2F3E5C" />
          <circle cx={u + 0.62} cy={0.78} r={0.1} fill="#F5EBC4" />
          <path d={`M ${u + 0.2} 1.3 L ${u + 0.42} 1.0 L ${u + 0.56} 1.18 L ${u + 0.66} 1.08 L ${u + 0.8} 1.3 Z`} fill="#5E7FA8" />
          <rect x={u + 0.2} y={1.3} width={0.6} height={0.2} fill={C.blush} />
        </g>
      );
    default:
      return <rect x={u + 0.2} y={0.8} width={0.6} height={0.6} fill={C.slate} />;
  }
}

// ── 먼지 ─────────────────────────────────────────────────────────

export function drawDirt(kind: string, sx: number, sy: number): ReactNode {
  switch (kind) {
    case "crumbs":
      return (
        <g fill="#B88B5C">
          {[
            [-6, 1],
            [-2, -2],
            [3, 2],
            [7, -1],
            [1, 4],
            [-4, 4],
          ].map(([dx, dy], i) => (
            <circle key={i} cx={sx + dx} cy={sy + dy} r={1.1 + (i % 2) * 0.4} />
          ))}
        </g>
      );
    case "stain":
      return <ellipse cx={sx} cy={sy} rx={12} ry={5.5} fill="rgba(122,92,52,0.33)" />;
    case "tissue":
      return (
        <g>
          <circle cx={sx} cy={sy - 3} r={5} fill="#FAFAF6" stroke="#CFCBC2" strokeWidth={0.7} />
          <path d={`M ${sx - 3} ${sy - 4} l 2 2 l 2 -2 M ${sx} ${sy - 1} l 3 1`} stroke="#CFCBC2" strokeWidth={0.6} fill="none" />
        </g>
      );
    case "sock":
      return (
        <g transform={`translate(${sx} ${sy - 3}) rotate(-20)`}>
          <path d="M -3 -6 L 2 -6 L 2 1 Q 7 1 7 4 Q 7 6 3 6 L -3 6 Z" fill="#FBFBF8" stroke="#C9C6BE" strokeWidth={0.6} />
          <rect x={-3} y={-4} width={5} height={1.4} fill={C.blush} />
          <rect x={-3} y={-1.4} width={5} height={1.4} fill={C.egg} />
        </g>
      );
    case "leaf":
      return (
        <g transform={`translate(${sx} ${sy}) rotate(25)`}>
          <ellipse cx={0} cy={0} rx={6} ry={2.6} fill="#A3934E" />
          <line x1={-6} y1={0} x2={6} y2={0} stroke="#7A6C35" strokeWidth={0.5} />
        </g>
      );
    default:
      return (
        <g>
          <circle cx={sx - 3} cy={sy - 1} r={4} fill="#A9A6A0" />
          <circle cx={sx + 3} cy={sy - 2} r={4.6} fill="#B9B6B0" />
          <circle cx={sx} cy={sy - 5} r={3.6} fill="#C6C3BD" />
          <circle cx={sx + 6} cy={sy + 1} r={2.4} fill="#A9A6A0" />
        </g>
      );
  }
}
