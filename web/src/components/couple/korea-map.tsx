"use client";

import { useEffect, useId, useMemo, useState } from "react";
import { photoSrc } from "@/lib/couple";
import { cn } from "@/lib/utils";

/**
 * Korea by 시/군/구, drawn from public/maps/korea.json (scripts/korea-map.mjs).
 * The land is the page's own grey; a region we have been to carries its photo,
 * clipped to its shape - the only colour on the map, the way the location
 * tab's only light is the road walked.
 */

export type Shape = { code: string; name: string; d: string; box: [number, number, number, number]; at: [number, number] | null };
export type Region = Shape & { sido: string };
export type KoreaMapData = { source: string; viewBox: string; provinces: Shape[]; regions: Region[] };

/**
 * A region's photo: the clip the app's crop editor made of it (the photo
 * already placed and turned for the region) or else the photo, filling the
 * region's box, turned by rotation - as the app lays it.
 */
export type Face = { asset: string; clip?: string; rotation?: number };

let loaded: Promise<KoreaMapData> | null = null;

/** The outline, read once per page load. */
export function loadKoreaMap(): Promise<KoreaMapData> {
  loaded ??= fetch("/maps/korea.json").then((r) => {
    if (!r.ok) throw new Error(String(r.status));
    return r.json() as Promise<KoreaMapData>;
  });
  loaded.catch(() => {
    loaded = null;
  });
  return loaded;
}

export function useKoreaMap() {
  const [map, setMap] = useState<KoreaMapData | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    loadKoreaMap().then(
      (m) => live && setMap(m),
      () => live && setFailed(true),
    );
    return () => {
      live = false;
    };
  }, []);
  return { map, failed };
}

/** The share of the country's 시/군/구 we have been to, one decimal: "5.2%". */
export function percent(visited: number, total: number): string {
  if (total === 0) return "0%";
  return `${Math.round((visited / total) * 1000) / 10}%`;
}

function inViewOf(view: string, r: Region) {
  return view === "all" || r.sido === view;
}

/** The box a 시/도 takes, with room around it. */
export function provinceBox(map: KoreaMapData, sido: string): string {
  const rs = map.regions.filter((r) => r.sido === sido);
  if (rs.length === 0) return map.viewBox;
  let [x0, y0, x1, y1] = [Infinity, Infinity, -Infinity, -Infinity];
  for (const r of rs) {
    x0 = Math.min(x0, r.box[0]);
    y0 = Math.min(y0, r.box[1]);
    x1 = Math.max(x1, r.box[0] + r.box[2]);
    y1 = Math.max(y1, r.box[1] + r.box[3]);
  }
  // A small city is still a third of the map wide, or its 구 are slivers.
  const w = Math.max(x1 - x0, 60);
  const h = Math.max(y1 - y0, 60);
  const cx = (x0 + x1) / 2;
  const cy = (y0 + y1) / 2;
  const pad = 0.08;
  return `${cx - (w / 2) * (1 + pad)} ${cy - (h / 2) * (1 + pad)} ${w * (1 + pad)} ${h * (1 + pad)}`;
}

export function KoreaMap({
  map,
  view = "all",
  faces,
  marked,
  selected,
  onSelect,
  labels,
  size = "thumb",
  className,
}: {
  map: KoreaMapData;
  /** "all", or a 시/도 code to draw only it, near. */
  view?: string;
  faces: ReadonlyMap<string, Face>;
  /** Regions we have been to with no photo yet: drawn in ink, not left grey. */
  marked?: ReadonlySet<string>;
  selected?: string | null;
  onSelect?: (code: string) => void;
  labels?: boolean;
  /** Which size of each photo: a whole country of tiles wants the small one. */
  size?: "thumb" | "display";
  className?: string;
}) {
  const uid = useId().replace(/:/g, "");
  const viewBox = view === "all" ? map.viewBox : provinceBox(map, view);
  const width = Number(viewBox.split(" ")[2]);
  // Near, the 시/도 is drawn with its neighbours faint around it: Seoul is
  // not a hole in 경기.
  const regions = map.regions;
  const inView = (r: Region) => inViewOf(view, r);
  const provinces = map.provinces;
  const chosen = selected ? map.regions.find((r) => r.code === selected) : undefined;
  const font = width / 34;
  // A name as large as its region allows, and none where it would not fit.
  const labelled = useMemo(
    () =>
      labels
        ? map.regions
            .filter((r) => r.at && inViewOf(view, r))
            .map((r) => ({ r, size: Math.min(font, Math.min(r.box[2], r.box[3]) / 2.6) }))
            .filter((l) => l.size >= font * 0.45)
        : [],
    [labels, map, view, font],
  );

  return (
    <svg viewBox={viewBox} className={cn("block select-none", className)} role="img" aria-label={view === "all" ? "전국 지도" : `${provinces[0]?.name ?? ""} 지도`}>
      <defs>
        {regions
          .filter((r) => faces.has(r.code))
          .map((r) => (
            <clipPath key={r.code} id={`${uid}-${r.code}`}>
              <path d={r.d} />
            </clipPath>
          ))}
      </defs>
      {regions.map((r) => {
        const face = faces.get(r.code);
        const here = inView(r);
        const press = onSelect && here
          ? {
              onClick: () => onSelect(r.code),
              role: "button" as const,
              tabIndex: view === "all" ? -1 : 0,
              "aria-label": `${r.name}${face || marked?.has(r.code) ? " · 가 봤어요" : ""}`,
              onKeyDown: (e: React.KeyboardEvent) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  onSelect(r.code);
                }
              },
              className: "cursor-pointer outline-none focus-visible:[stroke:var(--foreground)] focus-visible:[stroke-width:2px]",
            }
          : {};
        if (face) {
          const [x, y, w, h] = r.box;
          const cx = x + w / 2;
          const cy = y + h / 2;
          // As the app lays it: the photo fills the region's box; turned, a
          // square grown until the turned photo still covers the box.
          const turn = face.rotation ?? 0;
          const rad = (turn * Math.PI) / 180;
          const side = turn ? Math.max(w, h) * (Math.abs(Math.cos(rad)) + Math.abs(Math.sin(rad))) : 0;
          return (
            <g key={r.code} {...press} opacity={here ? undefined : 0.35}>
              <path d={r.d} className="fill-glass-raised" />
              <image
                href={photoSrc(face.clip ?? face.asset, size)}
                x={turn ? cx - side / 2 : x}
                y={turn ? cy - side / 2 : y}
                width={turn ? side : w}
                height={turn ? side : h}
                preserveAspectRatio="xMidYMid slice"
                clipPath={`url(#${uid}-${r.code})`}
                transform={turn ? `rotate(${turn} ${cx} ${cy})` : undefined}
              />
              <path d={r.d} fill="none" className="stroke-background" strokeWidth={0.6} vectorEffect="non-scaling-stroke" />
            </g>
          );
        }
        return (
          <path
            key={r.code}
            d={r.d}
            {...press}
            className={cn(marked?.has(r.code) ? "fill-dim" : "fill-glass-raised", "stroke-background", press.className)}
            opacity={here ? undefined : 0.35}
            strokeWidth={0.6}
            vectorEffect="non-scaling-stroke"
          />
        );
      })}
      {provinces.map((p) => (
        <path key={p.code} d={p.d} fill="none" className="pointer-events-none stroke-edge" strokeWidth={1} vectorEffect="non-scaling-stroke" />
      ))}
      {chosen ? (
        <path d={chosen.d} fill="none" className="pointer-events-none stroke-foreground" strokeWidth={2} vectorEffect="non-scaling-stroke" />
      ) : null}
      {labelled.map(({ r, size: s }) => (
        <text
          key={`l${r.code}`}
          x={r.at![0]}
          y={r.at![1]}
          fontSize={s}
          textAnchor="middle"
          dominantBaseline="central"
          className="pointer-events-none fill-ink [paint-order:stroke] stroke-background font-medium"
          strokeWidth={s / 5}
          strokeLinejoin="round"
        >
          {r.name}
        </text>
      ))}
    </svg>
  );
}
