/**
 * Temporary (2026-10-27): what the chat screen does on the phone itself -
 * which build is running, when the answer's pieces arrive and how they come
 * out, where the view is after a send. Posted to /api/diag, which writes it
 * to the Worker's log (wrangler tail jarvis). Numbers only, no words. To be
 * taken out once the phone's behaviour is understood.
 */
const BUILD = process.env.JARVIS_BUILD ?? "";

export function diag(kind: string, data: Record<string, unknown>) {
  try {
    const body = JSON.stringify({ kind, build: BUILD, now: Date.now(), ...data });
    const blob = new Blob([body], { type: "application/json" });
    if (!navigator.sendBeacon?.("/api/diag", blob)) void fetch("/api/diag", { method: "POST", body, keepalive: true });
  } catch {
    // Diagnostics never get in the way.
  }
}

/** The scroller's numbers, and the page's around it. */
export function viewOf(el: HTMLElement | null) {
  if (!el) return null;
  const vv = window.visualViewport;
  return {
    top: Math.round(el.scrollTop),
    h: el.scrollHeight,
    ch: el.clientHeight,
    gap: Math.round(el.scrollHeight - el.scrollTop - el.clientHeight),
    doc: Math.round(document.scrollingElement?.scrollTop ?? 0),
    vvh: Math.round(vv?.height ?? 0),
    vvt: Math.round(vv?.offsetTop ?? 0),
    ih: window.innerHeight,
  };
}

// The answer's frames as they arrive: [ms since its first, at, length].
let frames: number[][] = [];
let first = 0;
let firstAt = 0;

export function diagFrame(at: number, length: number) {
  const now = performance.now();
  if (at === 0 || !first) {
    first = now;
    firstAt = Date.now();
    frames = [];
  }
  if (frames.length < 400) frames.push([Math.round(now - first), at, length]);
}

export function takeFrames() {
  const out = { firstAt, frames };
  frames = [];
  first = 0;
  return out;
}

// Scroll events for a few seconds after a send: [ms, top, gap, rose, pinned].
let scrollsUntil = 0;
let scrollsFrom = 0;
let scrolls: number[][] = [];

export function watchScrolls(ms: number) {
  scrollsFrom = performance.now();
  scrollsUntil = scrollsFrom + ms;
  scrolls = [];
}

export function diagScroll(top: number, gap: number, rose: boolean, pinned: boolean) {
  const now = performance.now();
  if (now > scrollsUntil || scrolls.length >= 80) return;
  scrolls.push([Math.round(now - scrollsFrom), Math.round(top), Math.round(gap), rose ? 1 : 0, pinned ? 1 : 0]);
}

export function takeScrolls() {
  const out = scrolls;
  scrolls = [];
  return out;
}
