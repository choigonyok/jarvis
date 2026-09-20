"use client";

import { useEffect, useRef } from "react";
import { Composer } from "@/components/chat/composer";
import { ProposalCard } from "@/components/chat/proposal-card";
import { Header } from "@/components/shell/header";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";

/** Timestamps and status live in the rail, so the body stays a clean measure. */
function Rail({ at }: { at?: string }) {
  return (
    <div className="pt-[3px] pr-3 text-right sm:pr-4">
      {at ? <span className="tnum text-[11px] text-faint">{at}</span> : null}
    </div>
  );
}

export function Thread() {
  const { turns, proposals, byId, thinking, connection, error, hydrated, send, decide } =
    useThread();
  const bottomRef = useRef<HTMLDivElement>(null);

  const pending = proposals.filter(isPending).length;

  // A proposal nobody asked for has no turn pointing at it. Until a detector
  // exists to raise one, this renders nothing - but it is what makes the
  // chat able to show one the day it does.
  const attached = new Set(turns.map((t) => t.proposalId).filter(Boolean));
  const unattached = proposals.filter((p) => isPending(p) && !attached.has(p.id));

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [turns, proposals, thinking, error]);

  return (
    <div className="flex h-full flex-col">
      <Header connection={connection} pending={pending} />

      <main className="scrollbar-hairline relative flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-[46rem] px-5 pt-10 pb-6 sm:px-8">
          {hydrated && turns.length === 0 ? (
            <div className="grid grid-cols-[2.75rem_minmax(0,1fr)] sm:grid-cols-[3.5rem_minmax(0,1fr)]">
              <Rail />
              <p className="max-w-[32rem] text-[14.5px] leading-[1.75] text-dim">
                무엇을 맡길지 적어주세요. 상태를 바꾸는 일은 실행 전에 승인
                카드로 올라옵니다.
              </p>
            </div>
          ) : null}

          <ol className="space-y-7">
            {turns.map((turn) => {
              const proposal = turn.proposalId ? byId.get(turn.proposalId) : undefined;
              return turn.role === "agent" ? (
                <li
                  key={turn.id}
                  className="grid grid-cols-[2.75rem_minmax(0,1fr)] sm:grid-cols-[3.5rem_minmax(0,1fr)]"
                >
                  <Rail at={turn.at} />
                  <div className="min-w-0">
                    {turn.paragraphs?.length ? (
                      <div className="space-y-2.5">
                        {turn.paragraphs.map((p, i) => (
                          <p
                            key={i}
                            className="max-w-[36rem] text-[14.5px] leading-[1.75] text-pretty text-foreground/90"
                          >
                            {p}
                          </p>
                        ))}
                      </div>
                    ) : null}
                    {proposal ? (
                      <ProposalCard
                        proposal={proposal}
                        onDecide={(d) => void decide(proposal.id, d)}
                      />
                    ) : null}
                  </div>
                </li>
              ) : (
                <li key={turn.id} className="flex flex-col items-end gap-1.5">
                  <p className="max-w-[80%] rounded-2xl border border-edge bg-glass-raised px-4 py-2.5 text-[14.5px] leading-relaxed text-foreground backdrop-blur-md sm:max-w-[26rem]">
                    {turn.text}
                  </p>
                  <span className="tnum pr-1 text-[11px] text-faint">{turn.at}</span>
                </li>
              );
            })}

            {unattached.map((proposal) => (
              <li
                key={proposal.id}
                className="grid grid-cols-[2.75rem_minmax(0,1fr)] sm:grid-cols-[3.5rem_minmax(0,1fr)]"
              >
                <Rail at={proposal.at} />
                <div className="min-w-0">
                  <ProposalCard
                    proposal={proposal}
                    onDecide={(d) => void decide(proposal.id, d)}
                  />
                </div>
              </li>
            ))}

            {thinking ? (
              <li
                className="grid grid-cols-[2.75rem_minmax(0,1fr)] sm:grid-cols-[3.5rem_minmax(0,1fr)]"
                aria-live="polite"
                aria-label="Jarvis가 작업 중입니다"
              >
                <Rail />
                <span aria-hidden className="anim-think flex h-6 items-center gap-1">
                  <span className="size-[5px] rounded-full bg-dim" />
                  <span className="size-[5px] rounded-full bg-dim" />
                  <span className="size-[5px] rounded-full bg-dim" />
                </span>
              </li>
            ) : null}

            {error ? (
              <li
                className="grid grid-cols-[2.75rem_minmax(0,1fr)] sm:grid-cols-[3.5rem_minmax(0,1fr)]"
                role="status"
              >
                <Rail />
                <p className="max-w-[36rem] border-l-2 border-reject pl-3 text-[13.5px] leading-relaxed text-dim">
                  {error}
                </p>
              </li>
            ) : null}
          </ol>
          <div ref={bottomRef} />
        </div>
      </main>

      <footer className="relative shrink-0">
        {/* The transcript dissolves into the composer instead of stopping at a rule. */}
        <div
          aria-hidden
          className="pointer-events-none absolute inset-x-0 -top-10 h-10 bg-gradient-to-b from-transparent to-background"
        />
        <div className="mx-auto w-full max-w-[46rem] px-5 pb-6 sm:px-8">
          <Composer onSend={(text) => void send(text)} />
        </div>
      </footer>
    </div>
  );
}
