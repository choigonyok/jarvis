"use client";

import { useEffect, useState } from "react";
import { Check, X } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Pager } from "@/components/ui/pager";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { type OrderRequest, type OrderResult, type PlacedOrder, type Standing, cancelStanding, sendOrders } from "@/lib/orders";
import { usePaged } from "@/lib/paging";
import { type Holding, krw, quantity } from "@/lib/portfolio";
import { cn } from "@/lib/utils";

/**
 * Placing orders from the 자산 screen.
 *
 * Money moves here, so every path ends on one sheet that names exactly what
 * will be sent - "코카콜라 1주 매수", "BTC ₩50,000 매도" - checked by
 * assets-svc first (a share not held, more than held, under Upbit's minimum
 * come back as reasons), and one bright button that sends that and nothing
 * else. What came back is shown in the same place: placed, refused and why,
 * or "앱에서 직접" for KRX gold, which this cannot order.
 */


/** "코카콜라 1주 매수", "BTC ₩50,000 매도", "금현물 2g 매수". */
export function orderLine(o: OrderRequest, names: Map<string, string>): string {
  const name = names.get(`${o.venue}:${o.symbol}`) ?? o.symbol;
  const verb = o.side === "buy" ? "매수" : "매도";
  if (o.units === null || o.venue === "upbit") return `${name} ${krw(o.amountKrw)} ${verb}`;
  return `${name} ${o.units}${o.venue === "gold" ? "g" : "주"} ${verb}`;
}

const statusText: Record<OrderResult["status"], string> = {
  placed: "주문함",
  failed: "안 됨",
  manual: "앱에서 직접",
  preview: "",
};

/** The orders a confirm will send, then what became of each. */
function OrderList({ results, names }: { results: OrderResult[]; names: Map<string, string> }) {
  return (
    <ul className="divide-y divide-edge-soft">
      {results.map((r, i) => (
        <li key={`${r.venue}:${r.symbol}:${r.side}:${i}`} className="flex items-start gap-3 py-2.5">
          <span aria-hidden className="mt-[5px] flex size-3.5 shrink-0 items-center justify-center">
            {r.status === "placed" ? (
              <Check className="size-3.5 text-approve" />
            ) : r.status === "failed" ? (
              <X className="size-3.5 text-reject" />
            ) : (
              <span className={cn("size-[6px] rounded-full", r.side === "sell" ? "bg-foreground/50" : "bg-foreground/80")} />
            )}
          </span>
          <span className="min-w-0 flex-1">
            <span className="flex items-baseline justify-between gap-3">
              <span className="text-[14px] text-foreground/90">{orderLine(r, names)}</span>
              <span className="tnum shrink-0 text-[12px] text-faint">
                {statusText[r.status] || (r.units !== null && r.venue !== "upbit" ? `약 ${krw(r.amountKrw)}` : "")}
              </span>
            </span>
            {r.message ? (
              <span className={cn("mt-0.5 block text-[12px] leading-relaxed", r.status === "failed" ? "text-reject/90" : "text-faint")}>
                {r.message}
              </span>
            ) : null}
          </span>
        </li>
      ))}
    </ul>
  );
}

/**
 * Several orders at once - the rebalance plan, or what new cash buys. Opens
 * on assets-svc's check of exactly these; the button sends what was checked
 * and could go (refused and by-hand lines stay as they are).
 */
export function PlanSheet({
  open,
  onOpenChange,
  title,
  reason,
  load,
  holdings,
  onPlaced,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  reason: string;
  /** The checked orders (a dry run). */
  load: () => Promise<{ results: OrderResult[]; planId?: string | null }>;
  holdings: Holding[];
  onPlaced: () => void;
}) {
  const names = new Map(holdings.map((h) => [`${h.venue}:${h.symbol}`, h.name]));
  const [checked, setChecked] = useState<{ results: OrderResult[]; planId?: string | null } | null>(null);
  const [placed, setPlaced] = useState<OrderResult[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    let alive = true;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a fresh sheet each time it opens
    setChecked(null);
    setPlaced(null);
    setError(null);
    load()
      .then((c) => alive && setChecked(c))
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- checked once per opening
  }, [open]);

  const ready = checked?.results.filter((r) => r.status === "preview") ?? [];
  const send = async () => {
    setBusy(true);
    setError(null);
    try {
      const out = await sendOrders(ready, { reason, planId: checked?.planId ?? undefined });
      setPlaced([...out.results, ...(checked?.results.filter((r) => r.status !== "preview") ?? [])]);
      onPlaced();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const shown = placed ?? checked?.results ?? [];
  const sells = ready.filter((r) => r.side === "sell").length;
  return (
    <Panel
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={placed ? "보낸 결과예요. 체결은 잠시 뒤 잔고에 반영돼요." : "아래 주문을 그대로 보내요. 파는 것부터 내요."}
    >
      {!checked && !error ? <p className="py-6 text-center text-[13px] text-faint">주문을 확인하는 중…</p> : null}
      {shown.length ? <OrderList results={shown} names={names} /> : null}
      {checked && shown.length === 0 ? <p className="py-6 text-center text-[13px] text-faint">지금 낼 주문이 없어요.</p> : null}
      {error ? <p className="mt-2 text-[12.5px] text-reject/90">{error}</p> : null}
      {!placed && ready.length > 0 ? (
        <div className="mt-3 space-y-2">
          <p className="text-[11.5px] leading-relaxed text-faint">
            미국 주식은 지금가보다 0.3% 유리하게 지정가로 내서 바로 체결되게 해요. 업비트는 시장가예요. 원금 대비 수익률에는 들어가지
            않아요 - 원금은 입출금만이에요.
          </p>
          <Button variant="raised" size="touch" className="w-full" disabled={busy} onClick={() => void send()}>
            {busy ? "보내는 중…" : `주문 ${ready.length}건 보내기${sells ? ` (매도 ${sells}건 먼저)` : ""}`}
          </Button>
        </div>
      ) : null}
    </Panel>
  );
}

/**
 * One holding: buy more or sell some. Shares by the share, a coin by the won.
 * The button says the order in full before it goes.
 */
export function OrderSheet({
  holding,
  usdKrw,
  onOpenChange,
  onPlaced,
}: {
  holding: Holding | null;
  usdKrw: number;
  onOpenChange: (open: boolean) => void;
  onPlaced: () => void;
}) {
  const [side, setSide] = useState<"buy" | "sell">("buy");
  const [amount, setAmount] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<OrderResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a fresh form for each holding
    setSide("buy");
    setAmount("");
    setResult(null);
    setError(null);
  }, [holding?.id]);

  if (!holding) return null;
  const coin = holding.kind === "coin";
  const gold = holding.kind === "gold";
  const unitKrw = holding.currency === "USD" ? holding.price * usdKrw : holding.price;
  const n = Number(amount.replace(/[^\d]/g, "")) || 0;
  const amountKrw = coin ? n : n * unitKrw;
  const held = Math.floor(holding.quantity + 1e-6);
  const req: OrderRequest = {
    venue: holding.venue,
    symbol: holding.symbol,
    side,
    units: coin ? null : n,
    amountKrw: Math.round(amountKrw),
  };
  const names = new Map([[`${holding.venue}:${holding.symbol}`, holding.name]]);
  const line = orderLine(req, names);
  const tooMuch = side === "sell" && (coin ? amountKrw > holding.valueKrw * 1.02 : n > held);
  const tooSmall = coin && n > 0 && n < 5_000;

  const send = async () => {
    setBusy(true);
    setError(null);
    try {
      const out = await sendOrders([req], { reason: "manual" });
      setResult(out.results[0] ?? null);
      onPlaced();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const price = holding.currency === "USD" ? `$${holding.price.toFixed(2)} (${krw(unitKrw)})` : krw(holding.price);
  return (
    <Panel
      open
      onOpenChange={onOpenChange}
      title={`${holding.name} 주문`}
      description={`보유 ${quantity(holding.quantity)}${coin ? "" : gold ? "g" : "주"} · 지금 ${price}`}
    >
      {result ? (
        <OrderList results={[result]} names={names} />
      ) : (
        <div className="space-y-3">
          <ToggleGroup
            type="single"
            aria-label="매수 또는 매도"
            variant="segment"
            spacing={1}
            value={side}
            onValueChange={(v) => {
              if (v) setSide(v as "buy" | "sell");
            }}
            className="grid w-full grid-cols-2 rounded-lg bg-well p-1"
          >
            {(["buy", "sell"] as const).map((s) => (
              <ToggleGroupItem
                key={s}
                value={s}
                disabled={s === "sell" && holding.quantity <= 0}
                className="tap text-[13.5px] text-dim"
              >
                {s === "buy" ? "매수" : "매도"}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <Field>
            <FieldLabel htmlFor={`order-${holding.id}`} className="text-faint">
              {coin ? "금액 (원)" : gold ? "그램" : "주 수"}
            </FieldLabel>
            <Input
              id={`order-${holding.id}`}
              surface="glass"
              className="tnum"
              inputMode="numeric"
              value={n ? n.toLocaleString("ko-KR") : ""}
              onChange={(e) => setAmount(e.target.value)}
              placeholder={coin ? "예: 50,000" : "예: 1"}
            />
          </Field>
          <div className="flex flex-wrap gap-1.5">
            {(coin ? [10_000, 50_000, 100_000] : [1, 2, 5]).map((v) => (
              <button
                key={v}
                type="button"
                onClick={() => setAmount(String(v))}
                className="tap rounded-full bg-glass px-3 text-[12.5px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                {coin ? krw(v) : `${v}${gold ? "g" : "주"}`}
              </button>
            ))}
            {side === "sell" && holding.quantity > 0 ? (
              <button
                type="button"
                onClick={() => setAmount(String(coin ? Math.floor(holding.valueKrw) : held))}
                className="tap rounded-full bg-glass px-3 text-[12.5px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                전부
              </button>
            ) : null}
          </div>
          <p className="tnum text-[12px] leading-relaxed text-faint">
            {n > 0 ? `약 ${krw(amountKrw)}` : ""}
            {tooMuch ? <span className="text-reject/90"> · 갖고 있는 것보다 많아요</span> : null}
            {tooSmall ? <span className="text-reject/90"> · 업비트는 ₩5,000부터예요</span> : null}
            {gold ? " · 금현물은 한국투자 앱에서 직접 주문해야 해요" : null}
          </p>
          {error ? <p className="text-[12.5px] text-reject/90">{error}</p> : null}
          <Button variant="raised" size="touch" className="w-full" disabled={busy || n <= 0 || tooMuch || tooSmall} onClick={() => void send()}>
            {busy ? "보내는 중…" : n > 0 ? `${line} 주문 보내기` : "수량을 적어 주세요"}
          </Button>
        </div>
      )}
    </Panel>
  );
}

/** "10/18 01:12". */
const when = (iso: string) =>
  new Date(iso).toLocaleString("ko-KR", { timeZone: "Asia/Seoul", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false });

/**
 * Standing orders waiting for their price - "내려오면 사" - each placed again
 * every session until it fills. Cancelling also cancels today's order at the
 * broker.
 */
function Waiting({ standing, names, onChanged }: { standing: Standing[]; names: Map<string, string>; onChanged: () => void }) {
  const [busy, setBusy] = useState<number | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const active = standing.filter((o) => o.status === "active");
  const done = standing.filter((o) => o.status !== "active").slice(0, 3);
  if (active.length === 0 && done.length === 0) return null;
  return (
    <section aria-label="대기 주문" className="mb-3">
      <p className="mb-1 text-[11.5px] text-faint">대기 주문 · 체결될 때까지 거래일마다 다시 내요</p>
      <ul className="divide-y divide-edge-soft">
        {[...active, ...done].map((o) => {
          const name = names.get(`kis:${o.symbol}`) ?? o.symbol;
          return (
            <li key={o.id} className="flex items-start gap-3 py-2 text-[12.5px]">
              <span className="min-w-0 flex-1">
                <span className={o.status === "active" ? "text-foreground/90" : "text-faint"}>
                  {name} {o.units}주 ${o.limitPrice.toFixed(2)} {o.side === "buy" ? "이하에 매수" : "이상에 매도"}
                </span>
                <span className="block text-[11.5px] leading-relaxed text-faint">
                  {o.status === "filled" ? "체결됐어요" : o.status === "cancelled" ? "취소됨" : o.status === "expired" ? "기한이 지났어요" : o.message}
                </span>
              </span>
              {o.status === "active" ? (
                // One press used to cancel at once - and today's order at the
                // broker with it. Asked first, as every irreversible step is.
                <AlertDialog>
                  <AlertDialogTrigger asChild>
                    <Button variant="discard" size="inline" disabled={busy === o.id} className="tap shrink-0 px-2 text-[12px]">
                      취소
                    </Button>
                  </AlertDialogTrigger>
                  <AlertDialogContent>
                    <AlertDialogHeader>
                      <AlertDialogTitle>대기 주문을 취소할까요?</AlertDialogTitle>
                      <AlertDialogDescription>
                        {name} {o.units}주 ${o.limitPrice.toFixed(2)} {o.side === "buy" ? "이하 매수" : "이상 매도"}를 더는 내지 않고, 오늘 증권사에 낸
                        주문도 함께 취소해요. 되돌릴 수 없어요.
                      </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel>두기</AlertDialogCancel>
                      <AlertDialogAction
                        onClick={async () => {
                          setBusy(o.id);
                          try {
                            setNote(await cancelStanding(o.id));
                            onChanged();
                          } catch (e) {
                            setNote((e as Error).message);
                          } finally {
                            setBusy(null);
                          }
                        }}
                      >
                        주문 취소
                      </AlertDialogAction>
                    </AlertDialogFooter>
                  </AlertDialogContent>
                </AlertDialog>
              ) : null}
            </li>
          );
        })}
      </ul>
      {note ? <p className="mt-1 text-[11.5px] text-dim">{note}</p> : null}
    </section>
  );
}

/** The record: standing orders waiting, then what was sent, when, and how it went. */
export function OrderRecord({
  orders,
  holdings,
  standing = [],
  onChanged = () => {},
}: {
  orders: PlacedOrder[];
  holdings: Holding[];
  standing?: Standing[];
  onChanged?: () => void;
}) {
  const names = new Map(holdings.map((h) => [`${h.venue}:${h.symbol}`, h.name]));
  const paged = usePaged(orders);
  if (orders.length === 0 && standing.length === 0) return <p className="text-[12.5px] text-faint">아직 낸 주문이 없어요.</p>;
  return (
    <>
    <Waiting standing={standing} names={names} onChanged={onChanged} />
    <div id={paged.anchor} className="scroll-mt-4" />
    <ul className="divide-y divide-edge-soft">
      {paged.items.map((o) => (
        <li key={o.id} className="flex items-baseline gap-3 py-2 text-[12.5px]">
          <span className="tnum w-[5.5rem] shrink-0 text-faint">{when(o.createdAt)}</span>
          <span className="min-w-0 flex-1">
            <span className="text-foreground/90">{orderLine(o, names)}</span>
            {o.reason === "rebalance" ? <span className="ms-1.5 text-[11px] text-faint">리밸런싱</span> : null}
            {o.reason === "standing" ? <span className="ms-1.5 text-[11px] text-faint">대기 주문</span> : null}
            {o.status !== "placed" ? (
              <span className={cn("block text-[11.5px]", o.status === "failed" ? "text-reject/90" : "text-faint")}>{o.message}</span>
            ) : null}
          </span>
          <span className={cn("shrink-0 text-[11.5px]", o.status === "failed" ? "text-reject/90" : "text-faint")}>
            {statusText[o.status]}
          </span>
        </li>
      ))}
    </ul>
    <Pager {...paged} />
    </>
  );
}
