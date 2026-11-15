"use client";

import { useEffect, useState } from "react";
import { keepValue, keptValue } from "@/lib/keep";
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
import { AsOf } from "@/components/shell/as-of";
import { Failed } from "@/components/shell/failed";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Pager } from "@/components/ui/pager";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  type OrderRequest,
  type OrderResult,
  type PlacedOrder,
  type Standing,
  Unanswered,
  cancelStanding,
  findSent,
  sendOrders,
} from "@/lib/orders";
import { usePaged } from "@/lib/paging";
import { type Holding, krw, quantity } from "@/lib/portfolio";
import { SAY, failureText } from "@/lib/say";
import { clock, dateTime } from "@/lib/when";
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

/** What became of one order: as assets-svc answered it, or as its record has it. */
type Outcome = OrderRequest & Pick<OrderResult, "status" | "message">;

/** The orders a confirm will send, then what became of each. */
function OrderList({ results, names }: { results: Outcome[]; names: Map<string, string> }) {
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
              <span className="text-body text-ink">{orderLine(r, names)}</span>
              <span className="tnum shrink-0 text-aside text-faint">
                {statusText[r.status] || (r.units !== null && r.venue !== "upbit" ? `약 ${krw(r.amountKrw)}` : "")}
              </span>
            </span>
            {r.message ? (
              <span className={cn("mt-0.5 block text-aside leading-relaxed", r.status === "failed" ? "text-reject" : "text-faint")}>
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
 * A send's own planId, by which the record says what came of it when no
 * answer does - each press its own. randomUUID is there only on https and
 * localhost; anywhere else the press must not fail on it.
 */
function newPlanId(): string {
  const id = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(16).slice(2) + Date.now().toString(16);
  return id.replace(/-/g, "").slice(0, 8);
}

/**
 * A send that got no answer, not in the record either once it has had time to
 * be. It may yet be on its way - a slow broker outlasts the look - so the same
 * order sent again goes under the same planId, and assets-svc, which sends a
 * planId once, answers it with what came of the first.
 */
const NOT_IN_RECORD = "주문 기록에 없어요. 주문이 들어가지 않은 것 같아요. 같은 주문을 다시 보내도 두 번 들어가지 않아요.";

/**
 * In the send button's place while a send that got no answer back (lib/orders.ts
 * Unanswered) is looked for in the record: it may be in, and the same button
 * pressed again would send it twice.
 */
function Looking({ failed, onRetry }: { failed: boolean; onRetry: () => unknown }) {
  return failed ? (
    <Failed quiet onRetry={onRetry}>
      주문 기록을 불러오지 못했어요. 들어갔는지 확인한 뒤에 다시 보낼 수 있어요.
    </Failed>
  ) : (
    <p role="status" className="text-aside text-faint">
      주문 기록을 확인하는 중…
    </p>
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
  const [placed, setPlaced] = useState<Outcome[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Sent with no answer back, and looked for in the record by its planId.
  const [looking, setLooking] = useState<{ planId: string; failed: boolean } | null>(null);
  // The planId of a send that got no answer and was not found in the record:
  // these same orders pressed again go under it (NOT_IN_RECORD).
  const [again, setAgain] = useState<string | null>(null);
  // A check that did not come is asked again from here (다시 시도): it had
  // taken closing the sheet and opening it again.
  const [asked, setAsked] = useState(0);

  useEffect(() => {
    if (!open) return;
    let alive = true;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a fresh sheet each time it opens
    setChecked(null);
    setPlaced(null);
    setError(null);
    setAgain(null);
    load()
      .then((c) => alive && setChecked(c))
      .catch((e: unknown) => alive && setError(failureText(e, SAY.couldNotLoad)));
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- checked once per opening, and again when asked
  }, [open, asked]);

  const ready = checked?.results.filter((r) => r.status === "preview") ?? [];
  const unsent = checked?.results.filter((r) => r.status !== "preview") ?? [];
  const look = async (planId: string) => {
    setLooking({ planId, failed: false });
    const rows = await findSent(planId);
    if (!rows) return setLooking({ planId, failed: true });
    setLooking(null);
    if (rows.length) {
      setError(null);
      setPlaced([...rows, ...unsent]);
      onPlaced();
    } else {
      setError(NOT_IN_RECORD);
      setAgain(planId);
    }
  };
  const send = async () => {
    // Its own planId, by which the record says what came of it when no answer
    // does - the one of the send that got none, when these same orders go again.
    const planId = again ?? newPlanId();
    setBusy(true);
    setError(null);
    try {
      const out = await sendOrders(ready, { reason, planId });
      setAgain(null);
      setPlaced([...out.results, ...unsent]);
      onPlaced();
    } catch (e) {
      setError(failureText(e, "주문을 보내지 못했어요."));
      if (e instanceof Unanswered) void look(planId);
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
      {!checked && !error ? <p className="py-6 text-center text-ui text-faint">주문을 확인하는 중…</p> : null}
      {shown.length ? <OrderList results={shown} names={names} /> : null}
      {checked && shown.length === 0 ? <p className="py-6 text-center text-ui text-faint">지금 낼 주문이 없어요.</p> : null}
      {error && !checked ? (
        <Failed quiet onRetry={() => setAsked((n) => n + 1)}>
          {error}
        </Failed>
      ) : error ? (
        <p className="mt-2 text-aside text-reject">{error}</p>
      ) : null}
      {!placed && ready.length > 0 ? (
        <div className="mt-3 space-y-2">
          <p className="text-meta leading-relaxed text-faint">
            미국 주식은 지금가보다 0.3% 유리하게 지정가로 내서 바로 체결되게 해요. 업비트는 시장가예요. 원금 대비 수익률에는 들어가지
            않아요 - 원금은 입출금만이에요.
          </p>
          {looking ? (
            <Looking failed={looking.failed} onRetry={() => look(looking.planId)} />
          ) : (
            <Button variant="raised" size="touch" className="w-full" disabled={busy} onClick={() => void send()}>
              {busy ? "보내는 중…" : `주문 ${ready.length}건 보내기${sells ? ` (매도 ${sells}건 먼저)` : ""}`}
            </Button>
          )}
        </div>
      ) : null}
    </Panel>
  );
}

/**
 * One holding's sheet while the app is open (lib/keep.ts): the order half
 * typed - with a planId, sent and its answer not here - or what came of it.
 */
type Kept = { side: "buy" | "sell"; amount: string; planId?: string; again?: string } | { sent: Outcome };

/**
 * One holding: buy more or sell some. Shares by the share, a coin by the won.
 * The button says the order in full before it goes.
 */
export function OrderSheet({
  holding,
  at,
  usdKrw,
  onOpenChange,
  onPlaced,
}: {
  holding: Holding | null;
  /** When the holding's numbers were read. */
  at?: string;
  usdKrw: number;
  onOpenChange: (open: boolean) => void;
  onPlaced: () => void;
}) {
  const [side, setSide] = useState<"buy" | "sell">("buy");
  const [amount, setAmount] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Outcome | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Sent with no answer back, and looked for in the record by its planId.
  const [looking, setLooking] = useState<{ planId: string; failed: boolean } | null>(null);
  // A send that got no answer and was not found in the record: the same order
  // pressed again goes under its planId (NOT_IN_RECORD); another one does not.
  const [again, setAgain] = useState<{ planId: string; side: "buy" | "sell"; amount: string } | null>(null);

  const look = async (id: string, planId: string, draft: { side: "buy" | "sell"; amount: string }) => {
    setLooking({ planId, failed: false });
    const rows = await findSent(planId);
    if (!rows) return setLooking({ planId, failed: true });
    setLooking(null);
    if (rows[0]) {
      setError(null);
      setResult(rows[0]);
      setAgain(null);
      keepValue(`order:${id}`, { sent: rows[0] });
      onPlaced();
    } else {
      setError(NOT_IN_RECORD);
      setAgain({ planId, ...draft });
      keepValue(`order:${id}`, { ...draft, again: planId });
    }
  };

  // A fresh form for each holding - but one half typed is kept for it while
  // the app is open, so a tab switch does not lose the amount (lib/keep.ts).
  // Once sent it is no longer half typed: what came of it is kept in its
  // place, and coming back finds that rather than the same order ready to go
  // again - or, left while it was out, finds it looked for in the record.
  const holdingId = holding?.id;
  useEffect(() => {
    const kept = holdingId ? keptValue<Kept>(`order:${holdingId}`) : undefined;
    const draft = kept && "side" in kept ? kept : undefined;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the form of this holding
    setSide(draft?.side ?? "buy");
    setAmount(draft?.amount ?? "");
    setResult(kept && "sent" in kept ? kept.sent : null);
    setError(null);
    setAgain(draft?.again ? { planId: draft.again, side: draft.side, amount: draft.amount } : null);
    if (holdingId && draft?.planId) void look(holdingId, draft.planId, { side: draft.side, amount: draft.amount });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per holding; the sheet is keyed by it (assets.tsx)
  }, [holdingId]);
  // Kept as it is typed, not from an effect: one that ran as the sheet came
  // back would write the empty form over what was kept.
  const edit = (next: { side?: "buy" | "sell"; amount?: string }) => {
    const draft = { side, amount, ...next };
    setSide(draft.side);
    setAmount(draft.amount);
    if (holding) keepValue(`order:${holding.id}`, draft.amount ? { ...draft, again: again?.planId } : undefined);
  };

  if (!holding) return null;
  const coin = holding.kind === "coin";
  const gold = holding.kind === "gold";
  // Ordered by hand in the 한국투자 app, as assets-svc answers them (manual):
  // KRX gold, and a share in won. No send button promises what it cannot do -
  // "금현물 2g 매수 주문 보내기" had sent nothing.
  const byHand = holding.venue === "gold" || (holding.venue === "kis" && holding.currency !== "USD");
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
    // Its own planId, by which the record says what came of it when no answer
    // does - each new order its own, and the one of the send that got none
    // when the very same order goes again (assets-svc sends a planId once).
    const same = again && again.side === side && again.amount === amount;
    const planId = same ? again.planId : newPlanId();
    const draft = { side, amount };
    setBusy(true);
    setError(null);
    keepValue(`order:${holding.id}`, { ...draft, planId });
    try {
      const out = await sendOrders([req], { reason: "manual", planId });
      const sent = out.results[0] ?? null;
      setAgain(null);
      setResult(sent);
      setAmount("");
      keepValue(`order:${holding.id}`, sent ? { sent } : undefined);
      onPlaced();
    } catch (e) {
      setError(failureText(e, "주문을 보내지 못했어요."));
      if (e instanceof Unanswered) void look(holding.id, planId, draft);
      // Turned down: nothing went, and the form is half typed again - still
      // under the planId of a send that got no answer, if there was one.
      else keepValue(`order:${holding.id}`, { ...draft, again: again?.planId });
    } finally {
      setBusy(false);
    }
  };

  const price = holding.currency === "USD" ? `$${holding.price.toFixed(2)} (${krw(unitKrw)})` : krw(holding.price);
  return (
    <Panel
      open
      onOpenChange={(open) => {
        // Closed on what was sent: the next opening is a fresh form.
        if (!open && result) keepValue(`order:${holding.id}`, undefined);
        onOpenChange(open);
      }}
      title={`${holding.name} 주문`}
      // The price is as of a read, and says which; once placed, what is held
      // is about to change, and the line says that instead.
      description={
        result
          ? result.status === "placed"
            ? "보낸 결과예요. 체결은 잠시 뒤 잔고에 반영돼요."
            : "보낸 결과예요."
          : `보유 ${quantity(holding.quantity)}${coin ? "" : gold ? "g" : "주"} · ${price}${at ? ` · ${clock(at)} 기준` : ""}`
      }
    >
      {result ? (
        <>
          <OrderList results={[result]} names={names} />
          {/* Turned down, so nothing went: the form comes back with what was
              sent, to be fixed - it had taken closing the sheet and tapping
              the holding again, to type it all from the start. */}
          {result.status === "failed" ? (
            <Button
              variant="quiet"
              size="touch-sm"
              className="-ms-3 mt-1"
              onClick={() => {
                setResult(null);
                setError(null);
                edit({ side: result.side, amount: String(result.units ?? result.amountKrw) });
              }}
            >
              고쳐서 다시
            </Button>
          ) : null}
        </>
      ) : (
        <div className="space-y-3">
          {byHand ? (
            <p className="text-ui leading-relaxed text-ink">
              {gold ? "금현물은" : "국내 주식은"} 여기서 보낼 수 없어요. 한국투자 앱에서 직접 주문해 주세요.
            </p>
          ) : null}
          {/* Not changed while it is out, or while whether it went is not
              known: what was sent stays in view, and stays what is kept. */}
          <fieldset disabled={busy || looking !== null} className="min-w-0 space-y-3">
            <ToggleGroup
              type="single"
              aria-label="매수 또는 매도"
              variant="segment"
              spacing={1}
              value={side}
              onValueChange={(v) => {
                if (v) edit({ side: v as "buy" | "sell" });
              }}
              className="grid w-full grid-cols-2 rounded-lg bg-well p-1"
            >
              {(["buy", "sell"] as const).map((s) => (
                <ToggleGroupItem
                  key={s}
                  value={s}
                  disabled={s === "sell" && holding.quantity <= 0}
                  className="tap text-ui text-dim"
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
                onChange={(e) => edit({ amount: e.target.value })}
                placeholder={coin ? "예: 50,000" : "예: 1"}
              />
            </Field>
            <div className="flex flex-wrap gap-1.5">
              {(coin ? [10_000, 50_000, 100_000] : [1, 2, 5]).map((v) => (
                <button
                  key={v}
                  type="button"
                  onClick={() => edit({ amount: String(v) })}
                  className="tap rounded-full bg-glass px-3 text-aside text-dim hover:text-foreground focus-ring disabled:opacity-40"
                >
                  {coin ? krw(v) : `${v}${gold ? "g" : "주"}`}
                </button>
              ))}
              {side === "sell" && holding.quantity > 0 ? (
                <button
                  type="button"
                  onClick={() => edit({ amount: String(coin ? Math.floor(holding.valueKrw) : held) })}
                  className="tap rounded-full bg-glass px-3 text-aside text-dim hover:text-foreground focus-ring disabled:opacity-40"
                >
                  전부
                </button>
              ) : null}
            </div>
            <p className="tnum text-aside leading-relaxed text-faint">
              {n > 0 ? `약 ${krw(amountKrw)}` : ""}
              {tooMuch ? <span className="text-reject"> · 갖고 있는 것보다 많아요</span> : null}
              {tooSmall ? <span className="text-reject"> · 업비트는 ₩5,000부터예요</span> : null}
            </p>
          </fieldset>
          {error ? <p className="text-aside text-reject">{error}</p> : null}
          {looking ? (
            <Looking failed={looking.failed} onRetry={() => look(holding.id, looking.planId, { side, amount })} />
          ) : byHand ? null : (
            <Button variant="raised" size="touch" className="w-full" disabled={busy || n <= 0 || tooMuch || tooSmall} onClick={() => void send()}>
              {busy ? "보내는 중…" : n > 0 ? `${line} 주문 보내기` : "수량을 적어 주세요"}
            </Button>
          )}
        </div>
      )}
    </Panel>
  );
}

/** "오늘 01:12", "10월 18일 01:12" (lib/when.ts). */
const when = (iso: string) => dateTime(iso);

/**
 * Standing orders waiting for their price - "내려오면 사" - each placed again
 * every session until it fills. Cancelling also cancels today's order at the
 * broker.
 */
function Waiting({ standing, names, onChanged }: { standing: Standing[]; names: Map<string, string>; onChanged: () => void }) {
  const [busy, setBusy] = useState<number | null>(null);
  // What came of a cancel, said under the order it was pressed on - red when
  // it did not go. Grey at the foot of the list, a failure had read as done.
  const [note, setNote] = useState<{ id: number; text: string; failed: boolean } | null>(null);
  const active = standing.filter((o) => o.status === "active");
  const done = standing.filter((o) => o.status !== "active").slice(0, 3);
  const rows = [...active, ...done];
  if (rows.length === 0) return null;
  const noteLine = (className?: string) =>
    note ? (
      <span role="status" className={cn("block text-meta leading-relaxed", note.failed ? "text-reject" : "text-dim", className)}>
        {note.text}
      </span>
    ) : null;
  return (
    <section aria-label="대기 주문" className="mb-3">
      <p className="mb-1 text-meta text-faint">대기 주문 · 체결될 때까지 거래일마다 다시 내요</p>
      <ul className="divide-y divide-edge-soft">
        {rows.map((o) => {
          const name = names.get(`kis:${o.symbol}`) ?? o.symbol;
          return (
            <li key={o.id} className="flex items-start gap-3 py-2 text-aside">
              <span className="min-w-0 flex-1">
                <span className={o.status === "active" ? "text-ink" : "text-faint"}>
                  {name} {o.units}주 ${o.limitPrice.toFixed(2)} {o.side === "buy" ? "이하에 매수" : "이상에 매도"}
                </span>
                <span className="block text-meta leading-relaxed text-faint">
                  {o.status === "filled" ? "체결됐어요" : o.status === "cancelled" ? "취소됨" : o.status === "expired" ? "기한이 지났어요" : o.message}
                </span>
                {note?.id === o.id ? noteLine() : null}
              </span>
              {o.status === "active" ? (
                // Asked first, as every irreversible step is: one press would
                // cancel today's order at the broker with it.
                <AlertDialog>
                  <AlertDialogTrigger asChild>
                    <Button variant="discard" size="inline" disabled={busy === o.id} className="tap shrink-0 px-2 text-aside">
                      {busy === o.id ? "취소하는 중" : "취소"}
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
                          setNote(null);
                          try {
                            // Cancelled here with today's order still out at the
                            // broker is a 200 too: failed says so, and the words
                            // ("…취소하지 못했어요") say the half that did not go.
                            const { text, failed } = await cancelStanding(o.id);
                            setNote({ id: o.id, text, failed });
                            onChanged();
                          } catch (e) {
                            setNote({ id: o.id, text: failureText(e, "취소하지 못했어요."), failed: true });
                            // The line may have dropped after it was done: the list says.
                            onChanged();
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
      {/* The order cancelled may have left the few rows shown: its word stays. */}
      {note && !rows.some((o) => o.id === note.id) ? noteLine("mt-1") : null}
    </section>
  );
}

/**
 * The record: standing orders waiting, then what was sent, when, and how it
 * went. Reading, not read, and nothing sent yet are three different things
 * said three ways - a failed read had read as "아직 낸 주문이 없어요".
 */
export function OrderRecord({
  orders,
  holdings,
  standing = [],
  at = null,
  failed = false,
  onChanged = () => {},
}: {
  /** Null until it is read. */
  orders: PlacedOrder[] | null;
  holdings: Holding[];
  standing?: Standing[];
  /** When what is shown was read. */
  at?: Date | null;
  /** The last read did not come: what is shown is the one before it. */
  failed?: boolean;
  onChanged?: () => unknown;
}) {
  const names = new Map(holdings.map((h) => [`${h.venue}:${h.symbol}`, h.name]));
  const paged = usePaged(orders ?? []);
  if (!orders) {
    return failed ? (
      <Failed quiet onRetry={onChanged}>
        주문 기록을 불러오지 못했어요.
      </Failed>
    ) : (
      <p className="text-aside text-faint">{SAY.loading}</p>
    );
  }
  const asOf = failed ? <AsOf at={at} failed onRetry={() => void onChanged()} className="mb-2" /> : null;
  if (orders.length === 0 && standing.length === 0) {
    return (
      <>
        {asOf}
        <p className="text-aside text-faint">아직 낸 주문이 없어요.</p>
      </>
    );
  }
  return (
    <>
    {asOf}
    <Waiting standing={standing} names={names} onChanged={onChanged} />
    <div id={paged.anchor} className="scroll-mt-4" />
    <ul className="divide-y divide-edge-soft">
      {paged.items.map((o) => (
        <li key={o.id} className="flex items-baseline gap-3 py-2 text-aside">
          <span className="tnum w-[5.5rem] shrink-0 text-faint">{when(o.createdAt)}</span>
          <span className="min-w-0 flex-1">
            <span className="text-ink">{orderLine(o, names)}</span>
            {o.reason === "rebalance" ? <span className="ms-1.5 text-meta text-faint">리밸런싱</span> : null}
            {o.reason === "standing" ? <span className="ms-1.5 text-meta text-faint">대기 주문</span> : null}
            {o.status !== "placed" ? (
              <span className={cn("block text-meta", o.status === "failed" ? "text-reject" : "text-faint")}>{o.message}</span>
            ) : null}
          </span>
          <span className={cn("shrink-0 text-meta", o.status === "failed" ? "text-reject" : "text-faint")}>
            {statusText[o.status]}
          </span>
        </li>
      ))}
    </ul>
    <Pager {...paged} />
    </>
  );
}
