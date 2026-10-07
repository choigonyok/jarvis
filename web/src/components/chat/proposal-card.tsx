"use client";

import { Check, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Decision, Proposal, ProposalState } from "@/lib/thread";
import { cn } from "@/lib/utils";

const settledCopy: Record<
  Exclude<ProposalState, "pending">,
  { label: string; tone: string; edge: string; Icon: typeof Check }
> = {
  approved: { label: "승인함", tone: "text-approve", edge: "bg-approve", Icon: Check },
  executed: { label: "실행함", tone: "text-approve", edge: "bg-approve", Icon: Check },
  rejected: { label: "반려함", tone: "text-reject", edge: "bg-reject", Icon: X },
  failed: { label: "실패함", tone: "text-reject", edge: "bg-reject", Icon: X },
};

export function ProposalCard({
  proposal,
  onDecide,
}: {
  proposal: Proposal;
  onDecide: (decision: Decision) => void;
}) {
  const settled =
    proposal.state === "pending" ? null : settledCopy[proposal.state];
  // Claude Code's own tools are shown as the literal call; a module's action
  // is shown as the change it makes.
  const literal = proposal.action.kind.startsWith("claude.");

  return (
    <article
      // The thread's standing bar watches for these: a decision on screen
      // does not need announcing at the top of the page.
      data-pending-card={settled ? undefined : ""}
      className={cn(
        "relative mt-4 w-full max-w-[34rem] overflow-hidden rounded-xl border border-edge bg-glass backdrop-blur-md",
        "transition-colors duration-300",
        settled && "border-edge-soft",
      )}
    >
      {settled ? (
        <span
          aria-hidden
          className={cn("anim-edge absolute inset-y-0 left-0 w-[2px]", settled.edge)}
        />
      ) : null}

      <div className="px-4 pt-3.5 pb-4 sm:px-5">
        {/* Only an undecided proposal announces itself; a settled one is read by its edge. */}
        {settled ? null : (
          <div className="mb-2 flex items-center gap-2 text-[11px] text-faint">
            <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
            <span>결재 대기</span>
          </div>
        )}

        <h3 className="text-[14.5px] leading-snug font-medium text-foreground">
          {proposal.card.title}
        </h3>

        {/* Mono is reserved for a literal command the CLI would run. A
            module renders its change as a sentence, and setting that in mono
            would blur the line between "this is text" and "this is code".

            That split decides wrapping too: a command must not be re-wrapped,
            because a broken line changes what it says, so it scrolls. A
            sentence has no such constraint and wraps, which is what a narrow
            screen needs. */}
        <pre
          className={cn(
            "scrollbar-hairline mt-3 rounded-md bg-well px-3 py-2.5 leading-relaxed text-dim",
            literal
              ? "overflow-x-auto font-mono text-[12px]"
              : "tnum text-[13px] whitespace-pre-wrap",
          )}
        >
          <code>{proposal.card.body}</code>
        </pre>

        <p className="mt-2.5 text-[12px] leading-normal text-dim">
          {proposal.card.consequence}
        </p>
      </div>

      <div className="border-t border-edge-soft px-3 py-2.5 sm:px-4">
        {settled ? (
          <p
            className={cn(
              "anim-settle flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[11.5px]",
              settled.tone,
            )}
          >
            <settled.Icon aria-hidden className="size-3.5" />
            {settled.label}
            <span className="tnum text-faint">· {proposal.decidedAt}</span>
            {proposal.note ? <span className="text-faint">· {proposal.note}</span> : null}
          </p>
        ) : (
          /* This is the decision the whole product exists to collect, and on
             a phone it was a pair of 32px targets sitting a thumb-width apart.
             Full width, 48px, and a real gap between them: reaching for 승인
             must not be able to land on 반려. */
          <div className="grid grid-cols-2 gap-2 sm:flex sm:items-center sm:justify-end sm:gap-1.5">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onDecide("rejected")}
              className="h-12 text-[13.5px] text-dim hover:bg-reject/10 hover:text-reject sm:h-8 sm:px-3 sm:text-[0.8rem]"
            >
              반려
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => onDecide("approved")}
              className="h-12 border-edge bg-glass-raised text-[13.5px] text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve sm:h-8 sm:px-3.5 sm:text-[0.8rem] dark:bg-glass-raised dark:hover:bg-approve/12"
            >
              승인
            </Button>
          </div>
        )}
      </div>
    </article>
  );
}
