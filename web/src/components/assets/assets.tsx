"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { OrderRecord, OrderSheet, PlanSheet } from "@/components/assets/orders";
import { OutflowList, OutflowSummary } from "@/components/assets/outflows";
import { RealizedSection } from "@/components/assets/realized";
import { ProfitLines, ProfitOther, Row } from "@/components/assets/summary";
import { AllocationDetail, DriftColumns } from "@/components/assets/target";
import { Track } from "@/components/assets/track";
import { Failed } from "@/components/shell/failed";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { StandingBar } from "@/components/shell/standing-bar";
import { AsOf } from "@/components/shell/as-of";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { useKept } from "@/lib/keep";
import {
  checkRebalance,
  recentOrders,
  requestsOf,
  sendOrders,
  standingOrders,
  type OrderResult,
  type PlacedOrder,
  type Standing,
} from "@/lib/orders";
import {
  krw,
  percent,
  profitOf,
  quantity,
  rateOf,
  signedKrw,
  type Holding,
  type Portfolio,
} from "@/lib/portfolio";
import { onFocusWanted, wantedFocus } from "@/lib/focus";
import type { Plan } from "@/lib/trade-plan";
import { onResume } from "@/lib/resume";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { dayLabel, dayOf, monthDay } from "@/lib/when";
import { cn } from "@/lib/utils";

type Fold = "bridge" | "alloc" | "orders" | "principal";

/** A confirm sheet for several orders: what it is called, why, and how its orders are checked. */
type PlanAsk = { title: string; reason: string; load: () => Promise<{ results: OrderResult[]; planId?: string | null }> };

/**
 * What you own, and how far it has moved from what you paid.
 *
 * Three questions answered a line each - how much was made, what wants doing,
 * what went in - with the detail behind the first two folded under them, the
 * holdings always in view, and the pot's size and return over time below. Laid out in full it ran past four phone
 * screens; this fits the answers on one.
 *
 * Orders start here too - a holding tapped, the rebalance plan, what new cash
 * buys - and every one ends on a sheet that names exactly what will be sent,
 * checked by assets-svc first, with one button that sends that (orders.tsx).
 * The rebalance card mind raises in the conversation sends the same orders
 * when approved.
 */
export function Assets() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

  const [data, setData] = useState<Portfolio | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState<Record<Fold, boolean>>({ bridge: false, alloc: false, orders: false, principal: false });
  const toggle = (k: Fold) => setOpen((o) => ({ ...o, [k]: !o[k] }));
  // The order sheet left open stays open across a tab switch (lib/keep.ts).
  const [ordering, setOrdering] = useKept<Holding | null>("assets:ordering", null);
  const [asking, setAsking] = useState<PlanAsk | null>(null);
  const [record, setRecord] = useState<PlacedOrder[]>([]);
  const [standing, setStanding] = useState<Standing[]>([]);
  const reloadRecord = useCallback(() => {
    // As far back as assets-svc keeps on one page; the record pages them (orders.tsx).
    void recentOrders(100).then(setRecord);
    void standingOrders().then(setStanding);
  }, []);
  useEffect(() => {
    reloadRecord();
  }, [reloadRecord]);

  // A notification about a section opens its sheet (lib/focus.ts): the
  // allocation and its rows, the tax inside 수익. What it names is in the sheet,
  // so the sheet opens when it is wanted, not when it is found.
  useEffect(() => {
    const show = (id: string) => {
      if (id.startsWith("allocation")) setOpen((o) => ({ ...o, alloc: true }));
      if (id === "tax") setOpen((o) => ({ ...o, bridge: true }));
      if (id === "principal") setOpen((o) => ({ ...o, principal: true }));
    };
    const first = wantedFocus();
    if (first) show(first);
    const stop = onFocusWanted(show);
    const found = (e: Event) => show((e.target as HTMLElement).dataset.focus ?? "");
    document.addEventListener("jarvis:focused", found);
    return () => {
      stop();
      document.removeEventListener("jarvis:focused", found);
    };
  }, []);


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
      setError("잔고를 불러오지 못했어요.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    again.current = (tries) => void load(false, tries);
  }, [load]);
  // Back to the app, or a pull to refresh: what the service has now.
  useEffect(() => onResume(() => load()), [load]);

  // A placed order changes the holdings: read them afresh, and the record.
  const placed = () => {
    reloadRecord();
    window.setTimeout(() => void load(true), 1500);
  };

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

  // The headline return, taken apart by assets-svc (see ProfitBridge).
  const bridge = data?.bridge ?? null;
  const unrealizedKrw = data?.holdings.reduce((sum, h) => sum + profitOf(h), 0) ?? 0;
  // Off target: a bucket outside its band, said as the transfer that fixes it.
  const allocation = data?.allocation;
  const offBand = allocation ? allocation.rows.filter((r) => !r.inBand) : [];
  const fixes = allocation
    ? allocation.moves
        .filter((m) => !m.optional)
        .map((m) => {
          const label = (id: string) => allocation.rows.find((r) => r.id === id)?.label ?? id;
          return `${label(m.from)}에서 ${label(m.to)}로 ${krw(m.amountKrw)} 옮기기`;
        })
    : [];
  const since = principal ? monthDay(principal.since) : "";
  const track = data?.track ?? [];
  const first = track[0];
  const rateOf0 = (p: { totalKrw: number; principalKrw: number }) =>
    p.principalKrw > 0 ? (p.totalKrw - p.principalKrw) / p.principalKrw : 0;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={waiting.length} />
        <StandingBar pending={waiting} />
      </PageTop>

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar">
        <div className="mx-auto w-full max-w-page px-4 pt-5 pb-12 sm:px-8 sm:pt-7 sm:pb-6 lg:grid lg:max-w-page-wider lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)] lg:gap-14">
          <div className="min-w-0">
            <div className="mb-6 flex items-start justify-between gap-4">
              <div>
                <p className="text-aside text-faint">총자산</p>
                <p className="tnum mt-0.5 text-display leading-none font-semibold tracking-tight text-foreground">
                  {data ? krw(data.totalKrw) : "—"}
                </p>
                {data ? (
                  <p className="tnum mt-2 text-ui">
                    <span className={cn(up ? "text-approve" : "text-reject")}>
                      {signedKrw(profit)}
                    </span>
                    <span className="ms-2 text-dim">{percent(rate)}</span>
                    <span className="ms-2 text-faint">
                      {principal
                        ? `원금 ${krw(principal.principalKrw)} 대비`
                        : "평가손익 (원금 기록을 읽지 못함)"}
                    </span>
                  </p>
                ) : loading ? (
                  // The profit line's place while the total is read: it arrives
                  // into it rather than pushing the holdings down.
                  <p aria-hidden className="mt-2 h-5 w-48 rounded bg-well" />
                ) : null}
                {data ? (
                  <AsOf
                    at={data.at}
                    seconds
                    busy={data.stale || loading}
                    failed={error !== null}
                    onRetry={() => void load(true)}
                    className="mt-1.5"
                  >
                    {" "}· 환율 {krw(data.usdKrw)}/$
                  </AsOf>
                ) : null}
              </div>

              <button
                type="button"
                // The button means "now": the service drops what it cached.
                onClick={() => void load(true)}
                disabled={loading}
                aria-label="잔고 새로 불러오기"
                className="tap flex shrink-0 items-center justify-center rounded-lg text-faint transition-colors hover:text-dim focus-ring disabled:opacity-40 mouse:size-9 mouse:min-h-0 mouse:min-w-0"
              >
                <RefreshCw
                  aria-hidden
                  className={cn("size-4", loading && "anim-spin-slow")}
                />
              </button>
            </div>

            {/* Nothing read yet: the whole screen says it. A refresh that
                failed over figures already shown is said on their line. */}
            {error && !data ? (
              <Failed className="mb-5">{error} 키 설정을 확인하고 다시 시도해 주세요.</Failed>
            ) : null}

            {/* A venue that failed is named. A total quietly missing a holding
                would be wrong with nothing saying so. */}
            {data?.problems.length ? (
              <ul className="mb-5 space-y-1">
                {data.problems.map((problem) => (
                  <li key={problem}>
                    <Failed quiet>{problem}</Failed>
                  </li>
                ))}
              </ul>
            ) : null}

            {data && data.holdings.length > 0 ? (
              <div className="border-b border-edge-soft">
                <Row
                  id="assets-bridge"
                  title="수익"
                  focus="summary"
                  open={open.bridge}
                  onToggle={() => toggle("bridge")}
                  detail={
                    <div className="space-y-5">
                      {bridge ? <ProfitOther {...bridge} /> : null}
                      {data.realized ? (
                        <div data-focus="tax">
                          <RealizedSection realized={data.realized} />
                        </div>
                      ) : null}
                    </div>
                  }
                >
                  {bridge ? (
                    <ProfitLines {...bridge} />
                  ) : (
                    <span className="tnum text-ui text-dim">평가손익 {signedKrw(unrealizedKrw)}</span>
                  )}
                </Row>

                {allocation ? (
                  <Row
                    id="assets-alloc"
                    title="비중"
                    focus="allocation"
                    open={open.alloc}
                    onToggle={() => toggle("alloc")}
                    detail={
                      <AllocationDetail
                        allocation={allocation}
                        tax={data.realized?.tax}
                        onRebalance={() => {
                          setOpen((o) => ({ ...o, alloc: false }));
                          setAsking({ title: "리밸런싱 주문", reason: "rebalance", load: checkRebalance });
                        }}
                        onFill={(plan: Plan) => {
                          setOpen((o) => ({ ...o, alloc: false }));
                          setAsking({
                            title: "새로 넣을 돈으로 사기",
                            reason: "deposit",
                            load: () => sendOrders(requestsOf(plan), { reason: "deposit", dryRun: true }),
                          });
                        }}
                      />
                    }
                  >
                    {offBand.length ? (
                      <p className="flex items-baseline gap-2 text-ui text-ink">
                        <span aria-hidden className="size-1.5 shrink-0 self-center rounded-full bg-reject" />
                        비정상
                        <span className="text-aside text-dim">{fixes.join(" · ")}</span>
                      </p>
                    ) : (
                      <p className="text-ui text-dim">정상</p>
                    )}
                    <div className="mt-3">
                      <DriftColumns allocation={allocation} />
                    </div>
                  </Row>
                ) : null}

                {principal ? (
                  <Row
                    id="assets-principal"
                    title="원금"
                    focus="principal"
                    open={open.principal}
                    // Folds open on where the money taken out went - once there is any.
                    onToggle={principal.outflows?.length ? () => toggle("principal") : undefined}
                    detail={
                      principal.outflows?.length ? (
                        <OutflowList outflows={principal.outflows} onChanged={() => void load(true)} />
                      ) : undefined
                    }
                  >
                    <p className="tnum text-ui text-ink">
                      {krw(principal.principalKrw)}
                      <span className="ms-2 text-faint">{since}부터 넣은 돈</span>
                    </p>
                    {/* A ledger that disagrees with the trades is the one thing said here. */}
                    {principal.checks?.map((c) => (
                      <p key={`${c.kind}-${c.date ?? ""}`} className="mt-1 text-aside leading-relaxed text-dim">
                        <span aria-hidden className="me-1.5 inline-block size-1.5 rounded-full bg-reject align-middle" />
                        {c.message}
                      </p>
                    ))}
                    {principal.outflows ? <OutflowSummary outflows={principal.outflows} /> : null}
                  </Row>
                ) : null}

                <Row
                  id="assets-orders"
                  title="주문"
                  focus="orders"
                  open={open.orders}
                  onToggle={() => toggle("orders")}
                  detail={<OrderRecord orders={record} holdings={data.holdings} standing={standing} onChanged={reloadRecord} />}
                >
                  <p className="text-ui text-dim">
                    {standing.some((o) => o.status === "active")
                      ? `대기 주문 ${standing.filter((o) => o.status === "active").length}건 · ` +
                        standing
                          .filter((o) => o.status === "active")
                          .map((o) => `${data.holdings.find((h) => h.symbol === o.symbol)?.name ?? o.symbol} $${o.limitPrice.toFixed(2)} ${o.side === "buy" ? "이하 매수" : "이상 매도"}`)
                          .join(", ")
                      : record[0]
                      ? `최근 ${dayLabel(dayOf(new Date(record[0].createdAt)))} · ${record.filter((o) => o.status === "placed").length}건 보냄`
                      : "종목을 누르면 사고팔 수 있어요"}
                  </p>
                </Row>
              </div>
            ) : null}
          </div>

          <div className="min-w-0 max-lg:mt-8">
            {data && data.holdings.length > 0 ? (
              <section aria-label="보유 종목">
                <p className="mb-1 px-2 text-aside text-faint">보유</p>
                <div className="space-y-0.5">
                  {data.holdings.map((h) => {
                    const profit = profitOf(h);
                    const gain = profit >= 0;
                    // Sold earlier, so no longer in the row's own profit.
                    const sold = data.realized?.lines.find((l) => l.id === h.id)?.profitKrw ?? 0;
                    return (
                      <button
                        type="button"
                        key={h.id}
                        data-focus={`holding:${h.id}`}
                        onClick={() => setOrdering(h)}
                        aria-label={`${h.name} 주문`}
                        className="flex w-full items-baseline gap-3 rounded-lg px-2 py-2.5 text-left transition-colors hover:bg-glass focus-ring"
                      >
                        <span className="block min-w-0 flex-1">
                          <span className="block truncate text-body text-ink">
                            {h.name}
                          </span>
                          <span className="block tnum mt-0.5 text-meta text-faint">
                            {quantity(h.quantity)}
                            {h.kind === "stock" ? "주" : h.kind === "gold" ? "g" : ""} · 평단{" "}
                            {h.currency === "USD"
                              ? `$${h.avgPrice.toFixed(2)}`
                              : krw(h.avgPrice)}
                            {data.totalKrw > 0 ? ` · ${Math.round((h.valueKrw / data.totalKrw) * 100)}%` : ""}
                          </span>
                        </span>
                        <span className="block shrink-0 text-right">
                          <span className="block tnum text-body text-ink">
                            {krw(h.valueKrw)}
                          </span>
                          <span
                            className={cn(
                              "tnum mt-0.5 block text-meta",
                              gain ? "text-approve" : "text-reject",
                            )}
                          >
                            {percent(rateOf(h))}{" "}
                            <span className="text-faint">{signedKrw(profit)}</span>
                          </span>
                          {sold !== 0 ? (
                            <span className="block tnum mt-0.5 text-meta text-faint">
                              판 것{" "}
                              <span className={sold >= 0 ? "text-approve" : "text-reject"}>{signedKrw(sold)}</span>
                              <span className="ms-1.5">합계 {signedKrw(profit + sold)}</span>
                            </span>
                          ) : null}
                        </span>
                      </button>
                    );
                  })}


                  {/* Cash and the fixed amounts are held too: one list, each
                      with its share of the total. A coin's dust under ₩1 is
                      not a line; an account's won is, even near empty. */}
                  {(data.cash ?? [])
                    .filter((c) => c.valueKrw >= 1 || c.currency === "KRW")
                    .map((c) => (
                      <div key={c.id} className="flex items-baseline gap-3 rounded-lg px-2 py-2.5">
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-body text-ink">
                            {c.currency === "USD" ? "달러" : "원화"}
                            <span className="ms-2 text-aside text-faint">{c.label}</span>
                          </p>
                          <p className="tnum mt-0.5 text-meta text-faint">
                            현금
                            {data.totalKrw > 0 ? ` · ${Math.round((c.valueKrw / data.totalKrw) * 100)}%` : ""}
                          </p>
                        </div>
                        <div className="shrink-0 text-right">
                          <p className="tnum text-body text-ink">
                            {c.currency === "USD"
                              ? `$${c.amount.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
                              : krw(c.amount)}
                          </p>
                          {c.currency === "USD" ? (
                            <p className="tnum mt-0.5 text-meta text-faint">{krw(c.valueKrw)}</p>
                          ) : null}
                        </div>
                      </div>
                    ))}
                  {(data.fixed ?? []).map((f) => (
                    <div key={f.id} className="flex items-baseline gap-3 rounded-lg px-2 py-2.5">
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-body text-ink">{f.label}</p>
                        <p className="tnum mt-0.5 text-meta text-faint">
                          고정 · 비중 계산 제외
                          {data.totalKrw > 0 ? ` · ${Math.round((f.valueKrw / data.totalKrw) * 100)}%` : ""}
                        </p>
                      </div>
                      <p className="tnum shrink-0 text-right text-body text-ink">{krw(f.valueKrw)}</p>
                    </div>
                  ))}
                </div>
              </section>
            ) : !loading && !error && data ? (
              <Empty align="start" className="py-6">
                <EmptyHeader>
                  <EmptyTitle>보유 중인 종목이 없어요</EmptyTitle>
                  <EmptyDescription>업비트나 증권 계좌에 자산이 생기면 여기에 나타나요.</EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : null}
          </div>

          {/* The whole pot over time - its size and its return - not any
              one holding's price. Below everything, across both columns. */}
          {data && data.holdings.length > 0 ? (
            <section aria-labelledby="assets-track-title" className="mt-10 lg:col-span-2">
              <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                <h2 id="assets-track-title" className="text-ui text-dim">
                  자산 변화
                </h2>
                {first && track.length >= 2 ? (
                  <p className="tnum text-aside text-faint">
                    {Number(first.date.slice(5, 7))}/{Number(first.date.slice(8, 10))}부터{" "}
                    <span className="whitespace-nowrap">
                      {krw(first.totalKrw)} → {krw(data.totalKrw)}
                    </span>{" "}
                    <span className="whitespace-nowrap">
                      수익률 {percent(rateOf0(first), 1)} → {percent(rate, 1)}
                    </span>
                  </p>
                ) : null}
              </div>
              <Track points={track} />
            </section>
          ) : null}
        </div>
      </main>

      <OrderSheet
        holding={ordering}
        usdKrw={data?.usdKrw ?? 0}
        onOpenChange={(v) => !v && setOrdering(null)}
        onPlaced={placed}
      />
      {asking ? (
        <PlanSheet
          open
          onOpenChange={(v) => !v && setAsking(null)}
          title={asking.title}
          reason={asking.reason}
          load={asking.load}
          holdings={data?.holdings ?? []}
          onPlaced={placed}
        />
      ) : null}

      <TabBar pending={waiting.length} />
    </div>
  );
}
