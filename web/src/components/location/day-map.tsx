"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { Map as MapLibre } from "maplibre-gl";
import "maplibre-gl/dist/maplibre-gl.css";
import { Maximize2 } from "lucide-react";
import { baseStyle } from "@/components/location/map-style";
import { type Day, type Mode, type Node, type Stay, headAt, hm } from "@/lib/track";

/** What is laid over the map, inside it: the light, the places' names, the point of light. */
type Layer = {
  canvas: HTMLCanvasElement;
  dot: HTMLDivElement;
  names: Map<string, { el: HTMLDivElement; text: string; w: number; h: number }>;
  root: HTMLElement;
};

/**
 * The day on a map, as light: the way gone so far up to the moment shown,
 * drawn the way a long exposure records headlights. What is recent is bright
 * and what is hours old has dimmed; a road gone twice is brighter than one
 * gone once, because light adds up. A walk is a line of dots, a way the phone
 * said nothing about a faint dash. The places stayed at are rings with their
 * names and hours, and the moment shown is a point of light - breathing at
 * the end of today, because that one is now.
 *
 * The base map is MapLibre's (vector tiles through /api/map). The light, the
 * names and the point are this component's own, inside the map over its
 * canvas, placed again with every frame the map draws and every frame of the
 * replay.
 */
export function DayMap({ day, t, live, follow }: { day: Day; t: number; live: boolean; follow: boolean }) {
  const box = useRef<HTMLDivElement>(null);
  const map = useRef<MapLibre | null>(null);
  const layer = useRef<Layer | null>(null);
  const [ready, setReady] = useState(false);
  const [broken, setBroken] = useState<string | null>(null);
  // What a draw reads: the map's own events call it outside React.
  const now = useRef({ day, t, live, follow });
  useLayoutEffect(() => {
    now.current = { day, t, live, follow };
  });

  useEffect(() => {
    let gone = false;
    let made: MapLibre | null = null;
    import("maplibre-gl")
      .then((maplibre) => {
        if (gone || !box.current) return;
        maplibre.setWorkerUrl(new URL("maplibre-gl/dist/maplibre-gl-worker.mjs", import.meta.url).href);
        const { bounds } = now.current.day;
        made = new maplibre.Map({
          container: box.current,
          style: baseStyle(`${location.origin}/api/map/planet`, consoleFonts()),
          ...(bounds
            ? { bounds, fitBoundsOptions: { padding: padding(box.current), maxZoom: 15 } }
            : { center: [126.978, 37.5665], zoom: 10 }),
          // The tiles come through this page's own origin, whatever origin the
          // server wrote into the tileset: that is where the session cookie goes.
          transformRequest: (url) => {
            const u = new URL(url, location.origin);
            return { url: u.pathname.startsWith("/api/map/") ? `${location.origin}${u.pathname}${u.search}` : url };
          },
          attributionControl: false,
          locale: { "Map.Title": "하루 동안 다닌 길 지도" },
          dragRotate: false,
          pitchWithRotate: false,
          touchPitch: false,
          maxPitch: 0,
          renderWorldCopies: false,
        });
        made.touchZoomRotate.disableRotation();
        made.keyboard.disableRotation();
        // Over the map's own canvas, in this order: the light, the names, the point.
        const root = made.getCanvasContainer();
        const canvas = document.createElement("canvas");
        canvas.setAttribute("aria-hidden", "true");
        canvas.className = "pointer-events-none absolute top-0 left-0";
        const dot = pointOfLight();
        root.append(canvas, dot);
        layer.current = { canvas, dot, names: new Map(), root };
        let loaded = false;
        made.on("render", draw);
        made.on("load", () => {
          loaded = true;
          if (!gone) setReady(true);
        });
        made.on("error", (e) => {
          // A tile that does not come is the map's own business; a base map
          // that cannot load at all is said here.
          if (!loaded && !(e as { tile?: unknown }).tile && !gone) setBroken("지도 바탕을 불러오지 못했어요.");
        });
        map.current = made;
      })
      .catch(() => !gone && setBroken("지도를 불러오지 못했어요."));
    return () => {
      gone = true;
      made?.remove();
      map.current = null;
      layer.current = null;
    };
    // Made once; everything after reads `now`.
  }, []);

  // Another day opens on its own view; the same day read again keeps the view.
  const shown = useRef(day.date);
  useEffect(() => {
    if (shown.current !== day.date) {
      shown.current = day.date;
      fit(false);
    }
    draw();
  }, [day, ready]);

  // Drawn before the frame is painted, with the clock beside it.
  useLayoutEffect(() => {
    draw();
    keepInView();
  }, [t, live]);

  function fit(animate: boolean) {
    const m = map.current;
    const b = now.current.day.bounds;
    if (!m || !b) return;
    m.fitBounds(b, { padding: padding(m.getContainer()), maxZoom: 15, duration: animate && !reducedMotion() ? 600 : 0 });
  }

  /** While the day replays, the map follows the point of light when it nears an edge. */
  function keepInView() {
    const m = map.current;
    const { day, t, follow } = now.current;
    if (!m || !follow || m.isMoving()) return;
    const h = headAt(day, t);
    if (!h) return;
    const p = m.project([h.lon, h.lat]);
    const { clientWidth: w, clientHeight: hgt } = m.getContainer();
    const inset = Math.min(w, hgt) * 0.12;
    if (p.x < inset || p.y < inset || p.x > w - inset || p.y > hgt - inset) {
      m.easeTo({ center: [h.lon, h.lat], duration: reducedMotion() ? 0 : 500 });
    }
  }

  function draw() {
    const m = map.current;
    const l = layer.current;
    if (!m || !l) return;
    const { day, t, live } = now.current;
    const { clientWidth: w, clientHeight: h } = m.getContainer();
    const { canvas } = l;
    const dpr = Math.min(window.devicePixelRatio || 1, 3);
    if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
      canvas.width = Math.round(w * dpr);
      canvas.height = Math.round(h * dpr);
      canvas.style.width = `${w}px`;
      canvas.style.height = `${h}px`;
    }
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);
    const at = (p: { lat: number; lon: number }) => m.project([p.lon, p.lat]);

    // The way so far, as light that adds up where it crosses itself. Each
    // stretch of one mode and one brightness is a single line: drawn fix by
    // fix, the overlapping ends of the pieces would add up into beads.
    ctx.globalCompositeOperation = "lighter";
    ctx.lineJoin = "round";
    const position = headAt(day, t);
    type Run = { mode: Mode; level: number; points: { x: number; y: number }[]; offset: number };
    const runs: Run[] = [];
    let along = 0; // how far along one mode, so a dotted walk does not restart its dots
    for (const s of day.steps) {
      if (s.t0 >= t) break;
      const a = at(s.a);
      const b = s.t1 <= t || !position ? at(s.b) : at(position);
      const level = Math.round(brightness(Math.min(s.t1, t), t) * 24) / 24;
      const last = runs.at(-1);
      if (!last || last.mode !== s.mode || last.level !== level) {
        if (last?.mode !== s.mode) along = 0;
        runs.push({ mode: s.mode, level, points: [a], offset: along });
      }
      runs.at(-1)!.points.push(b);
      along += Math.hypot(b.x - a.x, b.y - a.y);
    }
    for (const r of runs) {
      if (r.points.length < 2) continue;
      const style = STROKE[r.mode];
      ctx.setLineDash(style.dash);
      ctx.lineDashOffset = -r.offset;
      // Dots need round ends; a line ends square, so two stretches meet without a bead.
      ctx.lineCap = style.dash.length ? "round" : "butt";
      ctx.beginPath();
      ctx.moveTo(r.points[0].x, r.points[0].y);
      for (const q of r.points.slice(1)) ctx.lineTo(q.x, q.y);
      // A soft spread under the line, then the line.
      ctx.strokeStyle = `rgba(236, 237, 238, ${r.level * style.alpha * 0.11})`;
      ctx.lineWidth = style.width * 3.2;
      ctx.stroke();
      ctx.strokeStyle = `rgba(236, 237, 238, ${r.level * style.alpha})`;
      ctx.lineWidth = style.width;
      ctx.stroke();
    }
    ctx.setLineDash([]);

    // The places stayed at, once reached: a ring cut out of the light.
    ctx.globalCompositeOperation = "source-over";
    for (const n of day.nodes) {
      if (!n.stay || n.start > t) continue;
      const p = at(n);
      ctx.beginPath();
      ctx.arc(p.x, p.y, 5.5, 0, Math.PI * 2);
      ctx.fillStyle = "#0c0d0f";
      ctx.fill();
      ctx.lineWidth = 1.5;
      ctx.strokeStyle = `rgba(236, 237, 238, ${position?.node === n ? 0.95 : 0.5})`;
      ctx.stroke();
    }

    // The point of light: where the operator was at t.
    const p = position ? at(position) : null;
    l.dot.style.display = p ? "" : "none";
    if (p) l.dot.style.transform = `translate(${p.x - 22}px, ${p.y - 22}px)`;
    l.dot.firstElementChild?.classList.toggle("anim-breathe", live);

    name(l, day, t, at, w);
  }

  return (
    <div className="absolute inset-0" data-no-pull>
      {/* Sized by height, not by inset: MapLibre's own stylesheet makes this box position: relative. */}
      <div ref={box} className="size-full bg-background" />
      {broken !== null ? (
        <p role="alert" className="absolute inset-x-0 top-1/2 -translate-y-1/2 px-8 text-center text-[13px] text-dim">
          {broken}
        </p>
      ) : null}
      {day.bounds ? (
        <button
          type="button"
          onClick={() => fit(true)}
          aria-label="하루 전체 보기"
          title="하루 전체 보기"
          className="absolute top-3 right-3 flex size-11 items-center justify-center rounded-full bg-popover text-dim shadow-[0_6px_16px_-6px_oklch(0_0_0/70%)] transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 mouse:size-9"
        >
          <Maximize2 aria-hidden className="size-4" strokeWidth={1.8} />
        </button>
      ) : null}
      <p className="absolute bottom-1.5 left-2.5 text-[10px] text-faint/70">
        <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer" className="hover:text-dim">
          © OpenStreetMap
        </a>
        {" · "}
        <a href="https://openfreemap.org" target="_blank" rel="noreferrer" className="hover:text-dim">
          OpenFreeMap
        </a>
      </p>
    </div>
  );
}

/** How each way is drawn: line width (px), dashes, and its strength against a drive. */
const STROKE: Record<Mode, { width: number; dash: number[]; alpha: number }> = {
  drive: { width: 2.4, dash: [], alpha: 1 },
  move: { width: 2.2, dash: [], alpha: 0.9 },
  walk: { width: 2.6, dash: [0.1, 5], alpha: 0.95 },
  gap: { width: 1.3, dash: [3, 6], alpha: 0.5 },
};

/** The light of a way gone at `when`, seen at `t`: full when fresh, under half after a few hours. */
function brightness(when: number, t: number): number {
  const floor = 0.42;
  return floor + (1 - floor) * Math.exp(-Math.max(0, t - when) / (75 * 60_000));
}

/** The point of light: a soft glow (which breathes when it is now) and the dot. */
function pointOfLight(): HTMLDivElement {
  const el = document.createElement("div");
  el.className = "pointer-events-none absolute top-0 left-0 size-11";
  const halo = document.createElement("span");
  halo.className = "absolute inset-0 rounded-full";
  halo.style.background = "radial-gradient(circle, rgba(236,237,238,0.42) 0%, rgba(236,237,238,0.12) 42%, transparent 70%)";
  const dot = document.createElement("span");
  dot.className = "absolute top-1/2 left-1/2 size-[11px] -translate-1/2 rounded-full bg-foreground";
  dot.style.boxShadow = "0 0 0 3px rgba(12,13,15,0.7)";
  el.replaceChildren(halo, dot);
  return el;
}

type Place = { key: string; node: Node; name?: string; stays: Stay[] };

const placesCache = new WeakMap<Day, Place[]>();

/**
 * The day's stays by place - by name, or for a nameless place by where it is
 * - in the order their names give way: named before nameless, then the
 * longest stayed.
 */
function placesOf(day: Day): Place[] {
  const cached = placesCache.get(day);
  if (cached) return cached;
  const by = new Map<string, Place>();
  for (const it of day.items) {
    if (it.type !== "stay") continue;
    const key = it.node.name ?? `${it.node.lat.toFixed(3)},${it.node.lon.toFixed(3)}`;
    const place = by.get(key) ?? { key, node: it.node, name: it.node.name, stays: [] };
    place.stays.push(it);
    by.set(key, place);
  }
  const stayed = (p: Place) => p.stays.reduce((s, x) => s + (x.end - x.start), 0);
  const out = [...by.values()].sort((a, b) => Number(!a.name) - Number(!b.name) || stayed(b) - stayed(a));
  placesCache.set(day, out);
  return out;
}

/**
 * The places' names, from the moment each is first reached, with its hours
 * so far. A name sits to the right of its ring, or to the left where the
 * right runs off the map or into another name; where neither side is free it
 * waits until the map is zoomed in far enough.
 */
function name(l: Layer, day: Day, t: number, at: (p: Node) => { x: number; y: number }, w: number) {
  const places = placesOf(day);
  const keep = new Set(places.map((p) => p.key));
  for (const [key, n] of l.names) {
    if (keep.has(key)) continue;
    n.el.remove();
    l.names.delete(key);
  }
  const taken: { x: number; y: number; w: number; h: number }[] = [];
  for (const place of places) {
    let n = l.names.get(place.key);
    if (!n) {
      const el = document.createElement("div");
      el.className = "pointer-events-none absolute top-0 left-0 flex flex-col font-sans leading-[1.25] whitespace-nowrap [text-shadow:0_0_6px_#0c0d0f,0_0_2px_#0c0d0f]";
      l.root.insertBefore(el, l.dot);
      n = { el, text: "", w: 0, h: 0 };
      l.names.set(place.key, n);
    }
    const seen = place.stays.filter((s) => s.start <= t);
    const hours = seen.map((s) => range(day, s, t)).filter(Boolean).join(" · ");
    const text = seen.length ? `${place.name ?? ""}|${hours}` : "";
    if (text !== n.text) {
      n.text = text;
      const title = document.createElement("b");
      title.className = "text-[12.5px] font-medium text-foreground/90";
      title.textContent = place.name ?? "";
      const when = document.createElement("span");
      when.className = "tnum text-[11px] text-dim";
      when.textContent = hours;
      n.el.replaceChildren(...(place.name ? [title] : []), ...(hours ? [when] : []));
      n.el.style.display = "";
      n.w = n.el.offsetWidth;
      n.h = n.el.offsetHeight;
    }
    if (!text || !(place.name || hours)) {
      n.el.style.display = "none";
      continue;
    }
    const p = at(place.node);
    const y = p.y - n.h / 2;
    const free = (x: number) =>
      x >= 4 &&
      x + n.w <= w - 4 &&
      !taken.some((b) => x < b.x + b.w + 6 && x + n.w + 6 > b.x && y < b.y + b.h && y + n.h > b.y);
    const right = p.x + 12;
    const left = p.x - 12 - n.w;
    const x = free(right) ? right : free(left) ? left : null;
    if (x === null) {
      n.el.style.display = "none";
      continue;
    }
    n.el.style.display = "";
    n.el.style.textAlign = x === left ? "right" : "left";
    n.el.style.transform = `translate(${Math.round(x)}px, ${Math.round(y)}px)`;
    taken.push({ x, y, w: n.w, h: n.h });
  }
}

/**
 * A stay's hours as far as t has reached: "08:51–18:42" once left, "08:51~"
 * while still there, "~08:06" for one begun before the day. Nothing for a
 * place held since before the day and not yet left.
 */
function range(day: Day, s: Stay, t: number): string {
  const from = s.start <= day.start ? "" : hm(s.start);
  const open = t < s.end || s.ongoing || s.end >= day.dayEnd;
  if (open) return from ? `${from}~` : "";
  return from ? `${from}–${hm(s.end)}` : `~${hm(s.end)}`;
}

/** The console's face, as the map's renderer asks for it: family names, the medium weight by name. */
function consoleFonts() {
  const raw = getComputedStyle(document.documentElement).getPropertyValue("--font-instrument");
  const own = raw
    .split(",")
    .map((f) => f.trim().replace(/^['"]|['"]$/g, ""))
    .filter(Boolean)[0];
  // Hangul is not in it: the system's Korean face takes over, as on the rest of the page.
  const korean = ["Apple SD Gothic Neo", "Noto Sans KR", "Malgun Gothic"];
  return own ? { regular: [own, ...korean], medium: [`${own} Medium`, own, ...korean] } : { regular: korean, medium: korean };
}

/** Room around the day: more where the map's own button sits (top right). */
function padding(el: HTMLElement) {
  const p = Math.round(Math.min(el.clientWidth, el.clientHeight) * 0.1);
  return { top: Math.max(p, 60), bottom: Math.max(p, 28), left: Math.max(p, 28), right: Math.max(p, 60) };
}

const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;
