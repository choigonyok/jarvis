"use client";

import { useMemo, useState } from "react";
import { type Holding, krw } from "@/lib/portfolio";
import type { Allocation, Bucket, Drift } from "@/lib/target";
import { type Order, type Plan, depositPlan, rebalancePlan } from "@/lib/trade-plan";
import { cn } from "@/lib/utils";

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

/**
 * Where the pot stands against the allocation it is meant to have.
 *
 * The target and the present are drawn as two stacked bars, in the same
 * order, and each bucket's slice is joined to itself by a ribbon. A ribbon
 * that runs straight down is on target; the further it leans, the further
 * that bucket has drifted - so the picture answers "how far off" before any
 * number is read. Below it, the gaps are paired into transfers, because what
 * you actually do is move money from one bucket into another.
 */
export function TargetAllocation({ allocation, holdings }: { allocation: Allocation; holdings: Holding[] }) {
  const { rows, moves, totalKrw } = allocation;
  if (totalKrw <= 0) return null;

  const due = moves.filter((m) => !m.optional);
  const dueKrw = due.reduce((sum, m) => sum + m.amountKrw, 0);
  const label = (id: Bucket) => rows.find((r) => r.id === id)!.label;

  return (
    <div>
      <p className="text-[15px] leading-snug text-foreground/90">
        {dueKrw > 0 ? (
          <>
            목표까지 <span className="tnum font-semibold">{krw(dueKrw)}</span>
            을 옮기면 돼요
          </>
        ) : (
          "목표 비중대로예요"
        )}
      </p>
      <p className="mt-1 text-[12px] text-faint">
        {rows.filter((r) => !r.inBand).length}개 자산이 허용 범위 밖
      </p>

      <Ribbons rows={rows} />

      {moves.length > 0 ? (
        <ul className="mt-5 space-y-1" aria-label="옮길 돈">
          {moves.map((m) => (
            <li
              key={`${m.from}-${m.to}`}
              className={cn(
                "flex items-center gap-2 rounded-lg bg-glass px-3 py-2.5",
                m.optional && "bg-transparent",
              )}
            >
              <Dot id={m.from} />
              <span
                className={cn(
                  "text-[13px]",
                  m.optional ? "text-faint" : "text-dim",
                )}
              >
                {label(m.from)}
              </span>
              <span className="sr-only">에서</span>
              <svg
                aria-hidden
                viewBox="0 0 16 8"
                className="h-2 w-4 shrink-0 text-faint"
              >
                <path
                  d="M0 4h14m-3-3 3 3-3 3"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.2"
                />
              </svg>
              <Dot id={m.to} />
              <span
                className={cn(
                  "min-w-0 truncate text-[13px]",
                  m.optional ? "text-faint" : "text-foreground/90",
                )}
              >
                {label(m.to)}
              </span>
              <span
                className={cn(
                  "tnum ms-auto shrink-0 text-[13.5px]",
                  m.optional ? "text-faint" : "text-foreground/90",
                )}
              >
                {krw(m.amountKrw)}
                {m.optional ? (
                  <span className="ms-1.5 text-[11px]">여유</span>
                ) : null}
              </span>
            </li>
          ))}
        </ul>
      ) : null}

      <table className="mt-6 w-full text-[12.5px]">
        <thead>
          <tr className="text-[11px] text-faint">
            <th className="pb-2 text-left font-normal">자산</th>
            <th className="pb-2 text-right font-normal">지금</th>
            <th className="pb-2 text-right font-normal">목표</th>
            <th className="pb-2 text-right font-normal">차이</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const pp = (r.current - r.target) * 100;
            return (
              <tr key={r.id} className="border-t border-edge-soft">
                <td className="py-2 pr-2">
                  <span className="flex min-w-0 items-center gap-2">
                    <Dot id={r.id} />
                    <span className="shrink-0 text-dim">{r.label}</span>
                    <span className="truncate text-[11px] text-faint">
                      {r.symbols.join(" ")}
                    </span>
                  </span>
                </td>
                <td className="tnum py-2 text-right text-foreground/90">
                  {pct(r.current)}
                  <span className="hidden ps-2 text-[11px] text-faint sm:inline">
                    {krw(r.valueKrw)}
                  </span>
                </td>
                <td className="tnum py-2 text-right text-faint">{pct(r.target)}</td>
                <td
                  className={cn(
                    "tnum py-2 pl-2 text-right",
                    r.inBand ? "text-faint" : "text-foreground/90",
                  )}
                >
                  {Math.abs(pp) < 0.5
                    ? "0"
                    : `${pp > 0 ? "+" : "−"}${Math.abs(pp).toFixed(0)}%p`}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>

      <p className="mt-3 text-[11px] leading-relaxed text-faint">
        허용 범위는 목표 ±7%p, 비중이 작은 자산은 목표의 절반이에요. 범위 안에서
        생긴 차이는 &lsquo;여유&rsquo;로 표시해요.
      </p>

      <Planner allocation={allocation} holdings={holdings} />
    </div>
  );
}

/**
 * The same gaps as orders that can be placed: whole shares, grams of gold,
 * coins by amount. Two questions - "rebalance what I have" and "I have new
 * cash, where does it go" - answered with the same rounding.
 */
function Planner({ allocation, holdings }: { allocation: Allocation; holdings: Holding[] }) {
  const [mode, setMode] = useState<"rebalance" | "deposit">("rebalance");
  const [amount, setAmount] = useState("");
  const deposit = Number(amount.replace(/[^\d]/g, "")) || 0;
  const plan = useMemo(
    () => (mode === "rebalance" ? rebalancePlan(allocation, holdings) : deposit > 0 ? depositPlan(allocation, holdings, deposit) : null),
    [mode, allocation, holdings, deposit],
  );

  return (
    <section className="mt-7 border-t border-edge-soft pt-5" aria-label="주문 계획">
      <div role="tablist" aria-label="계산" className="-ms-1 mb-3 flex gap-1">
        {(
          [
            ["rebalance", "주 단위로 맞추기"],
            ["deposit", "현금 넣기"],
          ] as const
        ).map(([key, label]) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={mode === key}
            onClick={() => setMode(key)}
            className={cn(
              "tap min-h-9 rounded-lg px-2.5 text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-8 sm:text-[12.5px]",
              mode === key ? "bg-glass-raised text-foreground" : "text-dim hover:text-foreground",
            )}
          >
            {label}
          </button>
        ))}
      </div>

      {mode === "deposit" ? (
        <label className="relative mb-3 block">
          <span className="mb-1.5 block text-[12px] text-dim">새로 넣을 현금</span>
          <input
            inputMode="numeric"
            value={deposit ? deposit.toLocaleString("ko-KR") : ""}
            onChange={(e) => setAmount(e.target.value)}
            placeholder="예: 1,000,000"
            className="tnum h-11 w-full rounded-lg border border-edge-soft bg-glass px-3 pe-8 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50 sm:h-10 sm:text-[14px]"
          />
          <span aria-hidden className="pointer-events-none absolute right-3 bottom-3 text-[13px] text-faint sm:bottom-2.5">
            원
          </span>
        </label>
      ) : null}

      {plan ? <PlanView plan={plan} deposit={mode === "deposit" ? deposit : 0} /> : (
        <p className="text-[12.5px] text-faint">금액을 적으면 어디에 얼마씩 사면 되는지 계산해요.</p>
      )}
    </section>
  );
}

function PlanView({ plan, deposit }: { plan: Plan; deposit: number }) {
  const buys = plan.orders.filter((o) => o.side === "buy");
  const sells = plan.orders.filter((o) => o.side === "sell");
  const nothing = plan.orders.length === 0 && plan.unpicked.length === 0;
  return (
    <div>
      {nothing ? (
        <p className="text-[13px] text-dim">
          {deposit ? "이 금액으로는 살 수 있는 단위가 없어요. 현금으로 두면 돼요." : "주 단위로는 지금이 가장 가까워요."}
        </p>
      ) : null}
      {sells.length > 0 ? <Orders title="팔기" orders={sells} /> : null}
      {buys.length > 0 ? <Orders title="사기" orders={buys} /> : null}
      {plan.unpicked.length > 0 ? (
        <ul className="mt-2 space-y-1">
          {plan.unpicked.map((u) => (
            <li key={u.bucket} className="flex items-center gap-2 rounded-lg border border-dashed border-edge px-3 py-2.5 text-[12.5px] text-dim">
              <Dot id={u.bucket} />
              {u.label}: 가진 종목이 없어 직접 골라야 해요
              <span className="tnum ms-auto text-foreground/90">{krw(u.amountKrw)}</span>
            </li>
          ))}
        </ul>
      ) : null}

      <p className="tnum mt-3 text-[12.5px] text-dim">
        {deposit
          ? `현금으로 남는 돈 ${krw(Math.max(0, plan.cashChangeKrw))}`
          : plan.cashChangeKrw >= 0
            ? `현금 ${krw(plan.cashChangeKrw)} 늘어남`
            : `현금 ${krw(-plan.cashChangeKrw)} 씀`}
      </p>

      <table className="mt-3 w-full text-[12px]">
        <thead>
          <tr className="text-[11px] text-faint">
            <th className="pb-1.5 text-left font-normal">자산</th>
            <th className="pb-1.5 text-right font-normal">지금</th>
            <th className="pb-1.5 text-right font-normal">주문 뒤</th>
            <th className="pb-1.5 text-right font-normal">목표</th>
          </tr>
        </thead>
        <tbody>
          {plan.buckets.map((b) => (
            <tr key={b.id} className="border-t border-edge-soft">
              <td className="py-1.5">
                <span className="flex items-center gap-2">
                  <Dot id={b.id} />
                  <span className="text-dim">{b.label}</span>
                </span>
              </td>
              <td className="tnum py-1.5 text-right text-faint">{pctOf(b.before)}</td>
              <td className="tnum py-1.5 text-right text-foreground/90">{pctOf(b.after)}</td>
              <td className="tnum py-1.5 text-right text-faint">{pctOf(b.target)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="mt-2 text-[11px] leading-relaxed text-faint">
        마지막으로 받은 가격 기준이에요. 수수료·환전 비용은 빠져 있고, 주식은 1주, 금현물은 1g, 코인은 금액 단위예요.
      </p>
    </div>
  );
}

const pctOf = (r: number) => `${(r * 100).toFixed(1)}%`;

function Orders({ title, orders }: { title: string; orders: Order[] }) {
  return (
    <div className="mt-2">
      <p className="mb-1 text-[11.5px] text-faint">{title}</p>
      <ul className="space-y-1">
        {orders.map((o, i) => (
          <li key={`${o.holding?.id}-${i}`} className="flex items-center gap-2 rounded-lg bg-glass px-3 py-2.5">
            <Dot id={o.bucket} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] text-foreground/90">
                {o.holding?.name || o.holding?.symbol}
                <span className="ms-1.5 text-[11.5px] text-faint">{o.holding?.symbol}</span>
              </span>
              <span className="tnum block text-[11.5px] text-faint">
                {o.units !== null && o.holding ? `${unitPrice(o.holding)} × ${o.units}${o.holding.kind === "gold" ? "g" : "주"}` : "금액으로 주문"}
              </span>
            </span>
            <span className="tnum shrink-0 text-right">
              {o.units !== null ? (
                <span className="block text-[13.5px] text-foreground">
                  {o.units}
                  {o.holding?.kind === "gold" ? "g" : "주"}
                </span>
              ) : null}
              <span className={cn("block", o.units !== null ? "text-[11.5px] text-dim" : "text-[13.5px] text-foreground")}>
                {o.units !== null ? "약 " : ""}
                {krw(o.amountKrw)}
              </span>
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** A share's price in its own currency: a US share is bought in dollars. */
function unitPrice(h: Holding): string {
  if (h.currency === "USD") return `$${h.price.toLocaleString("en-US", { maximumFractionDigits: 2 })}`;
  return krw(h.valueKrw / h.quantity);
}

function Dot({ id }: { id: Bucket }) {
  return (
    <span
      aria-hidden
      className="size-2 shrink-0 rounded-[2px]"
      style={{ background: HUE[id] }}
    />
  );
}

/**
 * Target on top, present underneath, one ribbon per bucket between them.
 * Drawn in a 100-wide viewBox stretched to the container, so the bars are
 * percentages with no layout maths; the labels are HTML on top so text is not
 * stretched with them.
 */
function Ribbons({ rows }: { rows: Drift[] }) {
  const GAP = 0.6;
  const spans = (key: "target" | "current") => {
    let at = 0;
    return rows.map((r) => {
      const width = r[key] * 100;
      const span = { x0: at, x1: at + width };
      at += width;
      return span;
    });
  };
  const top = spans("target");
  const bottom = spans("current");

  // Inset each slice by half the gap, but never past its own middle, so a
  // sliver stays a sliver instead of turning inside out.
  const inset = (s: { x0: number; x1: number }) => {
    const pad = Math.min(GAP / 2, (s.x1 - s.x0) / 2);
    return { x0: s.x0 + pad, x1: s.x1 - pad };
  };

  const BAR = 12;
  const DROP = 34;

  return (
    <div className="mt-5">
      <div className="mb-1.5 flex justify-between text-[11px] text-faint">
        <span>목표</span>
      </div>
      <div className="relative">
        <svg
          viewBox={`0 0 100 ${BAR * 2 + DROP}`}
          preserveAspectRatio="none"
          className="block h-[92px] w-full sm:h-[104px]"
          role="img"
          aria-label={rows
            .map((r) => `${r.label} 목표 ${pct(r.target)}, 지금 ${pct(r.current)}`)
            .join("; ")}
        >
          {rows.map((r, i) => {
            const t = inset(top[i]);
            const b = inset(bottom[i]);
            const hasTop = r.target > 0;
            const hasBottom = r.current > 0.0005;
            return (
              <g key={r.id} fill={HUE[r.id]}>
                {hasTop ? (
                  <rect x={t.x0} y={0} width={t.x1 - t.x0} height={BAR} />
                ) : null}
                <path
                  d={`M${t.x0} ${BAR} L${t.x1} ${BAR} L${b.x1} ${BAR + DROP} L${b.x0} ${BAR + DROP} Z`}
                  opacity={r.inBand ? 0.07 : 0.24}
                />
                {hasBottom ? (
                  <rect
                    x={b.x0}
                    y={BAR + DROP}
                    width={b.x1 - b.x0}
                    height={BAR}
                  />
                ) : null}
              </g>
            );
          })}
        </svg>

        {/* Percentages sit inside a slice only when it is wide enough to hold
            one; the table below has every number regardless. */}
        {(["target", "current"] as const).map((key) => {
          const spansFor = key === "target" ? top : bottom;
          return (
            <div
              key={key}
              aria-hidden
              className={cn(
                "pointer-events-none absolute inset-x-0 h-[22%]",
                key === "target" ? "top-0" : "bottom-0",
              )}
            >
              {rows.map((r, i) =>
                r[key] >= 0.09 ? (
                  <span
                    key={r.id}
                    className="tnum absolute top-1/2 -translate-y-1/2 ps-1.5 text-[10.5px] font-semibold text-black/70"
                    style={{ left: `${spansFor[i].x0}%` }}
                  >
                    {pct(r[key])}
                  </span>
                ) : null,
              )}
            </div>
          );
        })}
      </div>
      <div className="mt-1.5 text-[11px] text-faint">지금</div>
    </div>
  );
}
