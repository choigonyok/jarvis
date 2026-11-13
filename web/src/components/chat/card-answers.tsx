"use client";

import { useRef, useState } from "react";
import { TriangleAlert } from "lucide-react";
import { HeldBack, HoldBar, holdsMoney, useHold } from "@/components/chat/hold";
import { RejectForm } from "@/components/chat/reject-form";
import { Button } from "@/components/ui/button";
import { asksYesNo } from "@/lib/outcome";
import type { CardChoice, CardFact, Proposal } from "@/lib/thread";
import { cn } from "@/lib/utils";

/**
 * What a card is about as the code that raised it knows it - the amount a
 * payment window took, the site, the entry - in the well, where the screen
 * shows what a machine read. Whoever worded the card did not write these: the
 * words are above, in prose. What to check before deciding comes first, in
 * the brightest ink (the signal colours are 승인 and 반려 alone); then the
 * value the title leads with, at the size of a card's value; then the rest,
 * a label and a value a line.
 */
export function CardFacts({ facts, className }: { facts: CardFact[]; className?: string }) {
  const warns = facts.filter((f) => f.warn);
  const leads = facts.filter((f) => f.lead && !f.warn);
  const rest = facts.filter((f) => !f.lead && !f.warn);
  if (facts.length === 0) return null;
  return (
    <dl className={cn("rounded-md bg-well px-3 py-2.5 text-aside leading-relaxed", className)}>
      {warns.map((f, i) => (
        <div key={`w${i}`} className={cn("flex gap-1.5 text-foreground", i > 0 && "mt-1.5")}>
          <TriangleAlert aria-hidden className="mt-[0.2rem] size-3.5 shrink-0" strokeWidth={2} />
          <dt className="sr-only">{f.label}</dt>
          <dd className="min-w-0 break-words">{f.value}</dd>
        </div>
      ))}
      {leads.map((f, i) => (
        <div key={`l${i}`} className={cn((warns.length > 0 || i > 0) && "mt-2.5")}>
          <dt className="text-faint">{f.label}</dt>
          <dd className="tnum text-lead leading-tight font-medium text-foreground">{f.value}</dd>
        </div>
      ))}
      {rest.length > 0 ? (
        <div
          className={cn(
            "grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-3 gap-y-1",
            (warns.length > 0 || leads.length > 0) && "mt-2.5",
          )}
        >
          {rest.map((f, i) => (
            <div key={`f${i}`} className="contents">
              <dt className="max-w-[8.5rem] text-faint">{f.label}</dt>
              <dd className="tnum min-w-0 break-words text-dim">{f.value}</dd>
            </div>
          ))}
        </div>
      ) : null}
    </dl>
  );
}

/**
 * The answers a card offers: its own, or 반려 and 승인 for a card that offers
 * none (the 먼저 card raised before cards had their own: 아니요 and 예). An
 * answer of the card's own goes back with its id; the two that stand for none
 * go back as the decision alone.
 */
export function answersOf(p: Proposal): CardChoice[] {
  if (p.card.choices?.length) return p.card.choices;
  if (asksYesNo(p)) {
    return [
      { id: "", label: "아니요", decision: "rejected", tone: "neutral" },
      { id: "", label: "예", decision: "approved", tone: "approve" },
    ];
  }
  return [
    { id: "", label: "반려", decision: "rejected", tone: "reject", why: true },
    { id: "", label: "승인", decision: "approved", tone: "approve" },
  ];
}

const VARIANT = { approve: "approve", neutral: "soft", reject: "reject" } as const;

/**
 * The buttons of a card that waits: each answer it offers, drawn by what it
 * does - green on the pointer for the one that runs, red for a turn-down, plain
 * for the other answer. Two side by side, the one that runs on the right where
 * 승인 always is, so a thumb that knows the card finds it; three or four, one
 * under another on a phone, in the order offered.
 *
 * An answer that asks why opens the reason form first. One that runs a
 * payment waits five seconds with 되돌리기 (chat/hold.tsx). What cannot be
 * chosen yet (a field it needs is empty) is off, and the card says why.
 */
export function CardAnswers({
  proposal,
  size,
  busy,
  blocked,
  onAnswer,
}: {
  proposal: Proposal;
  size: "pair" | "pair-sm";
  busy?: boolean;
  /** Whether an answer cannot be chosen yet. */
  blocked?: (c: CardChoice) => boolean;
  onAnswer: (c: CardChoice, reason?: string) => void;
}) {
  const offered = answersOf(proposal);
  // Two answers keep the pair's places: the one that runs on the right.
  const answers =
    offered.length === 2 && offered[0].decision === "approved" && offered[1].decision === "rejected"
      ? [offered[1], offered[0]]
      : offered;
  const [asking, setAsking] = useState<CardChoice | null>(null);
  // The answer a held payment goes as, once its five seconds are up.
  const held = useRef<CardChoice | null>(null);
  const hold = useHold(proposal.id, () => {
    if (held.current) onAnswer(held.current);
  });

  if (hold.holding) return <HoldBar left={hold.left} onCancel={hold.cancel} />;
  if (asking) {
    return (
      <RejectForm
        busy={busy}
        label={asking.label}
        onCancel={() => setAsking(null)}
        onReject={(why) => {
          setAsking(null);
          onAnswer(asking, why);
        }}
      />
    );
  }

  const choose = (c: CardChoice) => {
    if (c.why && c.decision === "rejected") {
      setAsking(c);
      return;
    }
    if (c.decision === "approved" && holdsMoney(proposal)) {
      held.current = c;
      hold.start();
      return;
    }
    onAnswer(c);
  };
  const pair = answers.length <= 2;
  return (
    <div>
      {hold.interrupted ? <HeldBack /> : null}
      <div
        className={cn(
          pair
            ? "grid grid-cols-2 gap-2 sm:flex sm:items-center sm:justify-end sm:gap-1.5"
            : "flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:justify-end sm:gap-1.5",
        )}
      >
        {answers.map((c, i) => (
          <Button
            key={c.id || `${c.decision}-${i}`}
            variant={VARIANT[c.tone ?? (c.decision === "approved" ? "approve" : "reject")]}
            size={size}
            onClick={() => choose(c)}
            disabled={busy || (blocked?.(c) ?? false)}
            // A label of the card's own may run to two lines on a phone: the
            // button grows with it and never cuts it.
            className={cn("h-auto py-2 whitespace-normal mouse:h-auto mouse:min-h-8", size === "pair" ? "min-h-12" : "min-h-11")}
          >
            {c.label}
          </Button>
        ))}
      </div>
    </div>
  );
}
