"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { HeaderPanel } from "@/components/shell/panel";
import { capPct, freeLabel, freePct, pct, PURPOSE, resetLabel, tone, usd, useUsage } from "@/lib/use-usage";
import { cn } from "@/lib/utils";

/**
 * Hairlines in the header: the Claude subscription's 5-hour window on top, the
 * weekly one below it, then the API keys that cost money by use - memory's
 * against its daily cap, the search key (Voyage) into its free tokens, mind's
 * against its daily cap. Always there, never asking for attention until they
 * fill. A tap shows the numbers.
 *
 * Every line in the panel is the same meter: a name and its figure, the bar,
 * and one line under it that says when it resets or what the month came to.
 * Two groups - what the subscription allows, what the keys cost - so the eye
 * learns one shape and reads five of them.
 */
export function UsageBars() {
  const usage = useUsage();
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);

  const five = pct(usage?.fiveHour);
  const week = pct(usage?.sevenDay);
  const api = capPct(usage?.api);
  const voyage = usage?.api?.voyage;
  const free = freePct(voyage);
  const mind = usage?.mind;
  const mindPct = capPct(mind);
  const label =
    five == null && week == null
      ? "Claude 사용량을 아직 읽지 못했습니다"
      : `Claude 사용량: 5시간 ${five ?? "-"}%, 주간 ${week ?? "-"}%` +
        (api == null ? "" : `, 기억용 API 하루 상한의 ${api}%`) +
        (voyage ? `, 검색용 API 오늘 ${usd(voyage.todayUsd)}` : "") +
        (mindPct == null ? "" : `, 판단용 API 하루 상한의 ${mindPct}%`);
  const paid = Boolean(usage?.api || voyage || mind);
  const at = usage?.at
    ? new Date(usage.at).toLocaleTimeString("ko-KR", { timeZone: "Asia/Seoul", hour: "2-digit", minute: "2-digit", hour12: false })
    : null;

  return (
    <div ref={wrap} className="relative">
      <button
        type="button"
        aria-label={label}
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex h-11 w-12 flex-col justify-center gap-[3px] rounded-lg px-1.5 outline-none select-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-8"
      >
        <Bar value={five} />
        <Bar value={week} />
        {usage?.api ? <Bar value={api} /> : null}
        {voyage ? <Bar value={free} /> : null}
        {mind ? <Bar value={mindPct} /> : null}
      </button>

      {open ? (
        <HeaderPanel title="사용량" aside={at ? <span className="tnum text-[11.5px] text-faint">{at} 기준</span> : null}>
          <div className="max-h-[min(30rem,72vh)] space-y-4 overflow-y-auto overscroll-contain px-3.5 py-3">
            <Group name="Claude 구독">
              <Meter
                name="5시간"
                figure={five == null ? "-" : `${five}%`}
                value={five}
                note={usage?.fiveHour?.resetsAt ? `${resetLabel(usage.fiveHour.resetsAt)} 초기화` : undefined}
              />
              <Meter
                name="주간"
                figure={week == null ? "-" : `${week}%`}
                value={week}
                note={usage?.sevenDay?.resetsAt ? `${resetLabel(usage.sevenDay.resetsAt)} 초기화` : undefined}
              />
            </Group>

            {paid ? (
              <Group name="API 사용료 (오늘)">
                {usage?.api ? (
                  <Meter
                    name="기억"
                    detail={usage.api.model}
                    figure={
                      <>
                        {usd(usage.api.todayUsd)} <span className="text-faint">/ {usd(usage.api.dailyUsd)}</span>
                      </>
                    }
                    value={api}
                    note={`이번 달 ${usd(usage.api.month.usd)}`}
                  />
                ) : null}
                {voyage ? (
                  <Meter
                    name="검색"
                    detail="Voyage"
                    figure={usd(voyage.todayUsd)}
                    value={free}
                    note={`무료 토큰 ${freeLabel(voyage)} 사용 · 이번 달 ${usd(voyage.month.usd)}, 청구 ${usd(voyage.billedUsd)}`}
                  />
                ) : null}
                {mind ? (
                  <Meter
                    name="판단"
                    detail="mind"
                    figure={
                      <>
                        {usd(mind.todayUsd)} <span className="text-faint">/ {usd(mind.dailyUsd)}</span>
                      </>
                    }
                    value={mindPct}
                    note={`이번 달 ${usd(mind.month.usd)} · ${mind.month.calls}번`}
                  >
                    {mind.purposes.length ? (
                      <ul className="mt-1 space-y-0.5">
                        {mind.purposes.map((p) => (
                          <li key={p.purpose} className="tnum flex justify-between text-[11px] text-faint">
                            <span>{PURPOSE[p.purpose] ?? p.purpose}</span>
                            <span>
                              {p.calls}번 · {usd(p.usd)}
                            </span>
                          </li>
                        ))}
                      </ul>
                    ) : null}
                  </Meter>
                ) : null}
              </Group>
            ) : null}
          </div>
        </HeaderPanel>
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

function Group({ name, children }: { name: string; children: ReactNode }) {
  return (
    <section>
      <p className="mb-1.5 text-[11.5px] text-faint">{name}</p>
      <div className="space-y-3">{children}</div>
    </section>
  );
}

/** One line of the panel: name and figure, the bar, and what to know about it. */
function Meter({
  name,
  detail,
  figure,
  value,
  note,
  children,
}: {
  name: string;
  /** Which model or service, quieter than the name. */
  detail?: string;
  figure: ReactNode;
  value: number | null;
  note?: string;
  children?: ReactNode;
}) {
  return (
    <div>
      <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
        <span className="text-dim">
          {name}
          {detail ? <span className="ms-1.5 text-[11px] text-faint">{detail}</span> : null}
        </span>
        <span className="tnum text-[13px] text-foreground">{figure}</span>
      </div>
      <span aria-hidden className="mt-1.5 block h-1 overflow-hidden rounded-full bg-foreground/12">
        <span className={cn("block h-full rounded-full", tone(value))} style={{ width: `${Math.min(100, value ?? 0)}%` }} />
      </span>
      {note ? <p className="tnum mt-1 text-[11px] leading-snug text-faint">{note}</p> : null}
      {children}
    </div>
  );
}
