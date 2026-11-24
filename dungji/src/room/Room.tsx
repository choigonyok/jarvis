// 둥지의 방 한 칸: 벽, 바닥, 물건, 먼지, 밤의 불빛.
// 꾸미기 중에는 물건을 눌러 바로 끌어 옮기고, 꾸미기 밖에서는 물건을 길게 누르면 들어 올려 옮긴다.
// 손가락이 물건을 잡은 동안만 화면이 넘어가지 않고, 빈 곳에서 쓸면 화면이 넘어간다.

import { useEffect, useId, useMemo, useRef, useState, type PointerEvent as RPointerEvent, type ReactNode } from "react";
import { GRID, itemDef } from "../../shared/catalog";
import type { HomeView, ItemView } from "../../shared/api";
import { fitsFloor, fitsWall, footprint, hiddenItems, nextTurn, occluded, type Placed } from "../../shared/rules";
import { drawDirt, drawItem, drawWall, type DrawCtx } from "./art";
import { Flat, FLOOR_FRAME, LEFT_WALL_FRAME, proj, pts, RIGHT_WALL_FRAME, shade, unproj, WALL_H } from "./iso";
import { asTurn } from "./model";
import type { Phase } from "../lib/time";

export interface MoveTo {
  x: number;
  y: number;
  /** 돌린 정도(0~3, src/room/model.tsx) */
  turn: number;
  wall: "left" | "right" | null;
}

interface Props {
  home: HomeView;
  phase: Phase;
  now: Date;
  editing?: boolean;
  selected?: string | null;
  onSelect?: (id: string | null) => void;
  onMove?: (item: ItemView, to: MoveTo) => void;
  onDirt?: (id: string) => void;
  onItem?: (item: ItemView) => void;
  /** 꾸미기 밖에서 물건을 길게 눌렀다: 그 물건을 고른 채 꾸미기로(손가락은 계속 그 물건을 끈다) */
  onLift?: (id: string) => void;
  /** 다른 물건과 겹치는 자리에 놓으려 했다(물건은 제자리로) */
  onBlocked?: () => void;
  /** 방금 치운 먼지: 그 자리에 잠깐 퍼지는 효과 */
  poofs?: { key: string; x: number; y: number }[];
  /** 주인에게만: 목마른 화분·배고픈 물고기 표시 */
  showNeeds?: boolean;
  label: string;
}

const T = 0.18; // 벽 두께
const SLAB = 0.32; // 바닥 두께
const SLOP = 6; // 손가락이 이만큼(px) 움직여야 끌기, 그보다 덜 움직이고 떼면 누르기
const HOLD_MS = 450; // 꾸미기 밖에서 물건을 이만큼 누르고 있으면 들어 올린다

/** 누르고 있는 손가락 하나 */
interface Press {
  pointerId: number;
  /** 누른 물건(빈 곳이면 null) */
  id: string | null;
  x0: number;
  y0: number;
  /** 잡은 자리와 물건 자리의 차이(격자 단위): 바닥 [gx, gy], 벽 [벽을 따라] */
  grab: number[];
  /** 움직여서 끄는 중이다 */
  live: boolean;
  /** 꾸미기 밖에서 길게 누르기를 기다리는 중 */
  hold?: ReturnType<typeof setTimeout>;
  /** 길게 눌러 들어 올렸다 */
  lifted: boolean;
  /** 지금 끌어 놓은 자리 */
  to?: MoveTo;
  valid: boolean;
}

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));

export const SKY: Record<Phase, string> = {
  dawn: "#E4DDF3",
  day: "#CFE6EF",
  dusk: "#F1D2C3",
  night: "#232A4A",
};

export const WALLPAPER: Record<string, { base: string; ink: string }> = {
  paper_plain: { base: "#F1F2EE", ink: "#E3E5DF" },
  paper_stripe: { base: "#E8F1EE", ink: "#D2E5DF" },
  paper_dot: { base: "#F6EEEA", ink: "#E8C8BF" },
  paper_check: { base: "#EDF0F5", ink: "#D3DAE7" },
  paper_wood: { base: "#D9BA90", ink: "#C09C70" },
  paper_star: { base: "#2F3A5E", ink: "#F5EBC4" },
};

export const FLOORS: Record<string, { base: string; ink: string }> = {
  floor_wood: { base: "#D3A979", ink: "#B98D5D" },
  floor_tile: { base: "#ECEFEF", ink: "#D0D6D7" },
  floor_carpet: { base: "#A3C7BE", ink: "#8FB8AE" },
  floor_herring: { base: "#C99868", ink: "#AE7E50" },
  floor_grass: { base: "#8DB872", ink: "#74A05A" },
};

interface FloorEntry {
  item: ItemView;
  x: number;
  y: number;
  w: number;
  d: number;
  turn: number;
}

/** 뒤에 있는 것부터 그리는 순서(서로 가리는 순서). */
function depthSort(list: FloorEntry[]) {
  const behind = (a: FloorEntry, b: FloorEntry) =>
    (a.x + a.w <= b.x && !(b.y + b.d <= a.y)) || (a.y + a.d <= b.y && !(b.x + b.w <= a.x));
  const out: FloorEntry[] = [];
  const state = new Map<FloorEntry, 0 | 1 | 2>();
  const visit = (n: FloorEntry) => {
    const s = state.get(n);
    if (s === 2) return;
    if (s === 1) return; // 고리(겹친 경우)는 무시
    state.set(n, 1);
    for (const m of list) if (m !== n && behind(m, n)) visit(m);
    state.set(n, 2);
    out.push(n);
  };
  [...list].sort((a, b) => a.x + a.y - (b.x + b.y)).forEach(visit);
  return out;
}

export function Room({ home, phase, now, editing, selected, onSelect, onMove, onDirt, onItem, onLift, onBlocked, poofs = [], showNeeds, label }: Props) {
  const uid = useId().replace(/:/g, "");
  const svgRef = useRef<SVGSVGElement>(null);
  const [drag, setDrag] = useState<{ id: string; to: MoveTo; valid: boolean } | null>(null);
  const press = useRef<Press | null>(null);
  const editingRef = useRef(editing);
  editingRef.current = editing;
  const night = phase === "night";
  const ctxBase: DrawCtx = { id: uid, night, now, sky: SKY[phase] };
  const wp = WALLPAPER[home.wallpaper] ?? WALLPAPER.paper_plain;
  const fl = FLOORS[home.floor] ?? FLOORS.floor_wood;

  const room = useMemo(() => home.items.filter((i) => i.place === "room"), [home.items]);
  const placedList: Placed[] = useMemo(
    () => room.map((i) => ({ id: i.id, key: i.key, x: i.x, y: i.y, turn: i.turn, wall: i.wall })),
    [room],
  );

  const position = (i: ItemView): MoveTo => (drag && drag.id === i.id ? drag.to : { x: i.x, y: i.y, turn: i.turn, wall: i.wall });

  const floorItems: FloorEntry[] = [];
  const unders: FloorEntry[] = [];
  const wallItems: ItemView[] = [];
  for (const i of room) {
    const def = itemDef(i.key);
    if (!def) continue;
    if (def.kind === "wall") {
      wallItems.push(i);
      continue;
    }
    const p = position(i);
    const fp = footprint(def, p.turn);
    const entry = { item: i, x: p.x, y: p.y, w: fp.w, d: fp.d, turn: p.turn };
    (def.under ? unders : floorItems).push(entry);
  }

  // ── 끌어서 옮기기 ──
  function svgPoint(e: RPointerEvent) {
    const svg = svgRef.current!;
    const pt = svg.createSVGPoint();
    pt.x = e.clientX;
    pt.y = e.clientY;
    return pt.matrixTransform(svg.getScreenCTM()!.inverse());
  }

  /** 벽 장식: 화면 좌표의 오른쪽 벽·왼쪽 벽 위 자리(u 는 벽을 따라, w 는 위에서 아래로) */
  function onWalls(sx: number, sy: number) {
    const ur = sx / 28;
    const ul = (sx + 196) / 28;
    return { right: [ur, (sy - 14 * ur + 28 * WALL_H) / 28], left: [ul, (sy + 14 * ul - 98 + 28 * WALL_H) / 28] } as const;
  }

  /** 손가락이 물건의 어디를 잡았나: 끄는 동안 그 거리를 지켜 물건이 손가락 밑으로 튀지 않는다. */
  function grabAt(item: ItemView, sx: number, sy: number): number[] {
    if (itemDef(item.key)?.kind === "wall") return [onWalls(sx, sy)[item.wall === "left" ? "left" : "right"][0] - item.x];
    const [gx, gy] = unproj(sx, sy);
    return [gx - item.x, gy - item.y];
  }

  function candidate(item: ItemView, sx: number, sy: number, grab: number[]): { to: MoveTo; valid: boolean } | null {
    const def = itemDef(item.key)!;
    if (def.kind === "wall") {
      const walls = onWalls(sx, sy);
      const pick = (wall: "left" | "right") => {
        const [u, w] = walls[wall];
        if (u < -0.5 || u > GRID + 0.5 || w < -0.3 || w > WALL_H + 0.3) return null;
        const pos = clamp(Math.round(u - grab[0]), 0, GRID - def.w);
        return { to: { x: pos, y: 0, turn: 0, wall }, valid: fitsWall(placedList, def, wall, pos, item.id) };
      };
      return sx >= 0 ? (pick("right") ?? pick("left")) : (pick("left") ?? pick("right"));
    }
    const [gx, gy] = unproj(sx, sy);
    const fp = footprint(def, item.turn);
    const x = clamp(Math.round(gx - grab[0]), 0, GRID - fp.w);
    const y = clamp(Math.round(gy - grab[1]), 0, GRID - fp.d);
    return { to: { x, y, turn: item.turn, wall: null }, valid: fitsFloor(placedList, def, x, y, item.turn, item.id) };
  }

  /** 손가락(또는 마우스)을 누른다. 꾸미기 중이면 바로 잡고, 아니면 길게 누르기를 기다린다. */
  function begin(e: RPointerEvent, item: ItemView | null) {
    if (!e.isPrimary || e.button !== 0) return;
    const prev = press.current;
    if (prev?.hold) clearTimeout(prev.hold);
    const p = svgPoint(e);
    const pr: Press = { pointerId: e.pointerId, id: item?.id ?? null, x0: e.clientX, y0: e.clientY, grab: item ? grabAt(item, p.x, p.y) : [], live: false, lifted: false, valid: true };
    press.current = pr;
    if (!item) return;
    if (editing) {
      svgRef.current?.setPointerCapture(e.pointerId);
      return;
    }
    pr.hold = setTimeout(() => {
      if (press.current !== pr) return;
      pr.hold = undefined;
      pr.lifted = true;
      try {
        svgRef.current?.setPointerCapture(pr.pointerId);
      } catch {
        // 손가락이 이미 떨어졌다
      }
      onLift?.(item.id);
    }, HOLD_MS);
  }

  function onItemDown(e: RPointerEvent, item: ItemView) {
    if (editing) {
      e.stopPropagation();
      if (selected !== item.id) onSelect?.(item.id);
      begin(e, item);
    } else if (onLift) begin(e, item);
  }

  function onMovePointer(e: RPointerEvent) {
    const pr = press.current;
    if (!pr || pr.pointerId !== e.pointerId) return;
    const far = Math.hypot(e.clientX - pr.x0, e.clientY - pr.y0) >= SLOP;
    if (pr.hold) {
      // 들어 올리기 전에 움직였으면 화면을 넘기는 것이다
      if (far) {
        clearTimeout(pr.hold);
        press.current = null;
      }
      return;
    }
    if (!pr.live && !far) return;
    pr.live = true;
    if (!pr.id || (!editing && !pr.lifted)) return;
    const item = room.find((i) => i.id === pr.id);
    if (!item) return;
    const p = svgPoint(e);
    const c = candidate(item, p.x, p.y, pr.grab);
    if (!c) return;
    const same = pr.to && c.to.x === pr.to.x && c.to.y === pr.to.y && c.to.wall === pr.to.wall;
    pr.to = c.to;
    pr.valid = c.valid;
    if (!same) setDrag({ id: item.id, to: c.to, valid: c.valid });
  }

  function onUp(e: RPointerEvent) {
    const pr = press.current;
    if (!pr || pr.pointerId !== e.pointerId) return;
    if (pr.hold) clearTimeout(pr.hold);
    press.current = null;
    setDrag(null);
    // 꾸미기 중 빈 곳을 눌렀다 떼면 고른 것을 놓는다(쓸어 넘긴 것은 아니다)
    if (!pr.id) {
      if (editing && !pr.live) onSelect?.(null);
      return;
    }
    const item = room.find((i) => i.id === pr.id);
    const to = pr.to;
    if (!item || !to || !pr.live || (to.x === item.x && to.y === item.y && to.wall === item.wall)) return;
    if (pr.valid) onMove?.(item, to);
    else onBlocked?.();
  }

  function onCancel(e: RPointerEvent) {
    const pr = press.current;
    if (!pr || pr.pointerId !== e.pointerId) return;
    if (pr.hold) clearTimeout(pr.hold);
    press.current = null;
    setDrag(null);
  }

  function onBackgroundDown(e: RPointerEvent) {
    if (editing) begin(e, null);
  }

  // 손가락이 물건을 잡았으면 화면이 넘어가지 않게 한다(꾸미기 중 물건 위에서 시작한 손짓, 길게 눌러 들어 올린 뒤).
  // React 의 터치 처리기는 막을 수 없는(passive) 것이라 직접 단다. 빈 곳에서 시작한 손짓은 그대로 화면을 넘긴다.
  useEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const start = (e: TouchEvent) => {
      if (editingRef.current && e.touches.length === 1 && (e.target as Element | null)?.closest?.("[data-item],[data-grab]")) e.preventDefault();
    };
    const move = (e: TouchEvent) => {
      const pr = press.current;
      if (pr && pr.id && (pr.lifted || (editingRef.current && !pr.hold))) e.preventDefault();
    };
    const menu = (e: Event) => e.preventDefault();
    svg.addEventListener("touchstart", start, { passive: false });
    svg.addEventListener("touchmove", move, { passive: false });
    svg.addEventListener("contextmenu", menu);
    return () => {
      svg.removeEventListener("touchstart", start);
      svg.removeEventListener("touchmove", move);
      svg.removeEventListener("contextmenu", menu);
    };
  }, []);

  // 화면을 떠나면(다른 칸, 꾸미기 끝) 기다리던 길게 누르기를 버린다
  useEffect(
    () => () => {
      const pr = press.current;
      if (pr?.hold) clearTimeout(pr.hold);
    },
    [],
  );

  // ── 그리기 ──
  const wallRect = (frame: string, shadeAmt: number, key: string) => (
    <g transform={frame} key={key}>
      <rect x={0} y={0} width={GRID} height={WALL_H} fill={wp.base} />
      <rect x={0} y={0} width={GRID} height={WALL_H} fill={`url(#wp-${uid})`} />
      {shadeAmt > 0 && <rect x={0} y={0} width={GRID} height={WALL_H} fill={`rgba(30,40,40,${shadeAmt})`} />}
      <rect x={0} y={WALL_H - 0.14} width={GRID} height={0.14} fill={shade(wp.base, -0.18)} />
    </g>
  );

  const cleanliness = home.cleanliness;
  const flies = home.dirt.length >= 10;

  const itemNode = (e: FloorEntry, ghost = false): ReactNode => {
    const [tx, ty] = proj(e.x, e.y);
    const isSel = editing && selected === e.item.id;
    const ctx: DrawCtx = { ...ctxBase, id: `${uid}-${ghost ? "g" : ""}${e.item.id}`, plant: e.item.plant, tank: e.item.tank };
    return (
      <g
        key={(ghost ? "ghost-" : "") + e.item.id}
        transform={`translate(${tx} ${ty})`}
        className={ghost ? "room-ghost" : isSel ? "room-selected" : editing ? "room-editable" : onItem ? "room-tappable" : undefined}
        onPointerDown={ghost ? undefined : (ev) => onItemDown(ev, e.item)}
        onClick={ghost || editing ? undefined : () => onItem?.(e.item)}
        {...(ghost ? {} : itemA11y(e.item))}
      >
        {!ghost && <HitBox itemKey={e.item.key} />}
        {drawItem(e.item.key, ctx, asTurn(e.turn))}
      </g>
    );
  };

  /** 물건마다 이름을 붙이고 키보드로도 고를 수 있게 한다. */
  const itemA11y = (item: ItemView) => {
    const def = itemDef(item.key);
    const name = def?.name ?? "물건";
    if (!editing && !onItem) return { "data-item": item.id };
    return {
      "data-item": item.id,
      role: "button",
      tabIndex: 0,
      "aria-label": editing ? `${name} ${selected === item.id ? "고름, 화살표로 옮기기" : "고르기"}` : name,
      "aria-pressed": editing ? selected === item.id : undefined,
      onKeyDown: (ev: React.KeyboardEvent) => {
        if (ev.key !== "Enter" && ev.key !== " ") return;
        ev.preventDefault();
        if (editing) onSelect?.(selected === item.id ? null : item.id);
        else onItem?.(item);
      },
    };
  };

  /** 꾸미기 중 고른 물건을 화살표로 한 칸씩 옮기고, R 로 돌린다(Shift+R 은 반대로). */
  function onKey(ev: React.KeyboardEvent) {
    if (!editing || !selected) return;
    const item = room.find((i) => i.id === selected);
    const def = item && itemDef(item.key);
    if (!item || !def) return;
    if ((ev.key === "r" || ev.key === "R") && def.kind !== "wall" && !ev.metaKey && !ev.ctrlKey && !ev.altKey) {
      ev.preventDefault();
      const p = placedList.find((x) => x.id === item.id)!;
      onMove?.(item, { x: item.x, y: item.y, turn: nextTurn(placedList, def, p, ev.shiftKey ? -1 : 1).turn, wall: null });
      return;
    }
    const step: Record<string, [number, number]> = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };
    const d = step[ev.key];
    if (!d) return;
    ev.preventDefault();
    if (def.kind === "wall") {
      const pos = item.x + (d[0] || d[1]) * (item.wall === "left" ? -1 : 1);
      if (fitsWall(placedList, def, item.wall ?? "right", pos, item.id)) onMove?.(item, { x: pos, y: 0, turn: 0, wall: item.wall });
    } else if (fitsFloor(placedList, def, item.x + d[0], item.y + d[1], item.turn, item.id))
      onMove?.(item, { x: item.x + d[0], y: item.y + d[1], turn: item.turn, wall: null });
  }

  const needs = (e: FloorEntry): ReactNode => {
    if (!showNeeds || editing) return null;
    const def = itemDef(e.item.key);
    const [x, y] = proj(e.x + e.w / 2, e.y + e.d / 2, def?.kind === "tank" ? 1.6 : 1.9);
    let icon: ReactNode = null;
    if (e.item.plant?.ready) icon = <path d="M0 -6 L1.6 -1.6 L6 0 L1.6 1.6 L0 6 L-1.6 1.6 L-6 0 L-1.6 -1.6 Z" fill="#F2C230" stroke="#B88A12" strokeWidth={0.6} />;
    else if (e.item.plant && e.item.plant.water < 30) icon = <path d="M0 -6 C 3 -2 4.5 0.5 4.5 2.2 A 4.5 4.5 0 0 1 -4.5 2.2 C -4.5 0.5 -3 -2 0 -6 Z" fill="#5BA7D8" stroke="#2F7DB0" strokeWidth={0.6} />;
    else if (e.item.tank && e.item.tank.fish.some((f) => f.hunger < 30)) icon = <g fill="#C98A4B"><circle cx={-2.5} cy={0} r={1.6} /><circle cx={1.5} cy={-1.5} r={1.4} /><circle cx={2} cy={2} r={1.5} /></g>;
    else if (e.item.tank && e.item.tank.clarity < 40) icon = <g fill="none" stroke="#5BA7D8" strokeWidth={0.9}><circle cx={-2} cy={1} r={2} /><circle cx={2.5} cy={-2.5} r={1.5} /></g>;
    if (!icon) return null;
    return (
      <g
        key={`need-${e.item.id}`}
        transform={`translate(${x} ${y})`}
        className="need-bob room-tappable"
        role="button"
        aria-label={`${itemDef(e.item.key)?.name ?? "물건"} 돌보기`}
        onClick={(ev) => {
          ev.stopPropagation();
          onItem?.(e.item);
        }}
      >
        <circle r={22} fill="transparent" />
        <circle r={8.5} fill="rgba(255,255,255,.92)" stroke="rgba(0,0,0,.12)" />
        {icon}
      </g>
    );
  };

  const sorted = depthSort(floorItems);
  const hidden = useMemo(() => hiddenItems(placedList), [placedList]);

  return (
    <svg
      ref={svgRef}
      viewBox="-210 -110 420 322"
      className="room-svg"
      role="group"
      aria-label={label}
      onKeyDown={onKey}
      onPointerMove={onMovePointer}
      onPointerUp={onUp}
      onPointerCancel={onCancel}
      onPointerDown={onBackgroundDown}
    >
      <defs>
        <Wallpaper id={`wp-${uid}`} kind={home.wallpaper} ink={wp.ink} />
        <FloorPattern id={`fl-${uid}`} kind={home.floor} ink={fl.ink} />
        <radialGradient id={`glow-${uid}`}>
          <stop offset="0" stopColor="#FFE2A0" stopOpacity="0.85" />
          <stop offset="0.45" stopColor="#FFD27A" stopOpacity="0.32" />
          <stop offset="1" stopColor="#FFD27A" stopOpacity="0" />
        </radialGradient>
      </defs>

      {/* 벽 */}
      {wallRect(LEFT_WALL_FRAME, 0.05, "lw")}
      {wallRect(RIGHT_WALL_FRAME, 0, "rw")}
      <polygon points={pts([[-T, -T, WALL_H], [GRID, -T, WALL_H], [GRID, 0, WALL_H], [0, 0, WALL_H], [0, GRID, WALL_H], [-T, GRID, WALL_H]])} fill={shade(wp.base, 0.35)} stroke={shade(wp.base, -0.25)} strokeWidth={0.6} />
      <polygon points={pts([[GRID, -T, -SLAB], [GRID, 0, -SLAB], [GRID, 0, WALL_H], [GRID, -T, WALL_H]])} fill={shade(wp.base, -0.22)} />
      <polygon points={pts([[-T, GRID, -SLAB], [0, GRID, -SLAB], [0, GRID, WALL_H], [-T, GRID, WALL_H]])} fill={shade(wp.base, -0.1)} />
      {cleanliness < 50 && <Cobweb />}

      {/* 바닥 */}
      <polygon points={pts([[0, GRID, -SLAB], [GRID, GRID, -SLAB], [GRID, GRID, 0], [0, GRID, 0]])} fill={shade(fl.base, -0.28)} />
      <polygon points={pts([[GRID, 0, -SLAB], [GRID, GRID, -SLAB], [GRID, GRID, 0], [GRID, 0, 0]])} fill={shade(fl.base, -0.4)} />
      <g transform={FLOOR_FRAME}>
        <rect x={0} y={0} width={GRID} height={GRID} fill={fl.base} />
        <rect x={0} y={0} width={GRID} height={GRID} fill={`url(#fl-${uid})`} />
        {editing &&
          Array.from({ length: GRID + 1 }, (_, i) => (
            <g key={i} stroke="rgba(255,255,255,.7)" strokeWidth={0.025}>
              <line x1={i} y1={0} x2={i} y2={GRID} />
              <line x1={0} y1={i} x2={GRID} y2={i} />
            </g>
          ))}
      </g>

      {/* 벽 장식 */}
      {wallItems.map((i) => {
        const p = position(i);
        const isSel = editing && selected === i.id;
        return (
          <g
            key={i.id}
            transform={p.wall === "left" ? LEFT_WALL_FRAME : RIGHT_WALL_FRAME}
            className={editing ? "room-editable" : onItem ? "room-tappable" : undefined}
            onPointerDown={(ev) => onItemDown(ev, i)}
            onClick={editing ? undefined : () => onItem?.(i)}
            {...itemA11y(i)}
          >
            {isSel && (
              <rect
                x={p.x}
                y={0.4}
                width={itemDef(i.key)!.w}
                height={1.75}
                rx={0.06}
                style={drag && !drag.valid ? { fill: "color-mix(in oklab, var(--reject) 25%, transparent)", stroke: "var(--reject)" } : { fill: "color-mix(in oklab, var(--foreground) 18%, transparent)", stroke: "var(--foreground)" }}
                strokeWidth={0.04}
                strokeDasharray="0.14 0.08"
              />
            )}
            <rect x={p.x} y={0.35} width={itemDef(i.key)!.w} height={1.85} fill="transparent" />
            {drawWall(i.key, p.x, ctxBase)}
          </g>
        );
      })}

      {/* 러그 */}
      {unders.map((e) => itemNode(e))}

      {/* 고른 물건의 자리: 이 자리를 눌러도 그 물건을 잡는다(러그는 이 자리에 덮인다) */}
      {editing &&
        [...floorItems, ...unders]
          .filter((e) => e.item.id === selected)
          .map((e) => (
            <g key="sel" data-grab="" onPointerDown={(ev) => onItemDown(ev, e.item)} style={drag && !drag.valid ? { fill: "color-mix(in oklab, var(--reject) 35%, transparent)", stroke: "var(--reject)" } : { fill: "color-mix(in oklab, var(--foreground) 22%, transparent)", stroke: "var(--foreground)" }}>
              <Flat x={e.x} y={e.y} w={e.w} d={e.d} fill="inherit" stroke="inherit" />
            </g>
          ))}

      {/* 먼지 */}
      {home.dirt.map((s) => {
        const [sx, sy] = proj(s.x + 0.5, s.y + 0.5);
        return (
          <g
            key={s.id}
            data-dirt={s.id}
            className={editing ? undefined : "dirt"}
            pointerEvents={editing ? "none" : undefined}
            onClick={(ev) => {
              ev.stopPropagation();
              onDirt?.(s.id);
            }}
            role="button"
            tabIndex={editing ? -1 : 0}
            onKeyDown={(ev) => {
              if (ev.key !== "Enter" && ev.key !== " ") return;
              ev.preventDefault();
              onDirt?.(s.id);
            }}
            aria-label="먼지 치우기"
          >
            <ellipse cx={sx} cy={sy - 3} rx={32} ry={29} fill="transparent" />
            {drawDirt(s.kind, sx, sy)}
          </g>
        );
      })}

      {/* 가구 */}
      {sorted.map((e) => itemNode(e))}
      {sorted.map((e) => needs(e))}

      {/* 다른 가구에 가려진 물건, 위에 선 것들에 덮인 러그: 위에 작은 표시(누르면 그 물건) */}
      {[...unders, ...sorted]
        .filter((e) => hidden.has(e.item.id))
        .map((e) => {
          const def = itemDef(e.item.key)!;
          const [hx, hy] = proj(e.x + e.w / 2, e.y + e.d / 2, Math.max(def.h, 0.6) + 0.35);
          return (
            <g
              key={`hidden-${e.item.id}`}
              transform={`translate(${hx} ${hy})`}
              className="room-tappable"
              role="button"
              aria-label={`가려진 ${def.name}`}
              data-grab=""
              onPointerDown={(ev) => {
                if (!editing) return;
                ev.stopPropagation();
                onSelect?.(e.item.id);
                begin(ev, e.item);
              }}
              onClick={(ev) => {
                ev.stopPropagation();
                if (!editing) onItem?.(e.item);
              }}
            >
              <circle r={22} fill="transparent" />
              <circle r={10} fill="rgba(255,255,255,.94)" stroke="rgba(0,0,0,.18)" />
              {def.kind === "plant" ? (
                <path d="M0 5 C0 1 -4 -2 -5 -5 C-1 -5 1 -2 0 2 C1 -2 3 -5 6 -5 C5 -1 2 1 0 5 Z" fill="#4C7A3C" />
              ) : def.kind === "tank" ? (
                <g fill="#E07B39">
                  <ellipse cx={1} cy={0} rx={4.5} ry={2.8} />
                  <path d="M -3 0 L -6.5 -3 L -6 0 L -6.5 3 Z" />
                </g>
              ) : (
                <g fill="#5B6770">
                  <circle cx={-3.5} cy={0} r={1.4} />
                  <circle cx={0} cy={0} r={1.4} />
                  <circle cx={3.5} cy={0} r={1.4} />
                </g>
              )}
            </g>
          );
        })}

      {/* 고른 물건의 자리: 물건 위에 점선으로(물건 밑의 칸은 가려진다) */}
      {editing &&
        [...floorItems, ...unders]
          .filter((e) => e.item.id === selected)
          .map((e) => (
            <g key="sel-top" pointerEvents="none" style={{ stroke: drag && !drag.valid ? "var(--reject)" : "var(--foreground)" }}>
              <polygon
                points={pts([[e.x, e.y, 0], [e.x + e.w, e.y, 0], [e.x + e.w, e.y + e.d, 0], [e.x, e.y + e.d, 0]])}
                fill="none"
                strokeWidth={1.6}
                strokeDasharray="5 4"
              />
            </g>
          ))}

      {/* 가구에 가려진 먼지는 가구 위에 작은 표시로 */}
      {!editing &&
        home.dirt
          .filter((s) => occluded(placedList, s.x, s.y))
          .map((s) => {
            const [sx, sy] = proj(s.x + 0.5, s.y + 0.5);
            return (
              <g
                key={`peek-${s.id}`}
                data-dirt={s.id}
                className="dirt"
                transform={`translate(${sx} ${sy - 4})`}
                role="button"
                tabIndex={-1}
                aria-hidden
                onClick={(ev) => {
                  ev.stopPropagation();
                  onDirt?.(s.id);
                }}
              >
                <circle r={9} fill="rgba(255,255,255,.9)" stroke="rgba(0,0,0,.15)" />
                <circle cx={-2.5} cy={0.5} r={2.6} fill="#A9A6A0" />
                <circle cx={2} cy={-0.5} r={3} fill="#B9B6B0" />
                <circle cx={0} cy={-2.6} r={2.2} fill="#C6C3BD" />
              </g>
            );
          })}

      {/* 밤 */}
      {night && (
        <g pointerEvents="none">
          <polygon
            points={pts([[0, GRID, 0], [0, GRID, WALL_H], [0, 0, WALL_H], [GRID, 0, WALL_H], [GRID, 0, 0], [GRID, GRID, 0]])}
            fill="rgba(14,18,46,0.34)"
          />
          {room.map((i) => {
            const def = itemDef(i.key);
            if (!def?.glow) return null;
            // 불빛이 나는 자리: 벽 장식은 벽의 눈높이, 바닥 물건은 돌린 넓이의 가운데 위쪽. 큰 등일수록 넓게.
            let at: [number, number];
            if (def.kind === "wall") {
              const u = i.x + def.w / 2;
              at = i.wall === "left" ? proj(0, GRID - u, WALL_H - 1.2) : proj(u, 0, WALL_H - 1.2);
            } else {
              const { w, d } = footprint(def, i.turn);
              at = proj(i.x + w / 2, i.y + d / 2, def.h * 0.8);
            }
            const r = def.kind === "wall" ? 64 : 40 + 30 * Math.min(def.h, 1.8);
            return <circle key={`glow-${i.id}`} cx={at[0]} cy={at[1]} r={r} fill={`url(#glow-${uid})`} style={{ mixBlendMode: "screen" }} />;
          })}
        </g>
      )}

      {/* 치운 자리 */}
      {poofs.map((p) => {
        const [sx, sy] = proj(p.x + 0.5, p.y + 0.5);
        return (
          <g key={p.key} className="poof" pointerEvents="none" transform={`translate(${sx} ${sy})`}>
            {[0, 60, 120, 180, 240, 300].map((a) => (
              <circle key={a} cx={Math.cos((a * Math.PI) / 180) * 9} cy={Math.sin((a * Math.PI) / 180) * 4.5} r={3} fill="#fff" />
            ))}
          </g>
        );
      })}

      {flies && (
        <g pointerEvents="none" transform={`translate(${proj(3.5, 3.5, 1.2).join(" ")})`}>
          <circle className="fly fly-a" r={1.4} fill="#2E3438" />
          <circle className="fly fly-b" r={1.2} fill="#2E3438" />
        </g>
      )}
    </svg>
  );
}

/**
 * 한 칸짜리 작은 물건(방석·간식 상자·스탠드·화분)의 보이지 않는 누르는 영역: 그 칸의 상자 윤곽, 높이는 적어도 0.6.
 * 넓은 가구(침대·소파)에는 붙이지 않는다 - 그 윤곽은 그림보다 훨씬 커서 뒤의 물건을 누르는 손가락을 가로챈다.
 */
function HitBox({ itemKey }: { itemKey: string }) {
  const def = itemDef(itemKey);
  if (!def || def.kind === "wall" || def.under || def.w * def.d !== 1) return null;
  const h = Math.max(def.h, 0.6);
  const { w, d } = def;
  return <polygon points={pts([[0, 0, h], [w, 0, h], [w, 0, 0], [w, d, 0], [0, d, 0], [0, d, h]])} fill="transparent" />;
}

function Cobweb() {
  const [cx, cy] = proj(0, 0, WALL_H);
  const r = 22;
  return (
    <g stroke="rgba(120,120,120,.55)" strokeWidth={0.6} fill="none" pointerEvents="none">
      {[200, 235, 270, 305, 340].map((a) => (
        <line key={a} x1={cx} y1={cy} x2={cx + Math.cos((a * Math.PI) / 180) * r} y2={cy - Math.sin((a * Math.PI) / 180) * r * 0.9} />
      ))}
      <path d={`M ${cx - 14} ${cy + 6} Q ${cx} ${cy + 12} ${cx + 14} ${cy + 6}`} />
      <path d={`M ${cx - 8} ${cy + 3} Q ${cx} ${cy + 7} ${cx + 8} ${cy + 3}`} />
      <path d={`M ${cx - 20} ${cy + 9} Q ${cx} ${cy + 18} ${cx + 20} ${cy + 9}`} />
    </g>
  );
}

function Wallpaper({ id, kind, ink }: { id: string; kind: string; ink: string }) {
  switch (kind) {
    case "paper_stripe":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={0.5} height={1}>
          <rect x={0} y={0} width={0.2} height={1} fill={ink} />
        </pattern>
      );
    case "paper_dot":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={0.42} height={0.42}>
          <circle cx={0.1} cy={0.1} r={0.05} fill={ink} />
          <circle cx={0.31} cy={0.31} r={0.05} fill={ink} />
        </pattern>
      );
    case "paper_check":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={0.5} height={0.5}>
          <rect x={0} y={0} width={0.5} height={0.09} fill={ink} opacity={0.8} />
          <rect x={0} y={0} width={0.09} height={0.5} fill={ink} opacity={0.8} />
        </pattern>
      );
    case "paper_wood":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={0.46} height={WALL_H}>
          <rect x={0} y={0} width={0.025} height={WALL_H} fill={ink} />
        </pattern>
      );
    case "paper_star":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={1.1} height={0.9}>
          <circle cx={0.2} cy={0.2} r={0.03} fill={ink} />
          <circle cx={0.75} cy={0.55} r={0.022} fill={ink} />
          <circle cx={0.45} cy={0.8} r={0.018} fill={ink} />
        </pattern>
      );
    default:
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={1} height={1}>
          <rect width={0} height={0} />
        </pattern>
      );
  }
}

function FloorPattern({ id, kind, ink }: { id: string; kind: string; ink: string }) {
  switch (kind) {
    case "floor_tile":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={1} height={1}>
          <rect x={0} y={0} width={1} height={0.03} fill={ink} />
          <rect x={0} y={0} width={0.03} height={1} fill={ink} />
        </pattern>
      );
    case "floor_carpet":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={0.25} height={0.25}>
          <circle cx={0.06} cy={0.06} r={0.025} fill={ink} />
          <circle cx={0.19} cy={0.17} r={0.02} fill={ink} />
        </pattern>
      );
    case "floor_herring":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={0.7} height={0.7}>
          <path d="M 0 0.35 L 0.35 0 M 0.35 0.7 L 0.7 0.35 M 0 0.35 L 0.35 0.7 M 0.35 0 L 0.7 0.35" stroke={ink} strokeWidth={0.03} />
        </pattern>
      );
    case "floor_grass":
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={0.5} height={0.5}>
          <path d="M 0.1 0.3 l 0.03 -0.12 M 0.3 0.15 l 0.02 -0.1 M 0.4 0.42 l 0.03 -0.11" stroke={ink} strokeWidth={0.03} strokeLinecap="round" />
        </pattern>
      );
    default:
      return (
        <pattern id={id} patternUnits="userSpaceOnUse" width={2} height={0.5}>
          <rect x={0} y={0} width={2} height={0.025} fill={ink} />
          <rect x={0.7} y={0} width={0.025} height={0.5} fill={ink} opacity={0.7} />
        </pattern>
      );
  }
}
