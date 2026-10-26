"use client";

import { useMemo, useState } from "react";
import { Pager } from "@/components/ui/pager";
import { usePaged } from "@/lib/paging";
import { krw, VENUE_LABEL, type Outflow, type OutflowPart } from "@/lib/portfolio";

/**
 * Where money taken out of the accounts went (assets-svc outflows.ts).
 *
 * Most withdrawals were moved - Upbit to the bank to KIS - and are found
 * paired with the deposit they became. What nothing explains is asked about
 * on a card in the conversation (the agent's assets/ask.go); its answer shows
 * here, and what was spent goes into the 가계부. None of it moves a figure
 * above: a withdrawal left the principal whatever it was for.
 */

const md = (date: string) => `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;

const addDays = (date: string, days: number) => {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
};

/** What people said was spent out of the accounts. */
export function spentKrw(outflows: Outflow[]): number {
  return outflows.reduce(
    (sum, o) => sum + o.parts.filter((p) => p.use === "spend").reduce((s, p) => s + p.amountKrw, 0),
    0,
  );
}

/** The lines under 원금 while it is folded: what was spent, and what is still being asked. */
export function OutflowSummary({ outflows }: { outflows: Outflow[] }) {
  const spent = spentKrw(outflows);
  const asking = outflows.filter((o) => o.status === "ask");
  if (spent === 0 && asking.length === 0) return null;
  return (
    <>
      {spent > 0 ? <p className="tnum mt-1 text-[12.5px] text-dim">투자에서 꺼내 쓴 돈 {krw(spent)}</p> : null}
      {asking.length > 0 ? (
        <p className="tnum mt-1 text-[12px] leading-relaxed text-dim">
          <span aria-hidden className="me-1.5 inline-block size-1.5 rounded-full bg-reject align-middle" />
          어디로 갔는지 묻는 중 {asking.length}건 · {krw(asking.reduce((sum, o) => sum + o.askKrw, 0))}
        </p>
      ) : null}
    </>
  );
}

/**
 * One part of a withdrawal, in words: "→ 업비트로 옮김 · 10/8", "지출(생활·통신) · 월세".
 * The category in brackets: its own name may hold a middle dot.
 */
function partLine(p: OutflowPart, whole: number): string {
  const amount = p.amountKrw < whole - 0.5 ? ` ${krw(p.amountKrw)}` : "";
  switch (p.use) {
    case "transfer":
      return p.to ? `→ ${VENUE_LABEL[p.to.venue]}로 옮김 · ${md(p.to.date)}${amount}` : `다른 투자 계좌로 옮김${amount}`;
    case "spend":
      return `지출${p.category ? `(${p.category})` : ""}${p.memo ? ` · ${p.memo}` : ""}${amount}`;
    case "card":
      return `카드값${amount}`;
    case "saved":
      return `보관${amount}`;
    case "parked": {
      // Asked about again thirty days after the answer (assets-svc PARKED_DAYS).
      const until = p.decidedAt
        ? new Date(Date.parse(p.decidedAt) + 30 * 86_400_000).toLocaleDateString("sv-SE", { timeZone: "Asia/Seoul" })
        : null;
      return `아직 통장에${amount}${until ? ` · ${md(until)}까지 기다려요` : ""}`;
    }
  }
}

/** Every withdrawal since the principal's start, newest first, and where it went. */
export function OutflowList({ outflows, onChanged }: { outflows: Outflow[]; onChanged: () => void }) {
  const newest = useMemo(() => [...outflows].reverse(), [outflows]);
  const paged = usePaged(newest);
  if (outflows.length === 0) return <p className="text-[12.5px] text-faint">아직 뺀 돈이 없어요.</p>;
  return (
    <div id={paged.anchor} className="scroll-mt-4">
      <p className="mb-2 text-[12px] leading-relaxed text-faint">
        다른 투자 계좌로 다시 들어온 돈은 옮긴 것으로 보고, 아니면 대화에서 카드로 물어요. 지출로 답한 돈은 가계부에 들어가요.
      </p>
      <ul className="divide-y divide-edge-soft">
        {paged.items.map((o) => (
          <OutflowRow key={o.key} outflow={o} onChanged={onChanged} />
        ))}
      </ul>
      <Pager {...paged} />
    </div>
  );
}

function OutflowRow({ outflow: o, onChanged }: { outflow: Outflow; onChanged: () => void }) {
  const [state, setState] = useState<"idle" | "busy" | "done">("idle");
  const [error, setError] = useState<string | null>(null);
  const answered = o.parts.some((p) => p.decidedAt);

  const askAgain = async () => {
    setState("busy");
    setError(null);
    try {
      const res = await fetch(`/api/portfolio/outflows?key=${encodeURIComponent(o.key)}`, { method: "DELETE" });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        throw new Error(body?.error ?? `자산 서비스가 ${res.status} 로 답했어요.`);
      }
      setState("done");
      onChanged();
    } catch (e) {
      setState("idle");
      setError((e as Error).message);
    }
  };

  return (
    <li className="flex items-start gap-3 py-2.5 text-[12.5px]">
      <span className="tnum w-[2.75rem] shrink-0 pt-px text-faint">{md(o.date)}</span>
      <span className="min-w-0 flex-1">
        <span className="tnum block text-foreground/90">
          {VENUE_LABEL[o.venue]} {krw(o.amountKrw)}
        </span>
        {o.parts.map((p, i) => (
          <span key={i} className="tnum block truncate text-[11.5px] text-dim">
            {partLine(p, o.amountKrw)}
          </span>
        ))}
        {o.status === "ask" ? (
          <span className="tnum block text-[11.5px] text-dim">
            <span aria-hidden className="me-1.5 inline-block size-1.5 rounded-full bg-reject align-middle" />
            어디로 갔는지 묻는 중{o.parts.length > 0 ? ` ${krw(o.askKrw)}` : ""}
          </span>
        ) : o.status === "waiting" ? (
          <span className="block text-[11.5px] text-faint">
            다른 계좌로 들어오는지 보는 중 · {md(addDays(o.date, 3))}에 물어요
          </span>
        ) : null}
        {state === "done" ? <span className="block text-[11.5px] text-faint">다시 물을게요. 곧 대화에 카드가 와요.</span> : null}
        {error ? <span className="block text-[11.5px] text-reject/90">{error}</span> : null}
      </span>
      {answered && state !== "done" ? (
        <button
          type="button"
          disabled={state === "busy"}
          onClick={() => void askAgain()}
          // The hit area reaches above and below; the word sits level with the amount.
          className="tap -mt-2.5 -mb-2.5 flex shrink-0 items-start rounded-lg px-1.5 pt-3 text-[11.5px] leading-none text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
        >
          {state === "busy" ? "지우는 중" : "다시 묻기"}
        </button>
      ) : null}
    </li>
  );
}
