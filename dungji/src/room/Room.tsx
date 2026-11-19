// 둥지의 방 한 칸: 벽, 바닥, 물건, 먼지, 밤의 불빛. 꾸미기 중에는 끌어서 옮긴다.

import { useId, useMemo, useRef, useState, type PointerEvent as RPointerEvent, type ReactNode } from "react";
import { GRID, itemDef } from "../../shared/catalog";
import type { HomeView, ItemView } from "../../shared/api";
import { fitsFloor, fitsWall, footprint, occluded, type Placed } from "../../shared/rules";
import { drawDirt, drawItem, drawWall, type DrawCtx } from "./art";
import { Flat, FlipProvider, FLOOR_FRAME, LEFT_WALL_FRAME, proj, pts, RIGHT_WALL_FRAME, shade, unproj, WALL_H } from "./iso";
import type { Phase } from "../lib/time";

export interface MoveTo {
  x: number;
  y: number;
  flip: boolean;
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
  /** 방금 치운 먼지: 그 자리에 잠깐 퍼지는 효과 */
  poofs?: { key: string; x: number; y: number }[];
  /** 주인에게만: 목마른 화분·배고픈 물고기 표시 */
  showNeeds?: boolean;
  label: string;
}

const T = 0.18; // 벽 두께
const SLAB = 0.32; // 바닥 두께

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
  flip: boolean;
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

export function Room({ home, phase, now, editing, selected, onSelect, onMove, onDirt, onItem, poofs = [], showNeeds, label }: Props) {
  const uid = useId().replace(/:/g, "");
  const svgRef = useRef<SVGSVGElement>(null);
  const [drag, setDrag] = useState<{ id: string; to: MoveTo; valid: boolean; moved: boolean } | null>(null);
  const night = phase === "night";
  const ctxBase: DrawCtx = { id: uid, night, now, sky: SKY[phase] };
  const wp = WALLPAPER[home.wallpaper] ?? WALLPAPER.paper_plain;
  const fl = FLOORS[home.floor] ?? FLOORS.floor_wood;

  const room = useMemo(() => home.items.filter((i) => i.place === "room"), [home.items]);
  const placedList: Placed[] = useMemo(
    () => room.map((i) => ({ id: i.id, key: i.key, x: i.x, y: i.y, flip: i.flip, wall: i.wall })),
    [room],
  );

  const position = (i: ItemView): MoveTo => (drag && drag.id === i.id ? drag.to : { x: i.x, y: i.y, flip: i.flip, wall: i.wall });

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
    const fp = footprint(def, p.flip);
    const entry = { item: i, x: p.x, y: p.y, w: fp.w, d: fp.d, flip: p.flip };
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

  function candidate(item: ItemView, sx: number, sy: number): { to: MoveTo; valid: boolean } | null {
    const def = itemDef(item.key)!;
    if (def.kind === "wall") {
      const ur = sx / 28;
      const wr = (sy - 14 * ur + 28 * WALL_H) / 28;
      const ul = (sx + 196) / 28;
      const wl = (sy + 14 * ul - 98 + 28 * WALL_H) / 28;
      const pick = (u: number, w: number, wall: "left" | "right") => {
        if (u < -0.5 || u > GRID + 0.5 || w < -0.3 || w > WALL_H + 0.3) return null;
        const pos = Math.max(0, Math.min(GRID - def.w, Math.round(u - def.w / 2)));
        return { to: { x: pos, y: 0, flip: false, wall }, valid: fitsWall(placedList, def, wall, pos, item.id) };
      };
      return sx >= 0 ? (pick(ur, wr, "right") ?? pick(ul, wl, "left")) : (pick(ul, wl, "left") ?? pick(ur, wr, "right"));
    }
    const [gx, gy] = unproj(sx, sy);
    const flip = position(item).flip;
    const fp = footprint(def, flip);
    const x = Math.max(0, Math.min(GRID - fp.w, Math.round(gx - fp.w / 2)));
    const y = Math.max(0, Math.min(GRID - fp.d, Math.round(gy - fp.d / 2)));
    return { to: { x, y, flip, wall: null }, valid: fitsFloor(placedList, def, x, y, flip, item.id) };
  }

  function onItemDown(e: RPointerEvent, item: ItemView) {
    if (!editing) return;
    e.stopPropagation();
    if (selected !== item.id) {
      onSelect?.(item.id);
      return;
    }
    svgRef.current?.setPointerCapture(e.pointerId);
    setDrag({ id: item.id, to: position(item), valid: true, moved: false });
  }

  function onMovePointer(e: RPointerEvent) {
    if (!drag) return;
    const item = room.find((i) => i.id === drag.id);
    if (!item) return;
    const p = svgPoint(e);
    const c = candidate(item, p.x, p.y);
    if (!c) return;
    const same = c.to.x === drag.to.x && c.to.y === drag.to.y && c.to.wall === drag.to.wall;
    if (!same) setDrag({ id: drag.id, to: c.to, valid: c.valid, moved: true });
  }

  function onUp() {
    if (!drag) return;
    const item = room.find((i) => i.id === drag.id);
    const changed = item && (drag.to.x !== item.x || drag.to.y !== item.y || drag.to.wall !== item.wall);
    if (item && drag.moved && drag.valid && changed) onMove?.(item, drag.to);
    setDrag(null);
  }

  /** 꾸미기 중 빈 바닥을 누르면 고른 물건을 그리로 옮긴다. */
  function onBackgroundDown(e: RPointerEvent) {
    if (!editing) return;
    if (!selected) return;
    const item = room.find((i) => i.id === selected);
    if (!item) return;
    const p = svgPoint(e);
    const c = candidate(item, p.x, p.y);
    if (c?.valid && (c.to.x !== item.x || c.to.y !== item.y || c.to.wall !== item.wall)) onMove?.(item, c.to);
    else onSelect?.(null);
  }

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
        <g transform={e.flip ? "scale(-1 1)" : undefined}>
          <FlipProvider flip={e.flip}>{drawItem(e.item.key, ctx)}</FlipProvider>
        </g>
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

  /** 꾸미기 중 고른 물건을 화살표로 한 칸씩 옮긴다. */
  function onKey(ev: React.KeyboardEvent) {
    if (!editing || !selected) return;
    const item = room.find((i) => i.id === selected);
    const def = item && itemDef(item.key);
    if (!item || !def) return;
    const step: Record<string, [number, number]> = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };
    const d = step[ev.key];
    if (!d) return;
    ev.preventDefault();
    if (def.kind === "wall") {
      const pos = item.x + (d[0] || d[1]) * (item.wall === "left" ? -1 : 1);
      if (fitsWall(placedList, def, item.wall ?? "right", pos, item.id)) onMove?.(item, { x: pos, y: 0, flip: false, wall: item.wall });
    } else if (fitsFloor(placedList, def, item.x + d[0], item.y + d[1], item.flip, item.id))
      onMove?.(item, { x: item.x + d[0], y: item.y + d[1], flip: item.flip, wall: null });
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
      <g key={`need-${e.item.id}`} transform={`translate(${x} ${y})`} className="need-bob" pointerEvents="none">
        <circle r={8.5} fill="rgba(255,255,255,.92)" stroke="rgba(0,0,0,.12)" />
        {icon}
      </g>
    );
  };

  const sorted = depthSort(floorItems);

  return (
    <svg
      ref={svgRef}
      viewBox="-210 -110 420 322"
      className={`room-svg${editing ? " is-editing" : ""}`}
      role="group"
      aria-label={label}
      onKeyDown={onKey}
      onPointerMove={onMovePointer}
      onPointerUp={onUp}
      onPointerCancel={() => setDrag(null)}
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
                fill={drag && !drag.valid ? "rgba(194,69,58,.2)" : "rgba(79,167,156,.2)"}
                stroke={drag && !drag.valid ? "#C2453A" : "#2E8A7F"}
                strokeWidth={0.04}
                strokeDasharray="0.14 0.08"
              />
            )}
            {drawWall(i.key, p.x, ctxBase)}
          </g>
        );
      })}

      {/* 러그 */}
      {unders.map((e) => itemNode(e))}

      {/* 고른 물건의 자리 */}
      {editing &&
        [...floorItems, ...unders]
          .filter((e) => e.item.id === selected)
          .map((e) => (
            <Flat key="sel" x={e.x} y={e.y} w={e.w} d={e.d} fill={drag && !drag.valid ? "rgba(194,69,58,.35)" : "rgba(79,167,156,.32)"} stroke={drag && !drag.valid ? "#C2453A" : "#2E8A7F"} />
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
            <polygon points={pts([[s.x, s.y, 0], [s.x + 1, s.y, 0], [s.x + 1, s.y + 1, 0], [s.x, s.y + 1, 0]])} fill="transparent" />
            {drawDirt(s.kind, sx, sy)}
          </g>
        );
      })}

      {/* 가구 */}
      {sorted.map((e) => itemNode(e))}
      {sorted.map((e) => needs(e))}

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
          {room
            .filter((i) => i.key === "lamp_floor")
            .map((i) => {
              const [gx, gy] = proj(i.x + 0.5, i.y + 0.5, 1.5);
              return <circle key={`glow-${i.id}`} cx={gx} cy={gy} r={92} fill={`url(#glow-${uid})`} style={{ mixBlendMode: "screen" }} />;
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
