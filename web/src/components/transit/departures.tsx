"use client";

import { useEffect, useMemo, useState } from "react";
import { Pencil, Plus } from "lucide-react";
import { AsOf } from "@/components/shell/as-of";
import { Failed } from "@/components/shell/failed";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { useRole } from "@/components/shell/role";
import { StandingBar } from "@/components/shell/standing-bar";
import { Board } from "@/components/transit/board";
import { RouteEditor } from "@/components/transit/route-editor";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useKept } from "@/lib/keep";
import { onShown } from "@/lib/resume";
import { isPending } from "@/lib/thread";
import type { Who } from "@/lib/track";
import { type Departures as Read, type Plan, type Route, active, clockOf, readDepartures, saveRoutes, stageOf } from "@/lib/transit";
import { useResource } from "@/lib/use-resource";
import { useThread } from "@/lib/use-thread";
import { failureText, SAY } from "@/lib/say";
import { cn } from "@/lib/utils";

/** The route a person last picked, in their own browser. */
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
  // Read on opening, on coming back to the app, and while on screen every 20
  // seconds: the feeds move every few tens of seconds. It is not handed the
  // signal, so nothing cuts it short: context-svc reads the feeds for it and
  // keeps what they said a little while for whoever asks next
  // (internal/transit/feeds.go), and a feed read cut short with this request
  // could be kept as the feed failing - the board would say there is no live
  // data.
  const departures = useResource<Read>(`transit:${who}`, () => readDepartures(who), { interval: 20_000 });
  const read = departures.data ?? null;
  const error = departures.error ? failureText(departures.error, "출발 정보를 불러오지 못했어요.") : null;
  const load = departures.reload;
  const [now, setNow] = useState(() => Date.now());
  const [picked, setPicked] = useState<string | null>(() => {
    if (typeof window === "undefined") return null;
    try {
      return localStorage.getItem(PICK_KEY);
    } catch {
      return null;
    }
  });
  // The route pressed while looking, and the hours it was pressed in: it
  // holds - across a tab switch too - while those hours stand, and gives way
  // when others come.
  const [chosen, setChosen] = useKept<{ id: string; under: string } | null>("transit:chosen", null);
  // When this look began - opening the screen, or coming back to the app -
  // which the hours are read at, so the board does not change routes under
  // one's eyes as an hour ends.
  const [lookedAt, setLookedAt] = useState(() => Date.now());
  useEffect(() => onShown(() => setLookedAt(Date.now())), []);
  const [editing, setEditing] = useState<Route | null | undefined>(undefined);

  // The countdown moves every second.
  useEffect(() => {
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(tick);
  }, []);
  // The board counts on from the plan last read. Past refreshing - the last
  // read failed, or none has landed for three rounds (a phone in a lift, whose
  // reads hang rather than fail) - it says from when, and the count dims:
  // someone may run for a bus that is no longer coming.
  const stale = read !== null && (departures.error !== null || now - (departures.readAt?.getTime() ?? now) > 60_000);

  const routes = useMemo(() => read?.routes ?? [], [read]);
  const planOf = useMemo(() => new Map((read?.plans ?? []).map((p) => [p.route, p])), [read]);
  // The routes whose days and hours this look falls in. The one remembered
  // was the board all day once pressed: 출근 at six in the evening, with 퇴근
  // a press away.
  const inHours = useMemo(() => inHoursAt(routes, lookedAt), [routes, lookedAt]);
  const hours = keyOf(inHours);
  // The board: the route pressed while these hours stand; else the one they
  // are the hours of (of several, the one to leave for soonest); else the one
  // last picked in this browser; else the one to leave for soonest.
  const current = useMemo(() => {
    if (!routes.length) return null;
    const byId = (id: string | null | undefined) => routes.find((r) => r.id === id);
    const held = chosen?.under === hours ? byId(chosen.id) : undefined;
    return held ?? soonestOf(inHours, planOf) ?? inHours[0] ?? byId(picked) ?? soonestOf(routes, planOf) ?? routes[0];
  }, [routes, chosen, hours, inHours, picked, planOf]);

  function pick(id: string) {
    setChosen({ id, under: hours });
    setPicked(id);
    try {
      localStorage.setItem(PICK_KEY, id);
    } catch {}
  }

  async function keep(next: Route[]) {
    const adding = editing === null;
    const saved = await saveRoutes(who, next);
    setEditing(undefined);
    departures.mutate((r) => (r ? { ...r, routes: saved } : r));
    // A route just added is what one wants to see - now, not remembered over
    // the one picked.
    if (adding && saved.length) setChosen({ id: saved[saved.length - 1].id, under: keyOf(inHoursAt(saved, lookedAt)) });
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
              <Failed onRetry={load} className="pt-10">
                {error}
              </Failed>
            ) : (
              <Placeholder />
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
                {stale ? (
                  <AsOf at={departures.readAt} seconds failed={departures.error !== null} onRetry={() => void load()} className="mb-3" />
                ) : null}
                {current ? (
                  <TabsContent value={current.id}>
                    <Board who={who} route={current} plan={planOf.get(current.id)} now={now} stale={stale} />
                  </TabsContent>
                ) : null}
              </Tabs>
              <Keys feeds={read.feeds} routes={routes} owner={who === "owner"} />

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
                        {/* Short names line up at 48px; a long one (up to twenty
                            letters) ends in "…" instead of folding into a tall column. */}
                        <span className={cn("max-w-[40%] min-w-12 shrink-0 truncate text-body", current?.id === r.id ? "text-foreground" : "text-ink")}>
                          {r.name}
                        </span>
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
                {/* At the most, said - a greyed button did not say why. */}
                {routes.length >= 12 ? (
                  <p className="mt-3 px-1 text-aside text-faint">경로는 12개까지 둘 수 있어요.</p>
                ) : (
                  <button
                    type="button"
                    onClick={() => setEditing(null)}
                    className="mt-2 inline-flex h-11 items-center gap-1.5 rounded-lg px-1 text-ui text-dim hover:text-foreground focus-ring mouse:h-9"
                  >
                    <Plus aria-hidden className="size-4" strokeWidth={1.8} />
                    경로 추가
                  </button>
                )}
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

/** The routes whose days and hours a moment falls in. */
function inHoursAt(routes: Route[], at: number): Route[] {
  const when = new Date(at);
  return routes.filter((r) => active(r, when));
}

/** Which routes' hours stand: a press is held under it. */
const keyOf = (list: Route[]) => list.map((r) => r.id).join(" ");

/** Of these routes, the one to leave for soonest; none when none has a time to leave. */
function soonestOf(list: Route[], planOf: Map<string, Plan>): Route | undefined {
  return [...list]
    .filter((r) => planOf.get(r.id)?.leave)
    .sort((a, b) => Date.parse(planOf.get(a.id)!.leave!) - Date.parse(planOf.get(b.id)!.leave!))[0];
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

/**
 * The board while it is read: still blocks of the face and its words, the
 * count, the rail and the line to catch - what comes arrives into them.
 */
function Placeholder() {
  return (
    <div className="pt-1">
      <p role="status" className="sr-only">
        {SAY.loading}
      </p>
      <div aria-hidden>
        <div className="flex items-start gap-3">
          <span className="mt-1 size-10 shrink-0 rounded-full bg-well" />
          <span className="h-11 w-44 rounded-[12px] bg-well" />
        </div>
        <div className="mt-4 h-20 w-52 rounded-lg bg-well" />
        <div className="mt-2 h-4 w-24 rounded bg-well" />
        <div className="mt-6 h-14 rounded-lg bg-well" />
        <div className="mt-4 flex items-center justify-between gap-3">
          <span className="h-6 w-44 rounded bg-well" />
          <span className="h-4 w-16 rounded bg-well" />
        </div>
        <div className="mt-1.5 h-3.5 w-32 rounded bg-well" />
      </div>
    </div>
  );
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

/**
 * Which live feed has no key yet, of the kinds one's routes take - said once,
 * under the board. Where the key goes is the operator's to know; the guest,
 * who can do nothing about it, hears only that it is not connected yet.
 */
function Keys({ feeds, routes, owner }: { feeds: { buses: boolean; subway: boolean }; routes: Route[]; owner: boolean }) {
  const missing = [
    !feeds.buses && routes.some((r) => r.stop.kind === "bus") && "버스",
    !feeds.subway && routes.some((r) => r.stop.kind === "subway") && "지하철",
  ]
    .filter(Boolean)
    .join("·");
  if (!missing) return null;
  return (
    <p className="mt-6 text-aside leading-[1.55] break-keep text-faint">
      {owner
        ? `${missing} 실시간 정보 키가 아직 없어 그 경로는 시각을 세지 못해요. 키를 맥미니에 넣으면 바로 이어져요.`
        : `${missing} 실시간 정보가 아직 연결되지 않아 그 경로는 시각을 세지 못해요.`}
    </p>
  );
}
