"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { Curve } from "@/components/assets/curve";
import { PrincipalLedger, PrincipalParts } from "@/components/assets/principal";
import { TargetAllocation } from "@/components/assets/target";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import {
  WINDOW_LABEL,
  krw,
  percent,
  profitOf,
  quantity,
  rateOf,
  signedKrw,
  type Portfolio,
  type Window,
} from "@/lib/portfolio";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

/**
 * What you own, and how far it has moved from what you paid.
 *
 * Read-only on purpose. The keys behind this can place orders; this surface
 * only ever asks what is there. Anything that spends money in this product
 * goes through the approval card, and a dashboard is not the place for that.
 */
export function Assets() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

  const [data, setData] = useState<Portfolio | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  // Whose curve the chart shows: a holding id, or null for the whole basket.
  const [subject, setSubject] = useState<string | null>(null);
  const chart = useRef<HTMLDivElement>(null);

  // The service answers at once with what it last had (marked stale) and
  // rebuilds behind it; ask again shortly to pick up the fresh numbers.
  const followUp = useRef<number | null>(null);
  const again = useRef<(tries: number) => void>(() => {});
  const load = useCallback(async (fresh = false, tries = 0) => {
    if (followUp.current) window.clearTimeout(followUp.current);
    setLoading(true);
    try {
      const res = await fetch(`/api/portfolio${fresh ? "?fresh=1" : ""}`, { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const next = (await res.json()) as Portfolio;
      setData(next);
      setError(null);
      if (next.stale && tries < 4) {
        followUp.current = window.setTimeout(() => again.current(tries + 1), 2500);
      }
    } catch {
      setError("잔고를 불러오지 못했습니다.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    again.current = (tries) => void load(false, tries);
  }, [load]);

  useEffect(() => {
    // Not a cascading render: load only sets state once the fetch it starts
    // has resolved. The lint rule cannot see past the async call.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
    return () => {
      if (followUp.current) window.clearTimeout(followUp.current);
    };
  }, [load]);

  // Measured against money put in when the ledger is readable; against what
  // the holdings cost only when it is not, and labelled so the two are never
  // mistaken for each other.
  const principal = data?.principal ?? null;
  const profit = principal ? principal.profitKrw : (data?.profitKrw ?? 0);
  const rate = principal ? principal.rate : (data?.returnRate ?? 0);
  const up = profit >= 0;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-5 pb-12 sm:px-8 sm:pt-7 sm:pb-6">
          <div className="mb-6 flex items-start justify-between gap-4">
            <div>
              <p className="text-[12px] text-faint">총자산</p>
              <p className="tnum mt-0.5 text-[26px] leading-none font-semibold tracking-tight text-foreground sm:text-[30px]">
                {data ? krw(data.totalKrw) : "—"}
              </p>
              {data ? (
                <p className="tnum mt-2 text-[13px]">
                  <span className={cn(up ? "text-approve/80" : "text-reject/80")}>
                    {signedKrw(profit)}
                  </span>
                  <span className="ms-2 text-dim">{percent(rate)}</span>
                  <span className="ms-2 text-faint">
                    {principal
                      ? `원금 ${krw(principal.principalKrw)} 대비`
                      : "평가손익 (원금 기록을 읽지 못함)"}
                  </span>
                </p>
              ) : null}
              {data ? (
                <p className="tnum mt-1.5 text-[11.5px] text-faint" aria-live="polite">
                  {new Date(data.at).toLocaleTimeString("ko-KR", {
                    timeZone: "Asia/Seoul",
                    hour: "2-digit",
                    minute: "2-digit",
                    second: "2-digit",
                    hour12: false,
                  })}{" "}
                  기준{data.stale || loading ? " · 갱신 중…" : ""}
                </p>
              ) : null}
            </div>

            <button
              type="button"
              // The button means "now": the service drops what it cached.
              onClick={() => void load(true)}
              disabled={loading}
              aria-label="잔고 새로 불러오기"
              className="tap flex shrink-0 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-40 sm:size-9 sm:min-h-0 sm:min-w-0"
            >
              <RefreshCw
                aria-hidden
                className={cn("size-4", loading && "anim-spin-slow")}
              />
            </button>
          </div>

          {principal ? (
            <div className="-mt-2 mb-6">
              <PrincipalParts parts={principal.parts} fixed={data?.fixed ?? []} />
            </div>
          ) : null}

          {/* Three windows on one row. Change over time is a different fact
              from profit against cost - this basket moved this much, whether
              or not you are up on what you paid - so it gets its own band
              rather than crowding into the headline. */}
          {data ? (
            <div className="mb-6 grid grid-cols-3 gap-px overflow-hidden rounded-xl border border-edge-soft bg-edge-soft">
              {(["day", "month", "year"] as Window[]).map((window) => {
                const change = data.changes[window];
                const gain = (change.rate ?? 0) >= 0;
                return (
                  <div key={window} className="bg-background px-3 py-2.5">
                    <p className="text-[11px] text-faint">{WINDOW_LABEL[window]}</p>
                    {change.rate === null ? (
                      <p className="mt-1 text-[14px] text-faint">—</p>
                    ) : (
                      <>
                        <p
                          className={cn(
                            "tnum mt-1 text-[15px] leading-none",
                            gain ? "text-approve/85" : "text-reject/85",
                          )}
                        >
                          {percent(change.rate, 2)}
                        </p>
                        <p className="tnum mt-1 text-[11px] text-faint">
                          {signedKrw(change.amountKrw ?? 0)}
                        </p>
                      </>
                    )}
                    {change.missing.length > 0 ? (
                      <p className="mt-1 text-[10.5px] leading-tight text-faint">
                        {change.missing.join(", ")} 제외
                      </p>
                    ) : null}
                  </div>
                );
              })}
            </div>
          ) : null}

          {error ? (
            <p
              role="status"
              className="border-l-2 border-reject pl-3 text-[13.5px] leading-relaxed text-dim"
            >
              {error} 키 설정을 확인하고 다시 시도해 주세요.
            </p>
          ) : null}

          {/* A venue that failed is named. A total quietly missing a holding
              would be wrong with nothing saying so. */}
          {data?.problems.length ? (
            <ul className="mb-5 space-y-1">
              {data.problems.map((problem) => (
                <li
                  key={problem}
                  className="border-l-2 border-reject/60 pl-3 text-[12.5px] leading-relaxed text-dim"
                >
                  {problem}
                </li>
              ))}
            </ul>
          ) : null}

          {data && data.holdings.length > 0 ? (
            <>
              {/* The whole basket by default; a holding picked here or in
                  the list below shows its own return curve instead - a
                  percentage, so a small position is as readable as a big one. */}
              <div ref={chart} className="scroll-mt-4">
                <Curve
                  series={data.series}
                  rates={{
                    day: data.changes.day.rate,
                    month: data.changes.month.rate,
                    year: data.changes.year.rate,
                  }}
                  holdings={data.holdings}
                  holdingSeries={data.holdingSeries}
                  subject={subject}
                  onSubject={setSubject}
                />
              </div>

              {/* An assets-svc older than this page sends no allocation. */}
              {data.allocation ? (
                <section className="mt-7 border-t border-edge-soft pt-5" aria-label="목표 비중">
                  <TargetAllocation allocation={data.allocation} holdings={data.holdings} goldGramKrw={data.goldGramKrw} />
                </section>
              ) : null}

              {principal ? (
                <section className="mt-7 border-t border-edge-soft pt-5" aria-label="원금 기록">
                  <PrincipalLedger principal={principal} onChange={() => void load()} />
                </section>
              ) : null}

              <section className="mt-7 border-t border-edge-soft pt-5" aria-label="보유 종목">
                <div className="space-y-0.5">
                  {data.holdings.map((h) => {
                    const profit = profitOf(h);
                    const gain = profit >= 0;
                    const charted = (data.holdingSeries?.[h.id]?.month?.length ?? 0) >= 2;
                    return (
                      <button
                        key={h.id}
                        type="button"
                        disabled={!charted}
                        onClick={() => {
                          setSubject(h.id);
                          chart.current?.scrollIntoView({ behavior: "smooth", block: "start" });
                        }}
                        aria-label={`${h.name} 수익률 그래프 보기`}
                        className={cn(
                          "flex w-full items-baseline gap-3 rounded-lg px-2 py-2.5 text-left transition-colors outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-default",
                          subject === h.id && "bg-glass",
                        )}
                      >
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-[14px] text-foreground/90">
                            {h.name}
                          </p>
                          <p className="tnum mt-0.5 text-[11.5px] text-faint">
                            {quantity(h.quantity)}
                            {h.kind === "stock" ? "주" : h.kind === "gold" ? "g" : ""} · 평단{" "}
                            {h.currency === "USD"
                              ? `$${h.avgPrice.toFixed(2)}`
                              : krw(h.avgPrice)}
                          </p>
                        </div>
                        <div className="shrink-0 text-right">
                          <p className="tnum text-[14px] text-foreground/90">
                            {krw(h.valueKrw)}
                          </p>
                          <p
                            className={cn(
                              "tnum mt-0.5 text-[11.5px]",
                              gain ? "text-approve/75" : "text-reject/75",
                            )}
                          >
                            {percent(rateOf(h))}{" "}
                            <span className="text-faint">{signedKrw(profit)}</span>
                          </p>
                        </div>
                      </button>
                    );
                  })}
                </div>

                {data.cash?.length ? (
                  <div className="mt-4 border-t border-edge-soft pt-3">
                    <p className="mb-1 px-2 text-[12px] text-faint">현금</p>
                    {data.cash.map((c) => (
                      <div key={c.id} className="flex items-baseline gap-3 rounded-lg px-2 py-2.5">
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-[14px] text-foreground/90">
                            {c.currency === "USD" ? "달러" : "원화"}
                            <span className="ms-2 text-[12px] text-faint">{c.label}</span>
                          </p>
                        </div>
                        <div className="shrink-0 text-right">
                          <p className="tnum text-[14px] text-foreground/90">
                            {c.currency === "USD"
                              ? `$${c.amount.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                              : krw(c.amount)}
                          </p>
                          {c.currency === "USD" ? (
                            <p className="tnum mt-0.5 text-[11.5px] text-faint">{krw(c.valueKrw)}</p>
                          ) : null}
                        </div>
                      </div>
                    ))}
                  </div>
                ) : null}
              </section>

              <p className="mt-6 text-[11.5px] text-faint">
                환율 {krw(data.usdKrw)}/$ 기준 ·{" "}
                {new Date(data.at).toLocaleTimeString("ko-KR", {
                  hour: "2-digit",
                  minute: "2-digit",
                })}{" "}
                기준
              </p>
            </>
          ) : !loading && !error && data ? (
            <p className="text-[13.5px] leading-relaxed text-faint">
              보유 중인 종목이 없습니다. 업비트나 증권 계좌에 자산이 생기면 여기에
              나타납니다.
            </p>
          ) : null}
        </div>
      </main>

      <TabBar pending={waiting.length} />
    </div>
  );
}
