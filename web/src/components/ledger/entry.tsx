"use client";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ChevronDown } from "lucide-react";
import { CardAnswers, CardFacts } from "@/components/chat/card-answers";
import { isLiteral, readableBody, readableConsequence } from "@/lib/card-body";
import { outcomeCopy, summarize } from "@/lib/ledger";
import { chosenOf, outcomeOf } from "@/lib/outcome";
import type { CardChoice, Decision, Proposal } from "@/lib/thread";
import { cn } from "@/lib/utils";

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
  onDecide: (decision: Decision, reason?: string, choice?: Pick<CardChoice, "id" | "label">) => void;
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
  const literal = isLiteral(proposal);
  const time = when ?? (waiting ? proposal.at : (proposal.decidedAt ?? proposal.at));
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
          {copy.label}
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

          {proposal.note ? (
            <p className="mt-1.5 text-meta text-faint">{proposal.note}</p>
          ) : null}
          {proposal.reason ? <p className="mt-1.5 text-aside leading-normal text-dim">이유: {proposal.reason}</p> : null}

          {waiting ? (
            <div className="mt-3">
              <CardAnswers
                proposal={proposal}
                size="pair-sm"
                onAnswer={(c, why) => onDecide(c.decision, why, c.id ? { id: c.id, label: c.label } : undefined)}
              />
            </div>
          ) : null}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
