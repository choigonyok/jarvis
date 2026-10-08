"use client";

import { useEffect, useState } from "react";
import { API } from "@/lib/thread";

/** The Claude subscription's use, as the agent last read it (agent/internal/usage). */
export type UsageWindow = { utilization: number; resetsAt: string };
/** The memory model's pay-as-you-go API spend (memory-svc), not the subscription. */
export type ApiUsage = {
  model: string;
  enabled: boolean;
  dailyUsd: number;
  todayUsd: number;
  month: { usd: number; calls: number };
  days: { day: string; usd: number; calls: number; inputTokens: number; outputTokens: number }[];
};
export type Usage = {
  fiveHour?: UsageWindow;
  sevenDay?: UsageWindow;
  status?: string;
  at?: string;
  api?: ApiUsage;
};

/** "$0.0123" under a dollar, "$1.23" above. */
export const usd = (v: number) => `$${v < 1 ? v.toFixed(v < 0.01 ? 4 : 3) : v.toFixed(2)}`;

/** Today's spend against the daily cap, as a percent. */
export const capPct = (a?: ApiUsage) =>
  a && a.dailyUsd > 0 ? Math.min(100, Math.round((a.todayUsd / a.dailyUsd) * 100)) : null;

// One fetch shared by every component on the page (the header bars and the
// 상태 tab both read it), refreshed once a minute.
let cache: Usage | null = null;
let inflight: Promise<void> | null = null;
let last = 0;
const listeners = new Set<(u: Usage) => void>();

async function refresh() {
  if (inflight) return inflight;
  inflight = (async () => {
    try {
      const res = await fetch(`${API}/usage`, { cache: "no-store" });
      if (res.ok) {
        cache = (await res.json()) as Usage;
        last = Date.now();
        listeners.forEach((fn) => fn(cache!));
      }
    } catch {
      // The bars just keep their last value.
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}

export function useUsage(enabled = true): Usage | null {
  const [usage, setUsage] = useState<Usage | null>(cache);
  useEffect(() => {
    if (!enabled) return;
    listeners.add(setUsage);
    if (Date.now() - last > 30_000) void refresh();
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") void refresh();
    }, 60_000);
    return () => {
      listeners.delete(setUsage);
      window.clearInterval(id);
    };
  }, [enabled]);
  return enabled ? usage : null;
}

/** "2시간 10분 뒤", "10/14 (화) 09:00". */
export function resetLabel(iso: string, now = Date.now()): string {
  const t = new Date(iso).getTime();
  const min = Math.max(0, Math.round((t - now) / 60000));
  if (min < 60) return `${min}분 뒤`;
  if (min < 24 * 60) {
    const h = Math.floor(min / 60);
    const m = min % 60;
    return m ? `${h}시간 ${m}분 뒤` : `${h}시간 뒤`;
  }
  const d = new Date(iso);
  const day = d.toLocaleDateString("ko-KR", { timeZone: "Asia/Seoul", month: "numeric", day: "numeric", weekday: "short" });
  const time = d.toLocaleTimeString("ko-KR", { timeZone: "Asia/Seoul", hour: "2-digit", minute: "2-digit", hour12: false });
  return `${day} ${time}`;
}

export const pct = (w?: UsageWindow) => (w ? Math.round(w.utilization * 100) : null);

/** Calm until it matters: the bar warms at 70% and turns red at 90%. */
export function tone(p: number | null): string {
  if (p == null) return "bg-faint/40";
  if (p >= 90) return "bg-reject";
  if (p >= 70) return "bg-[oklch(0.8_0.14_75)]";
  return "bg-foreground/60";
}
