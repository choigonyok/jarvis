"use client";

import { useEffect, useId, useMemo, useState } from "react";
import type { Face } from "@/components/couple/korea-map";
import { photoSrc, type Visit } from "@/lib/couple";
import { cn } from "@/lib/utils";

/**
 * The world by country, drawn from public/maps/world.json
 * (scripts/world-map.mjs, Natural Earth). As on the date map, the land is the
 * page's own grey and a country we have been to carries its photo; a country
 * too small to see is a dot to tap. Picked, the map comes near it.
 */

export type Country = {
  code: string;
  name: string;
  continent: "asia" | "europe" | "africa" | "northAmerica" | "southAmerica" | "oceania" | "etc";
  d: string;
  box: [number, number, number, number];
  /** The largest piece's box, where the country has far islands: what the map comes near. */
  focus?: [number, number, number, number];
  dot?: boolean;
};
export type WorldMapData = { source: string; viewBox: string; countries: Country[] };

export const CONTINENTS: { key: Country["continent"]; name: string }[] = [
  { key: "asia", name: "아시아" },
  { key: "europe", name: "유럽" },
  { key: "africa", name: "아프리카" },
  { key: "northAmerica", name: "북아메리카" },
  { key: "southAmerica", name: "남아메리카" },
  { key: "oceania", name: "오세아니아" },
];

let loaded: Promise<WorldMapData> | null = null;

/** The outline, read once per page load. */
export function loadWorldMap(): Promise<WorldMapData> {
  loaded ??= fetch("/maps/world.json").then((r) => {
    if (!r.ok) throw new Error(String(r.status));
    return r.json() as Promise<WorldMapData>;
  });
  loaded.catch(() => {
    loaded = null;
  });
  return loaded;
}

export function useWorldMap() {
  const [map, setMap] = useState<WorldMapData | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    loadWorldMap().then(
      (m) => live && setMap(m),
      () => live && setFailed(true),
    );
    return () => {
      live = false;
    };
  }, []);
  return { map, failed };
}

/**
 * Each country's photo, as the app fills it: its face, or failing one the
 * newest photo of a trip there.
 */
export function worldFaces(visits: Visit[] = []): Map<string, Face> {
  const out = new Map<string, Face>();
  for (const v of visits) if (v.rep && v.asset) out.set(v.region, { asset: v.asset, clip: v.clip, rotation: v.rotation });
  const newest = new Map<string, Visit>();
  for (const v of visits) {
    if (v.rep || !v.asset || out.has(v.region)) continue;
    const cur = newest.get(v.region);
    if (!cur || v.createdAt > cur.createdAt) newest.set(v.region, v);
  }
  for (const [code, v] of newest) out.set(code, { asset: v.asset });
  return out;
}

export function WorldMap({
  map,
  faces,
  selected,
  onSelect,
  size = "thumb",
  className,
}: {
  map: WorldMapData;
  faces: Map<string, Face>;
  selected?: string | null;
  onSelect?: (code: string) => void;
  size?: "thumb" | "display";
  className?: string;
}) {
  const uid = useId().replace(/:/g, "");
  const chosen = selected ? map.countries.find((c) => c.code === selected) : undefined;
  // Near the country picked: its box, grown so its neighbours show too.
  const viewBox = useMemo(() => {
    if (!chosen) return map.viewBox;
    const [, , W, H] = map.viewBox.split(" ").map(Number);
    const [x, y, w, h] = chosen.focus ?? chosen.box;
    const side = Math.min(Math.max(w, h, 24) * 3, W);
    const vw = side;
    const vh = Math.min(side * 0.62, H);
    const vx = Math.min(Math.max(x + w / 2 - vw / 2, 0), W - vw);
    const vy = Math.min(Math.max(y + h / 2 - vh / 2, 0), H - vh);
    return `${vx} ${vy} ${vw} ${vh}`;
  }, [chosen, map.viewBox]);

  const ordered = useMemo(() => [...map.countries].sort((a, b) => Number(!!a.dot) - Number(!!b.dot)), [map.countries]);
  return (
    <svg viewBox={viewBox} role="img" aria-label="세계 지도" className={className}>
      <defs>
        {map.countries
          .filter((c) => faces.has(c.code))
          .map((c) => (
            <clipPath key={c.code} id={`${uid}-${c.code}`}>
              <path d={c.d} />
            </clipPath>
          ))}
      </defs>
      {ordered.map((c) => {
        const face = faces.get(c.code);
        const press = onSelect
          ? {
              onClick: () => onSelect(c.code),
              role: "button" as const,
              tabIndex: -1,
              "aria-label": `${c.name}${face ? " · 가 봤어요" : ""}`,
              className: "cursor-pointer outline-none",
            }
          : {};
        if (face) {
          const [x, y, w, h] = c.box;
          const cx = x + w / 2;
          const cy = y + h / 2;
          const turn = face.rotation ?? 0;
          const rad = (turn * Math.PI) / 180;
          const side = turn ? Math.max(w, h) * (Math.abs(Math.cos(rad)) + Math.abs(Math.sin(rad))) : 0;
          return (
            <g key={c.code} {...press}>
              <path d={c.d} className="fill-glass-raised" />
              <image
                href={photoSrc(face.clip ?? face.asset, size)}
                x={turn ? cx - side / 2 : x}
                y={turn ? cy - side / 2 : y}
                width={turn ? side : w}
                height={turn ? side : h}
                preserveAspectRatio="xMidYMid slice"
                clipPath={`url(#${uid}-${c.code})`}
                transform={turn ? `rotate(${turn} ${cx} ${cy})` : undefined}
              />
              <path d={c.d} fill="none" className="stroke-background" strokeWidth={0.5} vectorEffect="non-scaling-stroke" />
            </g>
          );
        }
        return (
          <path
            key={c.code}
            d={c.d}
            {...press}
            className={cn(c.dot ? "fill-dim" : "fill-glass-raised", "stroke-background", press.className)}
            strokeWidth={0.5}
            vectorEffect="non-scaling-stroke"
          />
        );
      })}
      {chosen ? <path d={chosen.d} fill="none" className="pointer-events-none stroke-foreground" strokeWidth={2} vectorEffect="non-scaling-stroke" /> : null}
    </svg>
  );
}
