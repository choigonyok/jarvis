"use client";

import { useEffect, useRef, useState } from "react";
import { TriangleAlert } from "lucide-react";
import { HeldBack, HoldBar, holdsMoney, useHold } from "@/components/chat/hold";
import { RejectForm } from "@/components/chat/reject-form";
import { Button } from "@/components/ui/button";
import { typedOn } from "@/lib/decide";
import { keepValue, keptValue } from "@/lib/keep";
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
  blockedWhy,
  onAnswer,
}: {
  proposal: Proposal;
  size: "pair" | "pair-sm";
  busy?: boolean;
  /** Whether an answer cannot be chosen yet. */
  blocked?: (c: CardChoice) => boolean;
  /** The id of what says why, for the answers that cannot. */
  blockedWhy?: string;
  onAnswer: (c: CardChoice, reason?: string) => void;
}) {
  const offered = answersOf(proposal);
  // Two answers keep the pair's places: the one that runs on the right.
  const answers =
    offered.length === 2 && offered[0].decision === "approved" && offered[1].decision === "rejected"
      ? [offered[1], offered[0]]
      : offered;
  // The answer whose reason is being asked for, by its place: asked for still
  // when the card is drawn again (another tab and back, a send refused), as
  // its fields are, until the card is back decided (lib/decide.ts).
  const askKey = typedOn(proposal.id, "asking");
  const [askingAt, setAskingAt] = useState<number | null>(() => keptValue<number>(askKey) ?? null);
  useEffect(() => {
    keepValue(askKey, askingAt ?? undefined);
  }, [askKey, askingAt]);
  const asking = askingAt === null ? null : (answers[askingAt] ?? null);
  // The answer a held payment goes as, once its five seconds are up, and its place.
  const held = useRef<{ c: CardChoice; at: number } | null>(null);
  const hold = useHold(proposal.id, () => {
    if (held.current) onAnswer(held.current.c);
  });

  // Pressed from the keyboard, an answer's button left the page under the
  // focus, and the focus fell to the top: 되돌리기's five seconds were out of
  // reach. It goes on to what takes the button's place, and back to the
  // button when that is let go. A tap leaves no focus here and moves none.
  // The reason's field takes it under a mouse however 반려 was pressed - the
  // reason is what comes next - and never on a touch screen, where it would
  // raise the keyboard for a reason that may be left empty.
  const box = useRef<HTMLDivElement>(null);
  const focusNext = useRef<string | null>(null);
  useEffect(() => {
    const target = focusNext.current;
    focusNext.current = null;
    if (target) box.current?.querySelector<HTMLElement>(target)?.focus();
  });
  const follow = (target: string) => {
    if (box.current?.contains(document.activeElement)) focusNext.current = target;
  };
  const answerAt = (at: number) => `[data-answer="${at}"]`;

  const choose = (c: CardChoice, at: number) => {
    if (c.why && c.decision === "rejected") {
      if (window.matchMedia("(pointer: fine)").matches) focusNext.current = "input";
      else follow("button");
      setAskingAt(at);
      return;
    }
    if (c.decision === "approved" && holdsMoney(proposal)) {
      follow("button");
      held.current = { c, at };
      hold.start();
      return;
    }
    onAnswer(c);
  };
  const pair = answers.length <= 2;
  return (
    <div ref={box}>
      {/* What the hold says - the seconds before a payment goes, that one was
          held back - is said into a region already on the page: a line that
          comes with a region of its own is mostly not read. The buttons and
          the reason's form stay out of it; the focus finds those. */}
      <div aria-live="polite">
        {hold.holding ? (
          <HoldBar
            left={hold.left}
            onCancel={() => {
              if (hold.cancel()) follow(answerAt(held.current?.at ?? 0));
            }}
          />
        ) : !asking && hold.unsent ? (
          <HeldBack by={hold.unsent} />
        ) : null}
      </div>
      {hold.holding ? null : asking ? (
        <RejectForm
          busy={busy}
          label={asking.label}
          keepKey={typedOn(proposal.id, "why")}
          onCancel={() => {
            follow(answerAt(askingAt ?? 0));
            setAskingAt(null);
          }}
          // Still asked for while it goes: a send refused draws the form
          // again with the reason as it was written, the refusal under it.
          onReject={(why) => onAnswer(asking, why)}
        />
      ) : (
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
              data-answer={i}
              variant={VARIANT[c.tone ?? (c.decision === "approved" ? "approve" : "reject")]}
              size={size}
              onClick={() => choose(c, i)}
              disabled={busy || (blocked?.(c) ?? false)}
              aria-describedby={blocked?.(c) ? blockedWhy : undefined}
              // A label of the card's own may run to two lines on a phone: the
              // button grows with it and never cuts it.
              className={cn("h-auto py-2 whitespace-normal mouse:h-auto mouse:min-h-8", size === "pair" ? "min-h-12" : "min-h-11")}
            >
              {/* A payment let go is asked for again, in so many words. */}
              {hold.unsent && c.decision === "approved" && holdsMoney(proposal) ? `다시 ${c.label}` : c.label}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}
