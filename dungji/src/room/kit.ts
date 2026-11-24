// 물건 그림을 만드는 데 같이 쓰는 것: 색, 그리는 때의 사정(DrawCtx), 그림의 모양(Build, WallDraw).
// 바닥 물건은 Build 로 모형 좌표에 도형을 쌓고(src/room/model.tsx), 벽 장식은 WallDraw 로 벽 좌표에 그린다.

import type { ReactNode } from "react";
import type { FishItem } from "../../shared/api";
import type { PlantView } from "../../shared/rules";
import type { Art } from "./model";

export { Art, type P3 } from "./model";
export { mixHex, proj, shade } from "./iso";

export const C = {
  wood: "#C99A6B",
  woodLight: "#DDB98F",
  woodDark: "#93633F",
  linen: "#F3EFE7",
  white: "#FBFBF8",
  egg: "#8CCFC6",
  eggDeep: "#4FA79C",
  blush: "#EFA49B",
  straw: "#E7B74A",
  slate: "#5B6770",
  ink: "#2E3438",
  leaf: "#6E9E58",
  leafDark: "#4C7A3C",
  soil: "#6B4A33",
  sky: "#BFE0EE",
};

export interface DrawCtx {
  /** 그림 안의 clipPath 이름을 겹치지 않게 하는 물건 id */
  id: string;
  night: boolean;
  now: Date;
  plant?: PlantView;
  tank?: { clarity: number; fish: FishItem[] };
  sky: string;
}

/** 바닥 물건 하나의 그림: 돌리지 않은 방향의 모형 좌표에 도형을 쌓는다. */
export type Build = (a: Art, ctx: DrawCtx) => void;

/** 네 다리: (x0, y0)·(x1, y0)·(x0, y1)·(x1, y1) 에 굵기 t 의 기둥 */
export function legs(a: Art, x0: number, y0: number, x1: number, y1: number, h: number, c: string, t = 0.07) {
  for (const [x, y] of [
    [x0, y0],
    [x1, y0],
    [x0, y1],
    [x1, y1],
  ])
    a.box({ x, y, w: t, d: t, h, c });
}

/**
 * 벽 장식 하나의 그림: 벽 좌표에서 u 는 벽을 따라(물건의 왼쪽 끝이 u), 세로는 벽 꼭대기 0 에서 아래로 3.4 까지.
 * 바닥에서 사람 눈높이쯤이 1.0~1.6 이다.
 */
export type WallDraw = (u: number, ctx: DrawCtx) => ReactNode;
