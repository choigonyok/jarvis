"use client";

import {
  type Holding,
  type Realized,
  type TaxBasket,
  krw,
  percent,
  signedKrw,
  taxFor,
} from "@/lib/portfolio";
import type { Order } from "@/lib/trade-plan";
import { cn } from "@/lib/utils";

/**
 * What has already been taken off the table.
 *
 * A holding's row shows only the units still held, so a partial sale moves
 * its profit out of sight; a holding sold out disappears altogether. This
 * keeps both: per holding since the principal's start, and per calendar year
 * against the capital-gains deduction, which is what decides whether the next
 * sale costs tax.
 */
export function RealizedSection({ realized }: { realized: Realized }) {
  const { lines, totalKrw, tax, since, missing } = realized;
  const up = totalKrw >= 0;
  const [, m, d] = since.split("-").map(Number);

  return (
    <div>
      <p className="text-[15px] leading-snug text-foreground/90">
        {lines.length === 0 ? (
          `${m}월 ${d}일 이후 판 것이 없어요`
        ) : (
          <>
            {m}월 {d}일 이후 팔아서{" "}
            <span className={cn("tnum font-semibold", up ? "text-approve/85" : "text-reject/85")}>
              {signedKrw(totalKrw)}
            </span>
          </>
        )}
      </p>
      {missing.length > 0 ? (
        <p className="mt-1 text-[12px] text-dim">
          {missing.map((v) => (v === "kis" ? "한국투자증권" : "업비트")).join(", ")} 매도 내역을 읽지 못해 빠져 있어요.
        </p>
      ) : null}

      {lines.length > 0 ? (
        <ul className="mt-3 space-y-0.5">
          {lines.map((l) => {
            const gain = l.profitKrw >= 0;
            const rate = l.costKrw > 0 ? l.profitKrw / l.costKrw : 0;
            return (
              <li key={l.id} className="flex items-baseline gap-3 rounded-lg px-2 py-2">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[14px] text-foreground/90">
                    {l.name}
                    {!l.held ? <span className="ms-2 text-[11.5px] text-faint">다 팖</span> : null}
                  </p>
                  <p className="tnum mt-0.5 text-[11.5px] text-faint">
                    {l.sales}번 · 판 금액 {krw(l.proceedsKrw)}
                  </p>
                </div>
                <div className="shrink-0 text-right">
                  <p className={cn("tnum text-[14px]", gain ? "text-approve/85" : "text-reject/85")}>
                    {signedKrw(l.profitKrw)}
                  </p>
                  <p className="tnum mt-0.5 text-[11.5px] text-faint">
                    {percent(rate)}
                  </p>
                </div>
              </li>
            );
          })}
        </ul>
      ) : null}

      <div className="mt-5 space-y-5">
        {tax.baskets.map((b) => (
          <TaxGauge key={b.kind} basket={b} year={tax.year} />
        ))}
      </div>

    </div>
  );
}

const BASKET_LABEL: Record<TaxBasket["kind"], string> = { overseas: "해외주식", coin: "코인" };

/**
 * A year's net gain against the 2.5M deduction, as one hairline track: the
 * deduction is the track, what has been realized fills it, and `addKrw` (a
 * sale being planned) continues the fill in a lighter tone. Whatever runs past
 * the end of the track is the taxable part, and the tax is said in words.
 */
export function TaxGauge({ basket, year, addKrw = 0 }: { basket: TaxBasket; year: number; addKrw?: number }) {
  const label = BASKET_LABEL[basket.kind];
  if (!basket.inForce) {
    return (
      <p className="text-[12px] leading-relaxed text-faint">
        {label} 양도세는 아직 걷지 않아요. 올해 {label} 손익 {signedKrw(basket.gainKrw + addKrw)}
        {basket.kind === "coin" ? ", 가상자산 과세는 2027년부터 시행 예정이에요." : "."}
      </p>
    );
  }

  const before = Math.max(0, basket.gainKrw);
  const after = Math.max(0, basket.gainKrw + addKrw);
  const deduction = basket.deductionKrw;
  // The track is the deduction, unless the gain already runs past it - then
  // it grows so the overflow stays visible.
  const scale = Math.max(deduction, after, before) * 1.0001;
  const w = (v: number) => `${(Math.min(v, scale) / scale) * 100}%`;
  const taxBefore = taxFor(basket.gainKrw, basket);
  const taxAfter = taxFor(basket.gainKrw + addKrw, basket);
  const left = Math.max(0, deduction - after);
  const planning = addKrw !== 0;

  return (
    <div>
      <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
        <span className="text-dim">
          {year}년 {label} 실현손익 <span className="tnum text-foreground/90">{signedKrw(basket.gainKrw)}</span>
        </span>
        <span className="tnum shrink-0 text-faint">공제 {krw(deduction)}</span>
      </div>

      <div
        className="relative mt-2 h-1.5 overflow-hidden rounded-full bg-well"
        role="img"
        aria-label={`공제 ${krw(deduction)} 중 ${krw(Math.min(before, deduction))} 사용${planning ? `, 이번 매도로 ${krw(Math.min(after, deduction))}` : ""}`}
      >
        {planning && after > before ? (
          <span className="absolute inset-y-0 left-0 rounded-full bg-foreground/30" style={{ width: w(after) }} />
        ) : null}
        <span className="absolute inset-y-0 left-0 rounded-full bg-foreground/75" style={{ width: w(Math.min(before, after)) }} />
        {scale > deduction ? (
          // Where the deduction ends, when the gain has gone past it.
          <span aria-hidden className="absolute inset-y-0 w-px bg-background" style={{ left: w(deduction) }} />
        ) : null}
      </div>

      <p className="tnum mt-1.5 text-[12px] leading-relaxed text-dim">
        {planning ? (
          taxAfter > taxBefore ? (
            <>이번 매도로 양도세가 약 <span className="text-foreground">{krw(taxAfter - taxBefore)}</span> 늘어요 (올해 합계 약 {krw(taxAfter)})</>
          ) : (
            <>이번 매도까지 공제 안이라 양도세 없음 · 남는 공제 {krw(left)}</>
          )
        ) : taxBefore > 0 ? (
          <>내년 5월에 낼 양도세 약 <span className="text-foreground">{krw(taxBefore)}</span></>
        ) : (
          <>공제 안이라 양도세 없음 · 남은 공제 {krw(left)}</>
        )}
      </p>
    </div>
  );
}

/**
 * What a planned sale would realize, per tax basket, from the holding's own
 * average cost. A domestic share and KRX gold are not taxed and count nowhere.
 */
export function plannedGains(orders: Order[]): Record<TaxBasket["kind"], number> {
  const out = { overseas: 0, coin: 0 };
  for (const o of orders) {
    const h: Holding | null = o.holding;
    if (o.side !== "sell" || !h || h.quantity <= 0 || h.valueKrw <= 0) continue;
    const gain =
      o.units !== null
        ? o.units * ((h.valueKrw - h.costKrw) / h.quantity)
        : o.amountKrw * (1 - h.costKrw / h.valueKrw);
    if (h.kind === "coin") out.coin += gain;
    else if (h.kind === "stock" && h.currency === "USD") out.overseas += gain;
  }
  return out;
}
