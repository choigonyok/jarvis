"use client";

import Link from "next/link";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ChevronDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { CardAnswers, CardFacts } from "@/components/chat/card-answers";
import { Sending, Sent } from "@/components/chat/hold";
import { isLiteral, readableBody, readableConsequence } from "@/lib/card-body";
import { sentChoice, useDecision } from "@/lib/decide";
import { outcomeCopy, raisedAt, summarize } from "@/lib/ledger";
import { asksYesNo, chosenOf, outcomeOf } from "@/lib/outcome";
import type { CardChoice, Decision, Proposal } from "@/lib/thread";
import { cn } from "@/lib/utils";
import { clock, dateTime } from "@/lib/when";

/**
 * One request, as one line.
 *
 * The 2px edge is the same mark the chat's card wears once it is settled;
 * stacked down a list it becomes the thing you read first - a week of greens
 * with one red in it is a sentence about how the agent has been behaving,
 * readable before a single word is.
 *
 * Opening a row expands it in place rather than in a dialog: on a phone a
 * dialog would cover the list you were scanning, and what is inside is
 * evidence, not a separate task.
 */
export function Entry({
  proposal,
  onDecide,
  when,
}: {
  proposal: Proposal;
  /** Resolves to why the decision was refused, if it was. */
  onDecide: (decision: Decision, reason?: string, choice?: Pick<CardChoice, "id" | "label">) => Promise<string | null>;
  /** In place of the time, where no day stands above the row. */
  when?: string;
}) {
  const outcome = outcomeOf(proposal);
  // An answer of the card's own is the word for how it ended; the other
  // answer (아니요, 순서대로) keeps a question's grey edge.
  const chosen = chosenOf(proposal);
  const own = chosen && outcome !== "pending" && outcome !== "failed" && outcome !== "closed";
  const copy = own
    ? {
        label: chosen.label,
        edge: outcomeCopy[chosen.decision === "rejected" ? (chosen.tone === "neutral" ? "no" : "rejected") : outcome === "yes" ? "yes" : outcome].edge,
      }
    : outcomeCopy[outcome];
  const waiting = proposal.state === "pending";
  // As on the conversation's card (lib/decide.ts): on its way, then taken and
  // not back yet - in the buttons' place, and in the row's word for when it
  // is folded - and a refusal under the buttons, not at the foot of the list.
  const decision = useDecision(proposal, onDecide);
  // Fields and photos are shown on the conversation's card, not on this line.
  // Approved from here, what the model had put in the fields went unseen, a
  // required one left empty was refused on every press, and a listing's photos
  // were never looked at: such a card is decided where they are.
  const fields = Boolean(proposal.card.fields?.length);
  const photos = Boolean(proposal.card.images?.length);
  const literal = isLiteral(proposal);
  // When it was raised, under the day it was raised: the day above the row
  // and its place in the day come from that moment too (lib/ledger.ts). The
  // time it was decided stood here, and a card decided days later read as
  // that time on the day it came, the day's rows out of order.
  const time = when ?? clock(raisedAt(proposal)?.toISOString(), proposal.at);
  // How it ended, when: the agent stamps a decision with the time of day
  // alone, and the whole moment only as the card's last change.
  const ended = waiting ? "" : proposal.changedAt ? dateTime(proposal.changedAt) : (proposal.decidedAt ?? "");
  // The words and the facts apart, as on the chat's card.
  const facts = proposal.card.facts ?? [];
  const apart = facts.length > 0 || Boolean(proposal.card.writer);
  const body = readableBody(proposal.card.body).trim();

  return (
    <Collapsible
      className={cn(
        "group/entry relative rounded-lg transition-colors",
        "hover:bg-glass focus-within:bg-glass",
      )}
    >
      {/* The margin mark. Inset from the row's own rounding so a run of them
          reads as one column rather than as a stack of separate cards. */}
      <span
        aria-hidden
        className={cn(
          "absolute inset-y-1.5 left-0 w-[2px] rounded-full",
          copy.edge,
          waiting && "anim-breathe",
        )}
      />

      <CollapsibleTrigger
        className={cn(
          "flex w-full items-baseline gap-3 rounded-lg py-2.5 pr-2 pl-4 text-left outline-none",
          "min-h-11 mouse:min-h-0",
          "focus-ring",
        )}
      >
        <span className="tnum w-[2.9rem] shrink-0 text-aside text-faint">{time}</span>

        {/* One line, always. A row that wraps to two breaks the rhythm of the
            column, and everything it would have said is a tap away. */}
        <span className="min-w-0 flex-1 truncate text-body leading-snug text-ink">
          {summarize(proposal)}
        </span>

        {/* The edge in the margin already carries the colour. Saying it twice
            left the word brighter than the request it describes, so the word
            keeps only what the edge cannot say - rejected from failed,
            approved from executed. */}
        <span className="max-w-[40%] shrink-0 truncate text-meta whitespace-nowrap text-dim">
          {decision.busy ? "보내는 중" : decision.sent ? "반영 중" : copy.label}
        </span>

        <ChevronDown
          aria-hidden
          className="size-3.5 shrink-0 self-center text-faint transition-transform group-data-[state=open]/entry:rotate-180 motion-reduce:transition-none"
        />
      </CollapsibleTrigger>

      <CollapsibleContent className="overflow-hidden data-[state=closed]:animate-none">
        <div className="pt-0.5 pr-2 pb-3 pl-4">
          {/* What would actually run. A literal command keeps its line breaks
              and scrolls; a module's sentence wraps. A card with facts says
              its words in prose and its facts in the well. */}
          {apart ? (
            <>
              {body ? <p className="text-aside leading-relaxed whitespace-pre-wrap text-ink">{body}</p> : null}
              <CardFacts facts={facts} className={cn(body && "mt-2")} />
            </>
          ) : (
            <pre
              className={cn(
                "scrollbar-hairline rounded-md bg-well px-3 py-2.5 leading-relaxed text-dim",
                literal
                  ? "overflow-x-auto font-mono text-aside"
                  : "tnum font-sans text-aside whitespace-pre-wrap [&_code]:font-sans",
              )}
            >
              <code>{readableBody(proposal.card.body)}</code>
            </pre>
          )}

          {proposal.card.consequence ? (
            <p className="mt-2 text-aside leading-normal text-dim">{readableConsequence(proposal.card.consequence)}</p>
          ) : null}

          {ended ? (
            <p className="mt-2 text-meta text-faint">
              {copy.label} · <span className="tnum">{ended}</span>
            </p>
          ) : null}
          {proposal.note ? (
            <p className="mt-1.5 text-meta text-faint">{proposal.note}</p>
          ) : null}
          {proposal.reason ? <p className="mt-1.5 text-aside leading-normal text-dim">이유: {proposal.reason}</p> : null}

          {waiting ? (
            <div className="mt-3">
              {/* Heard as it comes, as on the conversation's card. */}
              <div aria-live="polite">
                {decision.busy ? (
                  <Sending />
                ) : decision.sent ? (
                  <Sent decision={decision.sent} yesNo={asksYesNo(proposal)} label={sentChoice(proposal.id)} />
                ) : null}
              </div>
              {decision.busy || decision.sent ? null : fields || photos ? (
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
                  <p className="px-1 text-aside leading-normal text-dim">
                    {fields && photos ? "적을 칸과 사진" : fields ? "적을 칸" : "사진"}이 있는 카드라 대화에서 결재해요.
                  </p>
                  <Button asChild variant="raised" size="pair-sm" className="sm:shrink-0">
                    <Link href={`/?proposal=${encodeURIComponent(proposal.id)}`}>대화에서 열기</Link>
                  </Button>
                </div>
              ) : (
                <CardAnswers
                  proposal={proposal}
                  size="pair-sm"
                  busy={decision.busy}
                  onAnswer={(c, why) => void decision.answer(c, why)}
                />
              )}
              {decision.refusal ? (
                <p role="alert" className="mt-2 px-1 text-aside text-reject">
                  {decision.refusal}
                </p>
              ) : null}
            </div>
          ) : null}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
