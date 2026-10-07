"use client";

import { useMemo, useState } from "react";
import { Entry } from "@/components/ledger/entry";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { filters, groupByDay, matches, type Filter } from "@/lib/ledger";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

/**
 * Everything the agent has asked to do, and what happened to each one.
 *
 * It reads the same proposal store the chat does, so this is a view rather
 * than a log that could drift from it: approving here and approving in the
 * transcript are the same act on the same object, and a row cannot be marked
 * handled without actually being decided.
 */
export function Ledger() {
  const { proposals, connection, error, hydrated, decide } = useThread();
  const [filter, setFilter] = useState<Filter>("all");

  const pending = proposals.filter(isPending);
  const days = useMemo(
    () => groupByDay(proposals.filter((p) => matches(p, filter))),
    [proposals, filter],
  );

  const empty = hydrated && days.length === 0;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={pending.length} />
      {/* Muted here: the waiting rows are in the list below, named and
          decidable. A bar pointing at what is already on screen is nagging. */}
      <StandingBar pending={pending} muted />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-5 pb-10 sm:px-8 sm:pt-6 sm:pb-6">
          <div
            role="radiogroup"
            aria-label="보기"
            className="mb-5 inline-flex items-center gap-0.5 rounded-lg border border-edge-soft bg-glass p-0.5"
          >
            {filters.map((item) => {
              const active = filter === item.value;
              return (
                <button
                  key={item.value}
                  type="button"
                  role="radio"
                  aria-checked={active}
                  onClick={() => setFilter(item.value)}
                  className={cn(
                    "min-h-9 rounded-md px-3 text-[12.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-0 sm:px-2.5 sm:py-1",
                    active
                      ? "bg-glass-raised text-foreground"
                      : "text-faint hover:text-dim",
                  )}
                >
                  {item.label}
                </button>
              );
            })}
          </div>

          {empty ? (
            <p className="max-w-[30rem] text-[13.5px] leading-relaxed text-faint">
              {filter === "all"
                ? "아직 올라온 요청이 없습니다. Jarvis가 상태를 바꾸는 일을 하려 할 때마다 여기에 남습니다."
                : "이 조건에 맞는 요청이 없습니다."}
            </p>
          ) : null}

          <div className="space-y-6">
            {days.map((day) => (
              <section key={day.key}>
                {/* The day is a rule with a name on it, not a card header:
                    what matters is where one day stops and the next starts. */}
                <div className="mb-1.5 flex items-center gap-3">
                  <h2 className="text-[12px] text-faint">{day.label}</h2>
                  <span aria-hidden className="h-px flex-1 bg-edge-soft" />
                  <span className="tnum text-[11.5px] text-faint">
                    {day.entries.length}건
                  </span>
                </div>

                <div>
                  {day.entries.map((proposal) => (
                    <Entry
                      key={proposal.id}
                      proposal={proposal}
                      onDecide={(d) => void decide(proposal.id, d)}
                    />
                  ))}
                </div>
              </section>
            ))}
          </div>

          {error ? (
            <p
              role="status"
              className="mt-6 border-l-2 border-reject pl-3 text-[13.5px] leading-relaxed text-dim"
            >
              {error}
            </p>
          ) : null}
        </div>
      </main>

      <TabBar pending={pending.length} />
    </div>
  );
}
