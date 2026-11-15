"use client";

import { useEffect, useState } from "react";
import { ensurePush } from "@/lib/use-push";

/**
 * Wall-clock now, re-read every `ms`.
 *
 * Timers here are differences between timestamps, never counters. A counter
 * that adds one per tick stops when the phone locks between sets and comes
 * back minutes behind; a timestamp cannot drift.
 */
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = () => setNow(Date.now());
    const id = setInterval(tick, ms);
    // Back from the lock screen: show the true time at once, not on the next tick.
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [ms]);
  return now;
}

// The screen kept on through a session: unlocking the phone with chalky hands
// between every set is the friction the apps that get used remove first.
export { useWakeLock } from "@/lib/use-wake-lock";

let audio: AudioContext | null = null;

/**
 * Unlock sound for later. iOS only lets a page make a sound that started from
 * a tap, so this is called on the tap that starts a rest - the chime at the
 * end of it then has a running context to play on.
 */
export function primeChime() {
  try {
    if (audio?.state === "closed") audio = null;
    audio ??= new AudioContext();
    // Suspended, or Safari's "interrupted": what a phone leaves of a page's
    // sound after the page has been in the background.
    if (audio.state !== "running") void audio.resume().catch(() => {});
  } catch {
    audio = null;
  }
}

/**
 * The chime kept ready through a session. Back from the music app or the
 * lock screen, the sound iOS took away is asked back at once - and again at
 * the next touch anywhere, for a phone that only gives it back to a tap. It
 * had stayed silent until the next set was ticked, rests ending without it.
 */
export function useChimeReady(active: boolean) {
  useEffect(() => {
    if (!active) return;
    const back = () => {
      if (document.visibilityState === "visible" && audio && audio.state !== "running") {
        void audio.resume().catch(() => {});
      }
    };
    const touches = ["touchend", "click", "keydown"] as const;
    document.addEventListener("visibilitychange", back);
    for (const t of touches) document.addEventListener(t, primeChime, { capture: true, passive: true });
    return () => {
      document.removeEventListener("visibilitychange", back);
      for (const t of touches) document.removeEventListener(t, primeChime, { capture: true });
    };
  }, [active]);
}

/** Two short tones and a buzz: rest is over. Silent if sound was never unlocked. */
export function chime() {
  navigator.vibrate?.([120, 60, 120]);
  if (!audio || audio.state !== "running") return;
  const t = audio.currentTime;
  for (const [offset, freq] of [
    [0, 880],
    [0.18, 1175],
  ] as const) {
    const osc = audio.createOscillator();
    const gain = audio.createGain();
    osc.frequency.value = freq;
    gain.gain.setValueAtTime(0.0001, t + offset);
    gain.gain.exponentialRampToValueAtTime(0.25, t + offset + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, t + offset + 0.16);
    osc.connect(gain).connect(audio.destination);
    osc.start(t + offset);
    osc.stop(t + offset + 0.18);
  }
}

/** localStorage that never throws: private mode and blocked storage just forget. */
export const local = {
  get<T>(key: string): T | null {
    try {
      const raw = localStorage.getItem(key);
      return raw ? (JSON.parse(raw) as T) : null;
    } catch {
      return null;
    }
  },
  set(key: string, value: unknown) {
    try {
      if (value === null) localStorage.removeItem(key);
      else localStorage.setItem(key, JSON.stringify(value));
    } catch {
      // Nothing to do: the value lives for this page only.
    }
  },
};

/**
 * The end of a rest, sent to this phone as a push as well (output-svc
 * POST /timers). The chime is heard only from a page on screen and off mute;
 * between sets the phone is locked, in a pocket or showing the music app, and
 * a push buzzes it even on mute. The push follows the rest: asked for when
 * one starts or is nudged, taken back when it ends early or the session
 * does. A device without push has the chime alone, and a call that fails is
 * let go without a word - the screen still says when the rest is over.
 */
const REST_PUSH = "jarvis.workout.rest-push";

/** The push asked for, and when it goes: kept, so a reload can still take it back. */
type RestPush = { id: string; at: number };

// One call at a time, in order, each for the latest end asked: a nudge
// pressed three times is one push at its last end, and a rest ended while
// its push was being asked for takes that push back once it has an id. Each
// call outlives the page (keepalive): the phone is locked right after the tick.
let wanted: number | null = null;
let line: Promise<void> = Promise.resolve();

/** The rest now ends at `at` (ms), or there is none. Never waits, never throws. */
export function restPush(at: number | null) {
  wanted = at;
  line = line.then(settleRestPush).catch(() => {});
}

async function settleRestPush() {
  const at = wanted;
  const held = local.get<RestPush>(REST_PUSH);
  const now = Date.now();
  // Over, or all but: the screen says so, and a push would only say it again.
  if (at === null || at - now < 1000) {
    if (!held) return;
    local.set(REST_PUSH, null);
    // One that already went has nothing left to take back.
    if (held.at > now) await fetch(`/api/notify/timers/${encodeURIComponent(held.id)}`, { method: "DELETE", keepalive: true });
    return;
  }
  if (held?.at === at) return;
  const endpoint = await ensurePush();
  if (!endpoint) return;
  // The same device and tag again replaces the one asked before.
  const res = await fetch("/api/notify/timers", {
    method: "POST",
    keepalive: true,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      endpoint,
      at,
      tag: "rest",
      title: "휴식이 끝났어요",
      body: "다음 세트를 할 시간이에요.",
      url: "/workout",
    }),
  });
  if (!res.ok) return;
  const { id } = (await res.json()) as { id?: string };
  if (id) local.set(REST_PUSH, { id, at });
}
