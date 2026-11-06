"use client";

import type { ReactNode } from "react";
import { SAY } from "@/lib/say";
import { cn } from "@/lib/utils";

/** "14:05", or "14:05:09" with seconds, in Seoul. */
export function clock(at: Date | string, seconds = false): string {
  return new Date(at).toLocaleTimeString("ko-KR", {
    timeZone: "Asia/Seoul",
    hour: "2-digit",
    minute: "2-digit",
    ...(seconds ? { second: "2-digit" as const } : {}),
    hour12: false,
  });
}

/**
 * When what a screen shows was read - and, once a read has failed, that what
 * stands is that earlier read, with the way to read again. A failed read keeps
 * what was there and says so: "14:05 값 · 다시 시도". It had read as current -
 * one month's figures under the next month's name, a run "going on" that may
 * have ended, "없음" for what could not be read (2026-11-06 review).
 */
export function AsOf({
  at,
  failed,
  failedText,
  busy,
  seconds,
  onRetry,
  children,
  className,
}: {
  /** When what is shown was read; null before anything was. */
  at: Date | string | null;
  /** The last read failed: what is shown is the read before it. */
  failed?: boolean;
  /** What failed, said instead of "14:05 값" (a month asked for and not read). */
  failedText?: string;
  busy?: boolean;
  seconds?: boolean;
  onRetry?: () => void;
  /** More of the line, after the time: "· 환율 1,400원/$". */
  children?: ReactNode;
  className?: string;
}) {
  if (failed) {
    return (
      <p role="status" className={cn("tnum text-[11.5px] text-reject/85", className)}>
        {failedText ?? (at ? `${clock(at, seconds)} 값` : "불러오지 못했어요")}
        {onRetry ? (
          <>
            {" · "}
            <button
              type="button"
              onClick={onRetry}
              disabled={busy}
              className="tap-y underline decoration-reject/40 underline-offset-[3px] outline-none hover:text-reject focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-60"
            >
              {busy ? SAY.loading : SAY.retry}
            </button>
          </>
        ) : null}
      </p>
    );
  }
  if (!at) return null;
  return (
    <p className={cn("tnum text-[11.5px] text-faint", className)} aria-live="polite">
      {clock(at, seconds)} 기준{children}
      {busy ? " · 갱신 중…" : ""}
    </p>
  );
}
