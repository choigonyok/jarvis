"use client";

import { Collapsible } from "radix-ui";
import { ChevronDown } from "lucide-react";
import { HoldBar, holdsMoney, useHold } from "@/components/chat/hold";
import { Button } from "@/components/ui/button";
import { isLiteral, readableBody, readableConsequence } from "@/lib/card-body";
import { stateCopy, summarize } from "@/lib/ledger";
import type { Decision, Proposal } from "@/lib/thread";
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
}: {
  proposal: Proposal;
  onDecide: (decision: Decision) => void;
}) {
  const copy = stateCopy[proposal.state];
  const waiting = proposal.state === "pending";
  const literal = isLiteral(proposal);
  const time = waiting ? proposal.at : (proposal.decidedAt ?? proposal.at);
  // Money waits five seconds with 되돌리기, as on the chat's card (chat/hold.tsx).
  const hold = useHold(() => onDecide("approved"));

  return (
    <Collapsible.Root
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

      <Collapsible.Trigger
        className={cn(
          "flex w-full items-baseline gap-3 rounded-lg py-2.5 pr-2 pl-4 text-left outline-none",
          "min-h-11 mouse:min-h-0",
          "focus-visible:ring-3 focus-visible:ring-ring/50",
        )}
      >
        <span className="tnum w-[2.9rem] shrink-0 text-[12px] text-faint">{time}</span>

        {/* One line, always. A row that wraps to two breaks the rhythm of the
            column, and everything it would have said is a tap away. */}
        <span className="min-w-0 flex-1 truncate text-[14px] leading-snug text-foreground/90">
          {summarize(proposal)}
        </span>

        {/* The edge in the margin already carries the colour. Saying it twice
            left the word brighter than the request it describes, so the word
            keeps only what the edge cannot say - rejected from failed,
            approved from executed. */}
        <span className="shrink-0 text-[11.5px] whitespace-nowrap text-dim">
          {copy.label}
        </span>

        <ChevronDown
          aria-hidden
          className="size-3.5 shrink-0 self-center text-faint transition-transform group-data-[state=open]/entry:rotate-180 motion-reduce:transition-none"
        />
      </Collapsible.Trigger>

      <Collapsible.Content className="overflow-hidden data-[state=closed]:animate-none">
        <div className="pt-0.5 pr-2 pb-3 pl-4">
          {/* What would actually run. A literal command keeps its line breaks
              and scrolls; a module's sentence wraps. */}
          <pre
            className={cn(
              "scrollbar-hairline rounded-md bg-well px-3 py-2.5 leading-relaxed text-dim",
              literal
                ? "overflow-x-auto font-mono text-[12px]"
                : "tnum font-sans text-[12.5px] whitespace-pre-wrap [&_code]:font-sans",
            )}
          >
            <code>{readableBody(proposal.card.body)}</code>
          </pre>

          <p className="mt-2 text-[12px] leading-normal text-dim">
            {readableConsequence(proposal.card.consequence)}
          </p>

          {proposal.note ? (
            <p className="mt-1.5 text-[11.5px] text-faint">{proposal.note}</p>
          ) : null}

          {waiting && hold.holding ? (
            <HoldBar className="mt-3" left={hold.left} onCancel={hold.cancel} />
          ) : waiting ? (
            <div className="mt-3 grid grid-cols-2 gap-2 sm:flex sm:justify-end sm:gap-1.5">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => onDecide("rejected")}
                className="h-11 text-[13px] text-dim hover:bg-reject/10 hover:text-reject mouse:h-7 mouse:px-2.5 mouse:text-[12.5px]"
              >
                반려
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => (holdsMoney(proposal) ? hold.start() : onDecide("approved"))}
                className="h-11 border-edge bg-glass-raised text-[13px] text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve mouse:h-7 mouse:px-3 mouse:text-[12.5px] dark:bg-glass-raised dark:hover:bg-approve/12"
              >
                승인
              </Button>
            </div>
          ) : null}
        </div>
      </Collapsible.Content>
    </Collapsible.Root>
  );
}
