"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { Map as MapLibre } from "maplibre-gl";
import "maplibre-gl/dist/maplibre-gl.css";
import { Maximize2 } from "lucide-react";
import { baseStyle } from "@/components/location/map-style";
import { type Day, type Mode, type Node, type Person, type Scene, type Stay, type Who, headAt, hm, meters } from "@/lib/track";

/** What is laid over the map, inside it: the light, the places' names, the people's marks. */
type Layer = {
  canvas: HTMLCanvasElement;
  marks: Map<Who, HTMLDivElement>;
  names: Map<string, { el: HTMLDivElement; text: string; w: number; h: number }>;
  root: HTMLElement;
};

/**
 * Each person's light, in the shared calendar's colors: the viewer's own is
 * mine (violet), the other's partner (amber) - the tokens in globals.css,
 * worked out to rgb for the canvas. Where both went the same way, the two add
 * up towards white.
 */
const TONE = { mine: "194, 167, 244", partner: "246, 187, 112" } as const;
const toneOf = (p: Person) => TONE[p.me ? "mine" : "partner"];

/**
 * A place being made one of a phone's regions (위치 등록): its circle on the
 * map, dashed while it is being named and drawn whole once it is made - so
 * how far 50 m reaches is seen, not imagined.
 */
export type Spot = { lat: number; lon: number; radius: number; name: string; tone: keyof typeof TONE; made?: boolean };

/**
 * The day on a map, as light: the way gone so far up to the moment shown,
 * drawn the way a long exposure records headlights. What is recent is bright
 * and what is hours old has dimmed; a road gone twice is brighter than one
 * gone once, because light adds up. A walk is a line of dots, a way the phone
 * said nothing about a faint dash. The places stayed at are rings with their
 * names, and where each person was at the moment shown is their mark - a
 * cat's face each chose, grey for the operator and cream for the guest - in a
 * soft glow that breathes at the end of today, because that one is now.
 *
 * One person or both: both share the map, each in their own color, and the
 * names then say only the place (whose hours are whose is the strip's and the
 * list's to say).
 *
 * The base map is MapLibre's (vector tiles through /api/map). The light, the
 * names and the marks are this component's own, inside the map over its
 * canvas, placed again with every frame the map draws and every frame of the
 * replay.
 */
export function DayMap({
  scene,
  t,
  live,
  follow,
  spot = null,
}: {
  scene: Scene;
  t: number;
  live: boolean;
  follow: boolean;
  spot?: Spot | null;
}) {
  const box = useRef<HTMLDivElement>(null);
  const map = useRef<MapLibre | null>(null);
  const layer = useRef<Layer | null>(null);
  const [ready, setReady] = useState(false);
  const [broken, setBroken] = useState<string | null>(null);
  // What a draw reads: the map's own events call it outside React.
  const now = useRef({ scene, t, live, follow, spot });
  useLayoutEffect(() => {
    now.current = { scene, t, live, follow, spot };
  });

  useEffect(() => {
    let gone = false;
    let made: MapLibre | null = null;
    import("maplibre-gl")
      .then((maplibre) => {
        if (gone || !box.current) return;
        maplibre.setWorkerUrl(new URL("maplibre-gl/dist/maplibre-gl-worker.mjs", import.meta.url).href);
        const { bounds } = now.current.scene;
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
        // Over the map's own canvas, in this order: the light, the names, the marks.
        const root = made.getCanvasContainer();
        const canvas = document.createElement("canvas");
        canvas.setAttribute("aria-hidden", "true");
        canvas.className = "pointer-events-none absolute top-0 left-0";
        root.append(canvas);
        layer.current = { canvas, marks: new Map(), names: new Map(), root };
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

  // Another day, or other people, open on their own view; the same read
  // again keeps the view.
  const shown = useRef(viewKey(scene));
  useEffect(() => {
    if (shown.current !== viewKey(scene)) {
      shown.current = viewKey(scene);
      fit(false);
    }
    draw();
  }, [scene, ready]);

  // Drawn before the frame is painted, with the clock beside it.
  useLayoutEffect(() => {
    draw();
    keepInView();
  }, [t, live]);

  // A place being made a region: the map goes to it, close enough to see its
  // circle; done with, back to the whole day.
  const spotAt = spot ? `${spot.lat.toFixed(6)},${spot.lon.toFixed(6)}` : "";
  const spotted = useRef(false);
  useEffect(() => {
    const m = map.current;
    const s = now.current.spot;
    if (!m || !ready) return;
    if (s) {
      spotted.current = true;
      m.easeTo({ center: [s.lon, s.lat], zoom: Math.max(m.getZoom(), 16.6), duration: reducedMotion() ? 0 : 700 });
    } else if (spotted.current) {
      spotted.current = false;
      fit(true);
    }
  }, [spotAt, ready]);
  useLayoutEffect(() => {
    draw();
  }, [spot]);

  function fit(animate: boolean) {
    const m = map.current;
    const b = now.current.scene.bounds;
    if (!m || !b) return;
    m.fitBounds(b, { padding: padding(m.getContainer()), maxZoom: 15, duration: animate && !reducedMotion() ? 600 : 0 });
  }

  /** While one person's day replays, the map follows their mark when it nears an edge. */
  function keepInView() {
    const m = map.current;
    const { scene, t, follow } = now.current;
    if (!m || !follow || scene.people.length !== 1 || m.isMoving()) return;
    const h = headAt(scene.people[0].day, t);
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
    const { scene, t, live } = now.current;
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

    // The ways so far, as light that adds up where it crosses itself - and
    // where the two went together.
    ctx.globalCompositeOperation = "lighter";
    ctx.lineJoin = "round";
    for (const person of scene.people) light(ctx, person, t, at);
    ctx.setLineDash([]);

    // The places stayed at, once reached: a ring cut out of the light.
    ctx.globalCompositeOperation = "source-over";
    const positions = scene.people.map((person) => ({ person, position: headAt(person.day, t) }));
    for (const { person, position } of positions) {
      for (const n of person.day.nodes) {
        if (!n.stay || n.start > t) continue;
        const p = at(n);
        ctx.beginPath();
        ctx.arc(p.x, p.y, 5.5, 0, Math.PI * 2);
        ctx.fillStyle = "#0c0d0f";
        ctx.fill();
        ctx.lineWidth = 1.5;
        ctx.strokeStyle = `rgba(${toneOf(person)}, ${position?.node === n ? 0.95 : 0.55})`;
        ctx.stroke();
      }
    }

    if (now.current.spot) circle(ctx, now.current.spot, at, w, getComputedStyle(m.getContainer()).fontFamily);

    // Each person's mark: where they were at t. Two at the same place stand
    // side by side, so neither face hides the other.
    const marked: { x: number; y: number }[] = [];
    for (const [who, el] of l.marks) {
      if (!scene.people.some((p) => p.who === who)) el.style.display = "none";
    }
    const points: ({ x: number; y: number } | null)[] = positions.map(({ position }) => (position ? at(position) : null));
    const [a, b] = points;
    if (a && b && Math.hypot(a.x - b.x, a.y - b.y) < 24) {
      const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
      points[0] = { x: mid.x - 13, y: mid.y };
      points[1] = { x: mid.x + 13, y: mid.y };
    }
    positions.forEach(({ person }, i) => {
      let el = l.marks.get(person.who);
      if (!el) {
        el = mark(person.who);
        l.root.append(el);
        l.marks.set(person.who, el);
      }
      const p = points[i];
      el.style.display = p ? "" : "none";
      if (!p) return;
      marked.push(p);
      el.style.transform = `translate(${p.x - 21}px, ${p.y - 21}px)`;
      const halo = el.firstElementChild as HTMLElement | null;
      if (halo) {
        halo.style.background = `radial-gradient(circle, rgba(${toneOf(person)}, 0.5) 0%, rgba(${toneOf(person)}, 0.16) 45%, transparent 70%)`;
        halo.classList.toggle("anim-breathe", live);
      }
    });

    name(l, scene, t, at, w, marked);
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
      {scene.bounds ? (
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

/** Which day and which people: what, when it changes, the map fits to anew. */
const viewKey = (scene: Scene) => `${scene.date}:${scene.people.map((p) => p.who).join("+")}`;

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

/**
 * One person's way so far, in their color. Each stretch of one mode and one
 * brightness is a single line: drawn fix by fix, the overlapping ends of the
 * pieces would add up into beads.
 */
function light(
  ctx: CanvasRenderingContext2D,
  person: Person,
  t: number,
  at: (p: { lat: number; lon: number }) => { x: number; y: number },
) {
  const position = headAt(person.day, t);
  type Run = { mode: Mode; level: number; points: { x: number; y: number }[]; offset: number };
  const runs: Run[] = [];
  let along = 0; // how far along one mode, so a dotted walk does not restart its dots
  for (const s of person.day.steps) {
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
  const tone = toneOf(person);
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
    ctx.strokeStyle = `rgba(${tone}, ${r.level * style.alpha * 0.11})`;
    ctx.lineWidth = style.width * 3.2;
    ctx.stroke();
    ctx.strokeStyle = `rgba(${tone}, ${r.level * style.alpha})`;
    ctx.lineWidth = style.width;
    ctx.stroke();
  }
}

/**
 * The circle of a place being made a region: its true size at this zoom, a
 * soft fill, a dashed edge until it is made (then whole), and beside it what
 * it is to be called and how wide it is - on the right, or on the left where
 * the right runs off the map.
 */
function circle(
  ctx: CanvasRenderingContext2D,
  spot: Spot,
  at: (p: { lat: number; lon: number }) => { x: number; y: number },
  w: number,
  font: string,
) {
  const c = at(spot);
  const r = Math.max(4, Math.abs(at({ lat: spot.lat + spot.radius / 111_320, lon: spot.lon }).y - c.y));
  const tone = TONE[spot.tone];
  ctx.beginPath();
  ctx.arc(c.x, c.y, r, 0, Math.PI * 2);
  ctx.fillStyle = `rgba(${tone}, ${spot.made ? 0.16 : 0.09})`;
  ctx.fill();
  ctx.setLineDash(spot.made ? [] : [5, 5]);
  ctx.lineWidth = 1.6;
  ctx.strokeStyle = `rgba(${tone}, 0.92)`;
  ctx.stroke();
  ctx.setLineDash([]);

  const title = spot.name || "이름을 정해 주세요";
  const size = `반경 ${spot.radius}m`;
  ctx.save();
  ctx.shadowColor = "#0c0d0f";
  ctx.shadowBlur = 6;
  ctx.textBaseline = "middle";
  ctx.font = `500 12.5px ${font}`;
  const titleWidth = ctx.measureText(title).width;
  ctx.font = `400 11px ${font}`;
  const width = Math.max(titleWidth, ctx.measureText(size).width);
  const left = c.x + r + 10 + width > w - 4;
  ctx.textAlign = left ? "right" : "left";
  const x = left ? c.x - r - 10 : c.x + r + 10;
  ctx.font = `500 12.5px ${font}`;
  ctx.fillStyle = spot.name ? "rgba(241, 241, 243, 0.94)" : "rgba(241, 241, 243, 0.6)";
  ctx.fillText(title, x, c.y - 8);
  ctx.font = `400 11px ${font}`;
  ctx.fillStyle = "rgba(166, 167, 172, 0.95)";
  ctx.fillText(size, x, c.y + 8);
  ctx.restore();
}

/** Each person's own face on the map, as they chose it: the operator's grey cat, the guest's cream one. */
const FACE: Record<Who, string> = { owner: "/icons/location-cat.png", guest: "/icons/location-cat-guest.png" };

/**
 * A person's mark: their cat's face over a soft glow in their color, which
 * breathes when it is now. The glow keeps the face's dark outline from
 * sinking into the dark map.
 */
function mark(who: Who): HTMLDivElement {
  const el = document.createElement("div");
  el.className = "pointer-events-none absolute top-0 left-0 size-[42px]";
  const halo = document.createElement("span");
  halo.className = "absolute inset-0 rounded-full";
  const face = document.createElement("img");
  face.src = FACE[who];
  face.alt = "";
  face.draggable = false;
  face.className = "absolute top-1/2 left-1/2 size-[29px] -translate-1/2 select-none";
  el.replaceChildren(halo, face);
  return el;
}

type Place = { key: string; node: Node; name?: string; stays: Stay[] };

/** Stays of one name within this are one place; farther apart, two - each phone's own 집. */
const ONE_PLACE = 300;

/** One place: of one name and near, or nameless at the same spot. */
function samePlace(a: Place, b: Place): boolean {
  return a.name ? a.name === b.name && meters(a.node, b.node) <= ONE_PLACE : !b.name && a.key === b.key;
}

const placesCache = new WeakMap<Day, Place[]>();

/**
 * A day's stays by place - by name where they are near, or for a nameless
 * place by where it is - in the order their names give way: named before
 * nameless, then the longest stayed.
 */
function placesOf(day: Day): Place[] {
  const cached = placesCache.get(day);
  if (cached) return cached;
  const found: Place[] = [];
  for (const it of day.items) {
    if (it.type !== "stay") continue;
    const at = `${it.node.lat.toFixed(3)},${it.node.lon.toFixed(3)}`;
    const here: Place = { key: it.node.name ?? at, node: it.node, name: it.node.name, stays: [] };
    let place = found.find((p) => samePlace(p, here));
    if (!place) {
      if (found.some((p) => p.key === here.key)) here.key = `${here.key}@${at}`;
      found.push((place = here));
    }
    place.stays.push(it);
  }
  const stayed = (p: Place) => p.stays.reduce((s, x) => s + (x.end - x.start), 0);
  const out = found.sort((a, b) => Number(!a.name) - Number(!b.name) || stayed(b) - stayed(a));
  placesCache.set(day, out);
  return out;
}

/**
 * The places' names, from the moment each is first reached. With one person,
 * each with its hours so far; with both, the place alone, once however many
 * of them were there. A name sits to the right of its ring (clear of a mark,
 * where one is), or to the left where the right runs off the map or into
 * another name; where neither side is free it waits until the map is zoomed
 * in far enough.
 */
function name(
  l: Layer,
  scene: Scene,
  t: number,
  at: (p: Node) => { x: number; y: number },
  w: number,
  marks: { x: number; y: number }[],
) {
  const single = scene.people.length === 1;
  // Both at one place: one name. One name for places apart: one each.
  const places = new Map<string, { place: Place; day: Day; seen: Stay[] }>();
  for (const person of scene.people) {
    for (const place of placesOf(person.day)) {
      const seen = place.stays.filter((s) => s.start <= t);
      const had = [...places].find(([, o]) => samePlace(o.place, place));
      if (!had) places.set(places.has(place.key) ? `${person.who}:${place.key}` : place.key, { place, day: person.day, seen });
      else if (!had[1].seen.length && seen.length) places.set(had[0], { place, day: person.day, seen });
    }
  }
  for (const [key, n] of l.names) {
    if (places.has(key)) continue;
    n.el.remove();
    l.names.delete(key);
  }
  const taken: { x: number; y: number; w: number; h: number }[] = [];
  for (const [key, { place, day, seen }] of places) {
    let n = l.names.get(key);
    if (!n) {
      const el = document.createElement("div");
      el.className = "pointer-events-none absolute top-0 left-0 flex flex-col font-sans leading-[1.25] whitespace-nowrap [text-shadow:0_0_6px_#0c0d0f,0_0_2px_#0c0d0f]";
      l.root.insertBefore(el, l.marks.values().next().value ?? null);
      n = { el, text: "", w: 0, h: 0 };
      l.names.set(key, n);
    }
    const hours = single ? seen.map((s) => range(day, s, t)).filter(Boolean).join(" · ") : "";
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
    // Where a mark is, the name stands clear of it.
    const gap = marks.some((q) => Math.hypot(q.x - p.x, q.y - p.y) < 28) ? (marks.length > 1 ? 34 : 21) : 12;
    const free = (x: number) =>
      x >= 4 &&
      x + n.w <= w - 4 &&
      !taken.some((b) => x < b.x + b.w + 6 && x + n.w + 6 > b.x && y < b.y + b.h && y + n.h > b.y);
    const right = p.x + gap;
    const left = p.x - gap - n.w;
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

/** Room around the day: more where the map's own controls sit (the top). */
function padding(el: HTMLElement) {
  const p = Math.round(Math.min(el.clientWidth, el.clientHeight) * 0.1);
  return { top: Math.max(p, 64), bottom: Math.max(p, 28), left: Math.max(p, 28), right: Math.max(p, 60) };
}

const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;
