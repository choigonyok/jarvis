"use client";

import { useKept } from "@/lib/keep";
import { useMemo } from "react";
import { Entry } from "@/components/ledger/entry";
import { Failed } from "@/components/shell/failed";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { StandingBar } from "@/components/shell/standing-bar";
import { Pager } from "@/components/ui/pager";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { filters, groupByDay, matches, newestFirst, readArchive, waitingSince, withArchive, type Filter } from "@/lib/ledger";
import { usePaged } from "@/lib/paging";
import { isPending, type CardChoice, type Decision, type Proposal } from "@/lib/thread";
import { useResource } from "@/lib/use-resource";
import { useThread } from "@/lib/use-thread";

/**
 * Everything the agent has asked to do, and what happened to each one.
 *
 * It reads the same proposal store the chat does, so this is a view rather
 * than a log that could drift from it: approving here and approving in the
 * transcript are the same act on the same object, and a row cannot be marked
 * handled without actually being decided.
 */
export function Ledger() {
  const { proposals: live, connection, error, hydrated, decide } = useThread();
  // The thread carries the live cards; those decided long ago are in the
  // agent's archive, read when this opens and again on coming back to the
  // console - the agent may have archived more meanwhile, which the live
  // list then no longer holds. Not on a timer: cards move there in one pass
  // a day at most.
  const archive = useResource("record:archive", readArchive);
  const proposals = useMemo(() => withArchive(live, archive.data), [live, archive.data]);
  // The filter stays across a tab switch (lib/keep.ts).
  const [filter, setFilter] = useKept<Filter>("record:filter", "all");

  const pending = live.filter(isPending);
  // What waits stands above the days: every screen's bar lands here, and a card
  // from yesterday is on the second page of a busy day's list. In the full view
  // it is said once, up there.
  const waiting = useMemo(() => (filter === "all" ? newestFirst(proposals.filter(isPending)) : []), [proposals, filter]);
  const shown = useMemo(
    () => newestFirst(proposals.filter((p) => matches(p, filter) && !(filter === "all" && isPending(p)))),
    [proposals, filter],
  );
  // Twenty a page; a day split across pages still counts all of its own.
  const counts = useMemo(() => new Map(groupByDay(shown).map((d) => [d.key, d.entries.length])), [shown]);
  const paged = usePaged(shown, { reset: filter });
  const days = groupByDay(paged.items);

  // Nothing raised is said once the archive has answered too: until then the
  // older days may only be on their way, and if it fails that is said instead.
  const empty = hydrated && archive.data !== undefined && shown.length === 0 && waiting.length === 0;
  const decideOn = (proposal: Proposal) => (d: Decision, reason?: string, choice?: Pick<CardChoice, "id" | "label">) =>
    void decide(proposal.id, d, undefined, reason, choice);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={pending.length} />
        {/* Muted here: the waiting rows are in the list below, named and
            decidable. A bar pointing at what is already on screen is nagging. */}
        <StandingBar pending={pending} muted />
      </PageTop>

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar">
        <div className="mx-auto w-full max-w-page px-4 pt-5 pb-10 sm:px-8 sm:pt-6 sm:pb-6">
          <ToggleGroup
            type="single"
            aria-label="보기"
            variant="segment"
            size="segment"
            spacing={0.5}
            value={filter}
            onValueChange={(v) => {
              if (v) setFilter(v as Filter);
            }}
            className="mb-5 rounded-lg border border-edge-soft bg-glass p-0.5"
          >
            {filters.map((item) => (
              <ToggleGroupItem key={item.value} value={item.value} className="min-h-9 mouse:min-h-0 mouse:px-2.5 mouse:py-1">
                {item.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          {empty ? (
            <Empty align="start" className="py-6">
              <EmptyHeader>
                <EmptyTitle>{filter === "all" ? "아직 올라온 요청이 없어요" : "이 조건에 맞는 요청이 없어요"}</EmptyTitle>
                {filter === "all" ? (
                  <EmptyDescription>Jarvis가 상태를 바꾸는 일을 하려 할 때마다 여기에 남아요.</EmptyDescription>
                ) : null}
              </EmptyHeader>
            </Empty>
          ) : null}

          {waiting.length > 0 ? (
            <section className="mb-6">
              <div className="mb-1.5 flex items-center gap-3">
                <h2 className="text-aside text-faint">대기</h2>
                <span aria-hidden className="h-px flex-1 bg-edge-soft" />
                <span className="tnum text-meta text-faint">{waiting.length}건</span>
              </div>
              <div>
                {waiting.map((proposal) => (
                  <Entry key={proposal.id} proposal={proposal} onDecide={decideOn(proposal)} when={waitingSince(proposal)} />
                ))}
              </div>
            </section>
          ) : null}

          <div id={paged.anchor} className="scroll-mt-4 space-y-6">
            {days.map((day) => (
              <section key={day.key}>
                {/* The day is a rule with a name on it, not a card header:
                    what matters is where one day stops and the next starts. */}
                <div className="mb-1.5 flex items-center gap-3">
                  <h2 className="text-aside text-faint">{day.label}</h2>
                  <span aria-hidden className="h-px flex-1 bg-edge-soft" />
                  <span className="tnum text-meta text-faint">
                    {counts.get(day.key) ?? day.entries.length}건
                  </span>
                </div>

                <div>
                  {day.entries.map((proposal) => (
                    <Entry key={proposal.id} proposal={proposal} onDecide={decideOn(proposal)} />
                  ))}
                </div>
              </section>
            ))}
          </div>
          <Pager {...paged} unit="장" />

          {/* Only when none came: a later read that fails leaves the ones
              read before, which are still so. */}
          {archive.error && archive.data === undefined ? (
            <Failed onRetry={archive.reload} className="mt-6">
              지난 요청을 불러오지 못했어요.
            </Failed>
          ) : null}

          {error ? (
            <Failed className="mt-6">{error}</Failed>
          ) : null}
        </div>
      </main>

      <TabBar pending={pending.length} />
    </div>
  );
}
