"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { PrincipalLedger, PrincipalParts } from "@/components/assets/principal";
import { RealizedSection } from "@/components/assets/realized";
import { ProfitBridge, Row, todos } from "@/components/assets/summary";
import { TargetAllocation } from "@/components/assets/target";
import { Track } from "@/components/assets/track";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import {
  krw,
  percent,
  profitOf,
  quantity,
  rateOf,
  signedKrw,
  type Portfolio,
} from "@/lib/portfolio";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

type Fold = "bridge" | "todo" | "principal" | "track";

/**
 * What you own, and how far it has moved from what you paid.
 *
 * Four questions answered a line each - how much was made, what wants doing,
 * what went in, how the pot has grown - with the section behind each folded under
 * it, and the holdings always in view. Laid out in full it ran past four phone
 * screens; this fits the answers on one.
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
  const [open, setOpen] = useState<Record<Fold, boolean>>({
    bridge: false,
    todo: false,
    principal: false,
    track: false,
  });
  const toggle = (k: Fold) => setOpen((o) => ({ ...o, [k]: !o[k] }));

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

  // The headline return, taken apart: what was sold, what is still held, and
  // the rest (see ProfitBridge). Only with both a ledger and sales to go on.
  const realizedKrw = data?.realized?.totalKrw ?? 0;
  const unrealizedKrw = data?.holdings.reduce((sum, h) => sum + profitOf(h), 0) ?? 0;
  const bridge =
    principal && data?.realized
      ? { realizedKrw, unrealizedKrw, otherKrw: profit - realizedKrw - unrealizedKrw }
      : null;
  const todo = data ? todos(data) : [];
  const overseas = data?.realized?.tax.baskets.find((b) => b.kind === "overseas");
  const since = principal
    ? `${Number(principal.since.slice(5, 7))}/${Number(principal.since.slice(8, 10))}`
    : "";
  const track = data?.track ?? [];
  const first = track[0];
  const rateOf0 = (p: { totalKrw: number; principalKrw: number }) =>
    p.principalKrw > 0 ? (p.totalKrw - p.principalKrw) / p.principalKrw : 0;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-5 pb-12 sm:px-8 sm:pt-7 sm:pb-6 lg:grid lg:max-w-[72rem] lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)] lg:gap-14">
          <div className="min-w-0">
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
                    기준 · 환율 {krw(data.usdKrw)}/$
                    {data.stale || loading ? " · 갱신 중…" : ""}
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

            {error ? (
              <p
                role="status"
                className="mb-5 border-l-2 border-reject pl-3 text-[13.5px] leading-relaxed text-dim"
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
              <div className="border-b border-edge-soft">
                <Row
                  id="assets-bridge"
                  title="얼마 벌었나"
                  open={open.bridge}
                  onToggle={() => toggle("bridge")}
                  detail={data.realized ? <RealizedSection realized={data.realized} /> : null}
                >
                  {bridge ? (
                    <ProfitBridge {...bridge} />
                  ) : (
                    <span className="tnum text-[13px] text-dim">
                      평가손익 {signedKrw(unrealizedKrw)}
                    </span>
                  )}
                  {overseas?.inForce ? (
                    <p className="tnum mt-2 text-[12px] text-faint">
                      올해 해외주식 {signedKrw(overseas.gainKrw)} · 양도세{" "}
                      {overseas.taxKrw > 0 ? `약 ${krw(overseas.taxKrw)}` : "없음"} · 공제{" "}
                      {krw(Math.max(0, overseas.deductionKrw - Math.max(0, overseas.gainKrw)))} 남음
                    </p>
                  ) : null}
                </Row>

                <Row
                  id="assets-todo"
                  title="할 일"
                  open={open.todo}
                  onToggle={() => toggle("todo")}
                  detail={
                    data.allocation ? (
                      <TargetAllocation
                        allocation={data.allocation}
                        holdings={data.holdings}
                        goldGramKrw={data.goldGramKrw}
                        tax={data.realized?.tax}
                      />
                    ) : null
                  }
                >
                  {todo.length ? (
                    <ul className="space-y-1">
                      {todo.map((t) => (
                        <li
                          key={t.text}
                          className="flex gap-2 text-[13px] leading-snug text-foreground/90"
                        >
                          <span
                            aria-hidden
                            className="mt-[0.45em] size-1.5 shrink-0 rounded-full bg-reject"
                          />
                          {t.text}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="text-[13px] text-dim">
                      없어요{" "}
                      <span className="text-faint">· 비중은 허용 범위 안, 원금 기록도 맞아요</span>
                    </p>
                  )}
                </Row>

                {principal ? (
                  <Row
                    id="assets-principal"
                    title="원금"
                    open={open.principal}
                    onToggle={() => toggle("principal")}
                    detail={
                      <>
                        <div className="mb-6">
                          <PrincipalParts parts={principal.parts} fixed={data.fixed ?? []} />
                        </div>
                        <PrincipalLedger principal={principal} onChange={() => void load()} />
                      </>
                    }
                  >
                    <p className="tnum text-[13px] text-foreground/90">
                      {krw(principal.principalKrw)}
                      <span className="ms-2 text-faint">{since}부터 넣은 돈</span>
                    </p>
                  </Row>
                ) : null}

                {/* The whole pot over time - its size and its return - not
                    any one holding's price. */}
                <Row
                  id="assets-track"
                  title="자산 변화"
                  open={open.track}
                  onToggle={() => toggle("track")}
                  detail={<Track points={track} />}
                >
                  {first && track.length >= 2 ? (
                    <p className="tnum text-[13px] text-dim">
                      {Number(first.date.slice(5, 7))}/{Number(first.date.slice(8, 10))}부터{" "}
                      <span className="whitespace-nowrap text-foreground/90">
                        {krw(first.totalKrw)} → {krw(data.totalKrw)}
                      </span>{" "}
                      <span className="whitespace-nowrap">
                        수익률 {percent(rateOf0(first), 1)} → {percent(rate, 1)}
                      </span>
                    </p>
                  ) : (
                    <p className="text-[13px] text-faint">하루에 한 번 기록해요. 이틀치부터 보여요.</p>
                  )}
                </Row>
              </div>
            ) : null}
          </div>

          <div className="min-w-0 max-lg:mt-8">
            {data && data.holdings.length > 0 ? (
              <section aria-label="보유 종목">
                <p className="mb-1 px-2 text-[12px] text-faint">보유</p>
                <div className="space-y-0.5">
                  {data.holdings.map((h) => {
                    const profit = profitOf(h);
                    const gain = profit >= 0;
                    // Sold earlier, so no longer in the row's own profit.
                    const sold = data.realized?.lines.find((l) => l.id === h.id)?.profitKrw ?? 0;
                    return (
                      <div key={h.id} className="flex items-baseline gap-3 rounded-lg px-2 py-2.5">
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
                            {data.totalKrw > 0 ? ` · ${Math.round((h.valueKrw / data.totalKrw) * 100)}%` : ""}
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
                          {sold !== 0 ? (
                            <p className="tnum mt-0.5 text-[11.5px] text-faint">
                              판 것{" "}
                              <span className={sold >= 0 ? "text-approve/75" : "text-reject/75"}>{signedKrw(sold)}</span>
                              <span className="ms-1.5">합계 {signedKrw(profit + sold)}</span>
                            </p>
                          ) : null}
                        </div>
                      </div>
                    );
                  })}
                </div>

                {data.cash?.some((c) => c.valueKrw >= 1) ? (
                  <div className="mt-4 border-t border-edge-soft pt-3">
                    <p className="mb-1 px-2 text-[12px] text-faint">현금</p>
                    {data.cash
                      .filter((c) => c.valueKrw >= 1)
                      .map((c) => (
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
            ) : !loading && !error && data ? (
              <p className="text-[13.5px] leading-relaxed text-faint">
                보유 중인 종목이 없습니다. 업비트나 증권 계좌에 자산이 생기면 여기에
                나타납니다.
              </p>
            ) : null}
          </div>
        </div>
      </main>

      <TabBar pending={waiting.length} />
    </div>
  );
}
