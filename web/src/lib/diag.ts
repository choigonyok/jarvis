/**
 * Diagnostics, off unless asked for: localStorage "jarvis.diag" = "1" on the
 * phone being looked at. What the voice stage and the push setup do on it, in
 * names and numbers (no words), posted to /api/diag, which writes it to the
 * Worker's log (wrangler tail jarvis). The chat screen's own sampling, which
 * read the layout ten times a second while an answer was written on every
 * phone, is gone.
 */
const BUILD = process.env.JARVIS_BUILD ?? "";

let on: boolean | undefined;

function enabled(): boolean {
  if (on === undefined) {
    try {
      on = typeof window !== "undefined" && window.localStorage.getItem("jarvis.diag") === "1";
    } catch {
      on = false;
    }
  }
  return on;
}

export function diag(kind: string, data: Record<string, unknown>) {
  if (!enabled()) return;
  try {
    const body = JSON.stringify({ kind, build: BUILD, now: Date.now(), ...data });
    const blob = new Blob([body], { type: "application/json" });
    if (!navigator.sendBeacon?.("/api/diag", blob)) void fetch("/api/diag", { method: "POST", body, keepalive: true });
  } catch {
    // Diagnostics never get in the way.
  }
}
