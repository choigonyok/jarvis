"use client";

import { useMemo, useState } from "react";
import { plannedGains } from "@/components/assets/realized";
import { type Holding, type Realized, krw, signedKrw, taxFor } from "@/lib/portfolio";
import { cn } from "@/lib/utils";
import type { Allocation, Bucket, Drift } from "@/lib/target";
import { type Order, depositPlan, rebalancePlan } from "@/lib/trade-plan";

/**
 * Each bucket keeps one hue everywhere it appears. None of them is green or
 * red - those mean profit and loss on this surface - and gold is the one
 * colour allowed to be literal.
 */
const HUE: Record<Bucket, string> = {
  growth: "oklch(0.7 0.085 252)",
  coin: "oklch(0.7 0.1 298)",
  cash: "oklch(0.82 0.008 265)",
  dividend: "oklch(0.6 0.045 225)",
  gold: "oklch(0.8 0.105 82)",
};

const pct = (rate: number) => {
  const v = rate * 100;
  return `${v > 0 && v < 1 ? v.toFixed(1) : Math.round(v)}%`;
};

function Dot({ id }: { id: Bucket }) {
  return (
    <span
      aria-hidden
      className="size-2 shrink-0 rounded-[2px]"
      style={{ background: HUE[id] }}
    />
  );
}

/** The band, from assets-svc; an older one did not send it, so the same rule stands in. */
const bandOf = (r: Drift) => r.band ?? Math.min(0.07, r.target / 2);
const SHORT: Record<Bucket, string> = { growth: "유망주", coin: "코인", cash: "현금", dividend: "배당", gold: "금" };

/**
 * How far each bucket sits from its target, as five small columns: the
 * middle line is the target, a bar up is over and down is under, and the
 * shaded strip is the band it may wander in. A bar through the strip's edge
 * is out of band - the one thing the folded view needs to show, readable
 * even for a 5% bucket that a stacked bar draws as a sliver.
 */
export function DriftColumns({ allocation }: { allocation: Allocation }) {
  const { rows } = allocation;
  // One scale for all, so a bigger bar is a bigger miss.
  const reach = Math.max(...rows.map((r) => Math.max(Math.abs(r.current - r.target), bandOf(r))), 0.01) * 1.15;
  const H = 44;
  const y = (d: number) => H / 2 - (d / reach) * (H / 2);
  return (
    <ul className="grid grid-cols-5 gap-2" aria-label="목표 대비 차이">
      {rows.map((r) => {
        const d = r.current - r.target;
        const band = bandOf(r);
        const pp = Math.round(d * 100);
        return (
          <li key={r.id} className="min-w-0 text-center">
            <svg
              viewBox={`0 0 20 ${H}`}
              preserveAspectRatio="none"
              className="h-11 w-full"
              role="img"
              aria-label={`${r.label} 지금 ${pct(r.current)}, 목표 ${pct(r.target)}${r.inBand ? "" : ", 허용 범위 밖"}`}
            >
              <rect x="0" y={y(band)} width="20" height={y(-band) - y(band)} rx="1.5" fill="oklch(1 0 0 / 6%)" />
              <line x1="0" x2="20" y1={H / 2} y2={H / 2} stroke="oklch(1 0 0 / 30%)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
              <rect
                x="7"
                y={Math.min(y(d), H / 2)}
                width="6"
                height={Math.max(0.6, Math.abs(y(d) - H / 2))}
                rx="1"
                fill={HUE[r.id]}
                opacity={r.inBand ? 0.75 : 1}
              />
              {!r.inBand ? (
                <rect
                  x="5.5"
                  y={Math.min(y(d), H / 2) - 1.5}
                  width="9"
                  height={Math.abs(y(d) - H / 2) + 3}
                  rx="2"
                  fill="none"
                  className="stroke-reject"
                  strokeWidth="1.2"
                  vectorEffect="non-scaling-stroke"
                />
              ) : null}
            </svg>
            <p className={cn("mt-1 truncate text-[11px]", r.inBand ? "text-faint" : "text-foreground")}>{SHORT[r.id]}</p>
            <p className={cn("tnum text-[11px]", r.inBand ? "text-faint" : "font-semibold text-foreground")}>
              {pp === 0 ? "0" : `${pp > 0 ? "+" : "−"}${Math.abs(pp)}%p`}
            </p>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * The bar explained, and what to do about it: the buckets as "now (target)",
 * the orders only when something has left its band, and a field for new
 * cash that answers with what to buy. The opened view.
 */
export function AllocationDetail({
  allocation,
  holdings,
  goldGramKrw,
  tax,
}: {
  allocation: Allocation;
  holdings: Holding[];
  goldGramKrw?: number | null;
  tax?: Realized["tax"];
}) {
  const { rows } = allocation;
  const [amount, setAmount] = useState("");
  const deposit = Number(amount.replace(/[^\d]/g, "")) || 0;
  const rebalance = useMemo(() => rebalancePlan(allocation, holdings, { goldGramKrw }), [allocation, holdings, goldGramKrw]);
  const fill = useMemo(
    () => (deposit > 0 ? depositPlan(allocation, holdings, deposit, { goldGramKrw }) : null),
    [allocation, holdings, deposit, goldGramKrw],
  );
  const sells = rebalance.orders.filter((o) => o.side === "sell");
  const gains = plannedGains(sells);
  const overseas = tax?.baskets.find((b) => b.kind === "overseas" && b.inForce);
  const taxUp = overseas ? taxFor(overseas.gainKrw + gains.overseas, overseas) - taxFor(overseas.gainKrw, overseas) : 0;

  return (
    <div className="space-y-2.5">
      <ul className="divide-y divide-edge-soft border-y border-edge-soft">
        {rows.map((r) => (
          <li key={r.id} className="tnum flex items-baseline gap-2 py-2 text-[12.5px]">
            <Dot id={r.id} />
            <span className="min-w-0 flex-1 truncate">
              <span className={r.inBand ? "text-dim" : "text-foreground"}>{r.label}</span>
              {r.symbols.length ? <span className="ms-1.5 text-[11px] text-faint">{r.symbols.join(" ")}</span> : null}
            </span>
            <span className={r.inBand ? "text-dim" : "font-semibold text-foreground"}>{pct(r.current)}</span>
            <span className="text-faint">→ {pct(r.target)}</span>
            <span className="w-[5.5rem] shrink-0 text-right text-[11.5px] text-faint">
              {Math.abs(r.gapKrw) < 10_000 ? "맞음" : `${krwShort(Math.abs(r.gapKrw))} ${r.gapKrw > 0 ? "부족" : "많음"}`}
            </span>
          </li>
        ))}
      </ul>
      <p className="text-[11px] leading-relaxed text-faint">
        허용 범위는 목표 ±7%p, 비중이 작은 자산은 목표의 절반까지예요. 그림의 옅은 띠가 그 범위예요.
      </p>

      {rebalance.orders.length || rebalance.unpicked.length ? (
        <div className="text-[12.5px] leading-relaxed">
          <p className="text-dim">
            주 단위로 맞추면{" "}
            {[
              ...rebalance.orders.map((o) => orderText(o)),
              ...rebalance.unpicked.map((u) => `${u.label} ${krw(u.amountKrw)} (종목 골라야 함)`),
            ].join(" · ")}
          </p>
          {overseas && Math.abs(gains.overseas) >= 1_000 ? (
            <p className="tnum text-faint">
              이번 매도 실현손익 약 {signedKrw(gains.overseas)} ·{" "}
              {taxUp > 0 ? `양도세 약 ${krw(taxUp)} 늘어남` : "공제 안이라 양도세 없음"}
            </p>
          ) : null}
        </div>
      ) : null}

      <label className="flex flex-wrap items-center gap-x-2 gap-y-1.5 text-[12.5px] text-dim">
        <span className="shrink-0">새로 넣을 돈</span>
        <span className="relative">
          <input
            inputMode="numeric"
            value={deposit ? deposit.toLocaleString("ko-KR") : ""}
            onChange={(e) => setAmount(e.target.value)}
            placeholder="예: 1,000,000"
            className="tnum h-9 w-36 rounded-lg border border-edge-soft bg-glass px-2.5 pe-7 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-8 sm:text-[13px]"
          />
          <span aria-hidden className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-[12px] text-faint">
            원
          </span>
        </span>
        {fill ? (
          <span className="basis-full text-dim sm:basis-auto">
            {fill.orders.length
              ? `→ ${fill.orders.map((o) => orderText(o)).join(" · ")}`
              : "→ 이 금액으로는 살 수 있는 단위가 없어요"}
            {fill.cashChangeKrw >= 1 ? <span className="text-faint"> · 남는 현금 {krw(fill.cashChangeKrw)}</span> : null}
          </span>
        ) : null}
      </label>
    </div>
  );
}

/** ₩136,000 → "₩13.6만"; under ten thousand stays in won. */
function krwShort(v: number): string {
  if (v < 10_000) return krw(v);
  const man = v / 10_000;
  return `₩${man >= 100 ? Math.round(man).toLocaleString("ko-KR") : man.toFixed(1)}만`;
}

/** "스페이스X 2주 팔기 약 ₩44만", "BTC ₩32만 사기". */
function orderText(o: Order): string {
  const name = o.holding?.name || o.holding?.symbol || "";
  const verb = o.side === "sell" ? "팔기" : "사기";
  const man = `₩${Math.round(o.amountKrw / 10_000).toLocaleString("ko-KR")}만`;
  if (o.units === null) return `${name} ${o.amountKrw >= 10_000 ? man : krw(o.amountKrw)} ${verb}`;
  const unit = o.holding?.kind === "gold" ? "g" : "주";
  return `${name} ${o.units}${unit} ${verb} 약 ${o.amountKrw >= 10_000 ? man : krw(o.amountKrw)}`;
}
