"use client";

import { useEffect, useRef, useState } from "react";
import { capPct, pct, resetLabel, tone, usd, useUsage } from "@/lib/use-usage";
import { cn } from "@/lib/utils";

/**
 * Three hairlines in the header: the Claude subscription's 5-hour window on
 * top, the weekly one below it, and the memory API key's spend against its
 * daily cap at the bottom. Always there, never asking for attention until
 * they fill. Holding them (or hovering, on a pointer) shows the numbers.
 */
export function UsageBars() {
  const usage = useUsage();
  const [open, setOpen] = useState(false);
  const hold = useRef<number | null>(null);
  const wrap = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open]);

  const five = pct(usage?.fiveHour);
  const week = pct(usage?.sevenDay);
  const api = capPct(usage?.api);
  const label =
    five == null && week == null
      ? "Claude 사용량을 아직 읽지 못했습니다"
      : `Claude 사용량: 5시간 ${five ?? "-"}%, 주간 ${week ?? "-"}%` +
        (api == null ? "" : `, 기억용 API 하루 상한의 ${api}%`);

  return (
    <div ref={wrap} className="relative">
      <button
        type="button"
        aria-label={label}
        aria-expanded={open}
        onPointerDown={(e) => {
          if (e.pointerType === "mouse") return;
          hold.current = window.setTimeout(() => setOpen(true), 350);
        }}
        onPointerUp={() => hold.current && window.clearTimeout(hold.current)}
        onPointerLeave={(e) => {
          if (hold.current) window.clearTimeout(hold.current);
          if (e.pointerType === "mouse") setOpen(false);
        }}
        onPointerEnter={(e) => e.pointerType === "mouse" && setOpen(true)}
        onClick={() => setOpen((v) => !v)}
        onContextMenu={(e) => e.preventDefault()}
        className="flex h-11 w-12 flex-col justify-center gap-[3px] rounded-lg px-1.5 outline-none select-none [-webkit-touch-callout:none] focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-8"
      >
        <Bar value={five} />
        <Bar value={week} />
        {usage?.api ? <Bar value={api} /> : null}
      </button>

      {open ? (
        <div
          role="tooltip"
          className="absolute top-full right-0 z-50 mt-1 w-56 rounded-xl border border-edge bg-background/95 p-3 text-[12px] shadow-lg backdrop-blur-md"
        >
          <p className="mb-2 text-[11.5px] text-faint">Claude 구독 사용량</p>
          <Line name="5시간" value={five} resets={usage?.fiveHour?.resetsAt} />
          <Line name="주간" value={week} resets={usage?.sevenDay?.resetsAt} />
          {usage?.api ? (
            <div className="mt-2 border-t border-edge pt-2">
              <p className="mb-1 text-[11.5px] text-faint">기억용 API (Haiku 5.5)</p>
              <div className="flex items-baseline justify-between py-1">
                <span className="text-dim">오늘</span>
                <span className="tnum text-[13px] text-foreground">
                  {usd(usage.api.todayUsd)} <span className="text-faint">/ {usd(usage.api.dailyUsd)}</span>
                </span>
              </div>
              <span aria-hidden className="block h-1 overflow-hidden rounded-full bg-foreground/12">
                <span className={cn("block h-full rounded-full", tone(capPct(usage.api)))} style={{ width: `${capPct(usage.api) ?? 0}%` }} />
              </span>
              <p className="tnum mt-1 text-[10.5px] text-faint">이번 달 {usd(usage.api.month.usd)}</p>
            </div>
          ) : null}
          {usage?.at ? (
            <p className="mt-2 text-[10.5px] text-faint">
              {new Date(usage.at).toLocaleTimeString("ko-KR", { timeZone: "Asia/Seoul", hour: "2-digit", minute: "2-digit", hour12: false })}{" "}
              기준
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function Bar({ value }: { value: number | null }) {
  return (
    <span aria-hidden className="block h-[3px] w-full overflow-hidden rounded-full bg-foreground/12">
      <span
        className={cn("block h-full rounded-full transition-[width] duration-500", tone(value))}
        style={{ width: `${Math.min(100, Math.max(value ?? 0, value ? 3 : 0))}%` }}
      />
    </span>
  );
}

function Line({ name, value, resets }: { name: string; value: number | null; resets?: string }) {
  return (
    <div className="py-1">
      <div className="flex items-baseline justify-between">
        <span className="text-dim">{name}</span>
        <span className="tnum text-[13px] text-foreground">{value == null ? "-" : `${value}% 사용`}</span>
      </div>
      <span aria-hidden className="mt-1 block h-1 overflow-hidden rounded-full bg-foreground/12">
        <span className={cn("block h-full rounded-full", tone(value))} style={{ width: `${value ?? 0}%` }} />
      </span>
      {resets ? <p className="tnum mt-1 text-[10.5px] text-faint">{resetLabel(resets)} 초기화</p> : null}
    </div>
  );
}
