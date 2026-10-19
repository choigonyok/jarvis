"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { HeaderPanel } from "@/components/shell/panel";
import {
  agoLabel,
  ageMinutes,
  capPct,
  freeLabel,
  freePct,
  pct,
  PURPOSE,
  resetLabel,
  STALE_MINUTES,
  tone,
  usd,
  useUsage,
} from "@/lib/use-usage";
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
 *
 * The subscription's lines are the whole account as last read, and use from
 * claude.ai, other devices or the guest agent only shows at the next reading:
 * an old reading is drawn faint, and a window that started over since reads
 * "측정 전" rather than 0%.
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
  const age = ageMinutes(usage?.at);
  const stale = age != null && age >= STALE_MINUTES;
  const api = capPct(usage?.api);
  const voyage = usage?.api?.voyage;
  const free = freePct(voyage);
  const mind = usage?.mind;
  const mindPct = capPct(mind);
  const label =
    five == null && week == null
      ? "Claude 사용량을 아직 읽지 못했습니다"
      : `Claude 사용량${age != null ? `(${agoLabel(age)} 측정)` : ""}: 5시간 ${five ?? "-"}%, 주간 ${week ?? "-"}%` +
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
        className="flex h-11 w-12 flex-col justify-center gap-[3px] rounded-lg px-1.5 outline-none select-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-8"
      >
        <Bar value={five} faint={stale || five == null} />
        <Bar value={week} faint={stale || week == null} />
        {usage?.api ? <Bar value={api} /> : null}
        {voyage ? <Bar value={free} /> : null}
        {mind ? <Bar value={mindPct} /> : null}
      </button>

      {open ? (
        <HeaderPanel
          title="사용량"
          aside={at ? <span className="tnum text-[11.5px] text-faint">{at} 기준{age != null ? ` · ${agoLabel(age)}` : ""}</span> : null}
        >
          <div className="max-h-[min(30rem,72vh)] space-y-4 overflow-y-auto overscroll-contain px-3.5 py-3">
            <Group name="Claude 구독">
              <Meter
                name="5시간"
                figure={windowFigure(five, usage?.fiveHour?.reset)}
                value={five}
                faint={stale}
                note={windowNote(usage?.fiveHour)}
              />
              <Meter
                name="주간"
                figure={windowFigure(week, usage?.sevenDay?.reset)}
                value={week}
                faint={stale}
                note={windowNote(usage?.sevenDay)}
              />
              {stale ? (
                <p className="text-[11px] leading-snug text-faint">
                  {agoLabel(age!)} 값이에요. 그 뒤에 claude.ai나 다른 기기에서 쓴 양은 다음 대화나 작업 때(쉬는 동안엔 한 시간마다)
                  반영돼요.
                </p>
              ) : null}
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

/** "37%", or "측정 전" for a window that started over since it was read. */
function windowFigure(p: number | null, reset?: boolean): string {
  if (reset) return "측정 전";
  return p == null ? "-" : `${p}%`;
}

function windowNote(w?: { resetsAt: string; reset?: boolean }): string | undefined {
  if (!w?.resetsAt) return undefined;
  if (w.reset) return "초기화됐어요. 다음 대화나 작업 때 다시 읽어요.";
  return `${resetLabel(w.resetsAt)} 초기화`;
}

function Bar({ value, faint }: { value: number | null; faint?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn("block h-[3px] w-full overflow-hidden rounded-full bg-foreground/12 transition-opacity", faint && "opacity-45")}
    >
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
  faint,
  children,
}: {
  name: string;
  /** Which model or service, quieter than the name. */
  detail?: string;
  figure: ReactNode;
  value: number | null;
  note?: string;
  /** An old reading: drawn quieter than a fresh one. */
  faint?: boolean;
  children?: ReactNode;
}) {
  return (
    <div className={cn("transition-opacity", faint && "opacity-60")}>
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
