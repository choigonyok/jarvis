"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Pencil, Plus } from "lucide-react";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { Retry } from "@/components/shell/retry";
import { useRole } from "@/components/shell/role";
import { StandingBar } from "@/components/shell/standing-bar";
import { Board } from "@/components/transit/board";
import { RouteEditor } from "@/components/transit/route-editor";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { onResume } from "@/lib/resume";
import { isPending } from "@/lib/thread";
import type { Who } from "@/lib/track";
import { type Departures as Read, type Plan, type Route, clockOf, readDepartures, saveRoutes, stageOf } from "@/lib/transit";
import { useThread } from "@/lib/use-thread";
import { SAY } from "@/lib/say";
import { cn } from "@/lib/utils";

/** The route a person last looked at, in their own browser. */
const PICK_KEY = "jarvis:transit-route";

/**
 * 출발: when to leave for the bus or the train one usually takes. Each saved
 * route - its stop, its line, the walk to it - is read against the real-time
 * feeds, and the one that matters now is the board: one's face saying how it
 * stands ("슬슬 짐 싸야 해요"), the minutes until one must leave, the rail,
 * and the line to catch. Each person sees and keeps their own routes only.
 */
export function Departures() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const role = useRole();
  const who: Who = role === "guest" ? "guest" : "owner";
  const [read, setRead] = useState<Read | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const [picked, setPicked] = useState<string | null>(() => {
    if (typeof window === "undefined") return null;
    try {
      return localStorage.getItem(PICK_KEY);
    } catch {
      return null;
    }
  });
  const [editing, setEditing] = useState<Route | null | undefined>(undefined);

  const load = useCallback(
    () =>
      readDepartures(who).then(
        (r) => {
          setRead(r);
          setError(null);
        },
        (e) => setError(e instanceof Error ? e.message : "출발 정보를 읽지 못했어요."),
      ),
    [who],
  );
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => onResume(load), [load]);
  // The feeds move every few tens of seconds; the countdown every second.
  useEffect(() => {
    const read = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, 20_000);
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => {
      window.clearInterval(read);
      window.clearInterval(tick);
    };
  }, [load]);

  function pick(id: string) {
    setPicked(id);
    try {
      localStorage.setItem(PICK_KEY, id);
    } catch {}
  }

  const routes = useMemo(() => read?.routes ?? [], [read]);
  const planOf = useMemo(() => new Map((read?.plans ?? []).map((p) => [p.route, p])), [read]);
  // The board: the route one picked, else the one to leave for soonest.
  const current = useMemo(() => {
    if (!routes.length) return null;
    const chosen = routes.find((r) => r.id === picked);
    if (chosen) return chosen;
    const soonest = [...routes]
      .filter((r) => planOf.get(r.id)?.leave)
      .sort((a, b) => Date.parse(planOf.get(a.id)!.leave!) - Date.parse(planOf.get(b.id)!.leave!))[0];
    return soonest ?? routes[0];
  }, [routes, picked, planOf]);

  async function keep(next: Route[]) {
    const adding = editing === null;
    const saved = await saveRoutes(who, next);
    setEditing(undefined);
    setRead((r) => (r ? { ...r, routes: saved } : r));
    // A route just added is what one wants to see.
    if (adding && saved.length) pick(saved[saved.length - 1].id);
    void load();
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={waiting.length} />
        <StandingBar pending={waiting} />
      </PageTop>
      <main className="inset-x-safe min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar">
        <div className="mx-auto w-full max-w-page-narrow px-4 pt-5 pb-10 sm:px-8 sm:pt-8">
          {!read ? (
            error ? (
              <div className="pt-10 text-center">
                <p className="mb-3 text-ui text-dim">{error}</p>
                <Retry onRetry={load} />
              </div>
            ) : (
              <p className="pt-10 text-center text-ui text-faint">{SAY.loading}</p>
            )
          ) : routes.length === 0 ? (
            <NoRoutes onAdd={() => setEditing(null)} />
          ) : (
            <>
              {/* One route's board at a time; the arrow keys go between them. */}
              <Tabs value={current?.id ?? ""} onValueChange={pick}>
                {routes.length > 1 ? (
                  <TabsList variant="pills" aria-label="경로" className="-mx-1 mb-5 overflow-x-auto px-1 pb-1">
                    {routes.map((r) => (
                      <TabsTrigger key={r.id} value={r.id}>
                        {r.name}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                ) : null}
                {current ? (
                  <TabsContent value={current.id}>
                    <Board who={who} route={current} plan={planOf.get(current.id)} now={now} />
                  </TabsContent>
                ) : null}
              </Tabs>
              {!read.feeds.buses || !read.feeds.subway ? <Keys feeds={read.feeds} /> : null}

              <section className="mt-10" aria-label="내 경로">
                <h2 className="mb-1 text-ui font-medium text-foreground">내 경로</h2>
                <ul className="divide-y divide-edge-soft">
                  {routes.map((r) => (
                    <li key={r.id} className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={() => pick(r.id)}
                        className="flex min-w-0 flex-1 items-baseline gap-3 py-3 text-left focus-ring"
                      >
                        <span className={cn("w-12 shrink-0 text-body", current?.id === r.id ? "text-foreground" : "text-ink")}>{r.name}</span>
                        <span className="min-w-0 flex-1 truncate text-aside text-dim">
                          {r.stop.name} · {r.line.name}
                          {r.from ? ` · ${r.from}–${r.to}${r.push ? " 알림" : ""}` : ""}
                        </span>
                        <span className="tnum shrink-0 text-aside text-faint">{status(planOf.get(r.id), now)}</span>
                      </button>
                      <button
                        type="button"
                        onClick={() => setEditing(r)}
                        aria-label={`${r.name} 고치기`}
                        className="tap -mr-2 flex shrink-0 items-center justify-center rounded-full text-faint hover:text-foreground focus-ring"
                      >
                        <Pencil aria-hidden className="size-[15px]" strokeWidth={1.8} />
                      </button>
                    </li>
                  ))}
                </ul>
                <button
                  type="button"
                  onClick={() => setEditing(null)}
                  disabled={routes.length >= 12}
                  className="mt-2 inline-flex h-11 items-center gap-1.5 rounded-lg px-1 text-ui text-dim hover:text-foreground focus-ring disabled:opacity-40 mouse:h-9"
                >
                  <Plus aria-hidden className="size-4" strokeWidth={1.8} />
                  경로 추가
                </button>
              </section>
            </>
          )}
        </div>
      </main>
      <TabBar pending={waiting.length} />

      <RouteEditor
        who={who}
        open={editing !== undefined}
        onOpenChange={(o) => !o && setEditing(undefined)}
        route={editing ?? null}
        onSave={(r) => keep(editing ? routes.map((x) => (x.id === editing.id ? { ...r, id: x.id } : x)) : [...routes, r])}
        onDelete={editing ? () => keep(routes.filter((x) => x.id !== editing.id)) : undefined}
      />
    </div>
  );
}

/** A route's line in the list: when to leave, as the clock goes on. */
function status(plan: Plan | undefined, now: number): string {
  if (!plan) return "";
  if (plan.stage === "off") return "실시간 없음";
  if (!plan.leave || !plan.catch) return plan.missed?.length ? "걸어선 못 타요" : "곧 오는 차 없음";
  const leave = Date.parse(plan.leave);
  const { stage } = stageOf(leave, now, plan.last ? Date.parse(plan.last) : leave);
  if (stage === "none") return "놓침";
  if (stage === "run") return "지금 뛰기";
  const min = Math.round((leave - now) / 60_000);
  return min <= 0 ? "지금 나가기" : `${clockOf(plan.leave)} 나가기`;
}

function NoRoutes({ onAdd }: { onAdd: () => void }) {
  return (
    <Empty className="pt-12">
      <EmptyHeader className="gap-1">
        <EmptyTitle className="font-normal">자주 타는 버스나 지하철을 경로로 두면</EmptyTitle>
        <EmptyDescription>실시간 도착으로 언제 나가야 하는지 세어 드리고, 정한 시간대에는 알림도 보내요.</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button variant="raised" size="touch" onClick={onAdd}>
          <Plus aria-hidden strokeWidth={1.8} />
          경로 추가
        </Button>
      </EmptyContent>
    </Empty>
  );
}

/** Which live feed has no key yet - said once, under the board. */
function Keys({ feeds }: { feeds: { buses: boolean; subway: boolean } }) {
  const missing = [!feeds.buses && "버스", !feeds.subway && "지하철"].filter(Boolean).join("·");
  return (
    <p className="mt-6 text-aside leading-[1.55] break-keep text-faint">
      {missing} 실시간 정보 키가 아직 없어 그 경로는 시각을 세지 못해요. 키를 맥미니에 넣으면 바로 이어져요.
    </p>
  );
}
