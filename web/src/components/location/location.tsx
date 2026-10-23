"use client";

import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ChevronLeft, ChevronRight, Info, LocateFixed, Pause, Play } from "lucide-react";
import { Header, TabBar } from "@/components/shell/header";
import { Retry } from "@/components/shell/retry";
import { useRole } from "@/components/shell/role";
import { StandingBar } from "@/components/shell/standing-bar";
import { PhoneSetup } from "@/components/status/phone-setup";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { DayMap, type Spot } from "@/components/location/day-map";
import { ModeTag, ModesSheet } from "@/components/location/phone-mode";
import { type Here, RegisterPanel } from "@/components/location/register";
import { Strip } from "@/components/location/strip";
import { onResume } from "@/lib/resume";
import { isPending } from "@/lib/thread";
import {
  type Day,
  type Person,
  type Scene,
  type Track,
  type Who,
  MODE_WORD,
  ago,
  buildDay,
  dateLabel,
  distance,
  headAt,
  hm,
  itemAt,
  readTrack,
  sceneOf,
  shiftDay,
  span,
  todayOf,
  toward,
} from "@/lib/track";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

/** Whose day is on the screen: the viewer's, both, or the other's - the calendar's 나 · 함께 · 상대. */
type View = "me" | "both" | "partner";

const VIEWS: { view: View; label: string; dot: string }[] = [
  { view: "me", label: "나", dot: "bg-mine" },
  { view: "both", label: "함께", dot: "owner-shared" },
  { view: "partner", label: "상대", dot: "bg-partner" },
];

/** The view a person left the screen on, kept in their own browser. */
const VIEW_KEY = "jarvis:location-view";

type Read = {
  date: string;
  at: number;
  tracks: Partial<Record<Who, Track>>;
  errors: Partial<Record<Who, string>>;
};

/**
 * Where the operator and the guest went in a day, replayed (context-svc's
 * fixes from each one's OwnTracks). It opens on now: the whole of today as
 * light on the map, and where they are. ▶ replays the day from midnight -
 * slowly where they moved, quickly where they stayed - and lands back on now.
 * Each person's day can be seen alone, or both together on one map in the
 * shared calendar's colors. Other days are a tap back, as far as the fixes
 * are kept.
 */
export function Location() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const role = useRole();
  const me: Who = role === "guest" ? "guest" : "owner";
  const partner: Who = me === "owner" ? "guest" : "owner";
  const [today, setToday] = useState(() => todayOf(Date.now()));
  const [date, setDate] = useState(today);
  const [view, setView] = useState<View>(() => {
    // Where this person left it; the guest first sees the operator's day.
    const fallback: View = role === "guest" ? "partner" : "me";
    if (typeof window === "undefined") return fallback;
    try {
      const kept = localStorage.getItem(VIEW_KEY);
      return kept === "me" || kept === "both" || kept === "partner" ? kept : fallback;
    } catch {
      return fallback;
    }
  });
  const [read, setRead] = useState<Read | null>(null);

  function choose(next: View) {
    setView(next);
    try {
      localStorage.setItem(VIEW_KEY, next);
    } catch {}
  }

  // Both days, always: switching between 나 · 함께 · 상대 has nothing to wait for.
  const load = useCallback(
    () =>
      Promise.allSettled([readTrack(date, "owner"), readTrack(date, "guest")]).then((results) => {
        const at = Date.now();
        setRead((r) => {
          const tracks: Read["tracks"] = {};
          const errors: Read["errors"] = {};
          (["owner", "guest"] as const).forEach((who, i) => {
            const res = results[i];
            if (res.status === "fulfilled") {
              tracks[who] = res.value;
              return;
            }
            errors[who] = res.reason instanceof Error ? res.reason.message : "위치를 읽지 못했어요.";
            // A read that fails keeps what was shown of the same day.
            if (r?.date === date && r.tracks[who]) tracks[who] = r.tracks[who];
          });
          return { date, at, tracks, errors };
        });
      }),
    [date],
  );
  useEffect(() => {
    void load();
  }, [load]);
  // Back to the app, or a pull to refresh.
  useEffect(
    () =>
      onResume(() => {
        setToday(todayOf(Date.now()));
        return load();
      }),
    [load],
  );
  // Today moves on: read it again every minute while it is on screen. Past
  // midnight it is yesterday, and the screen says so (오늘로).
  useEffect(() => {
    const id = window.setInterval(() => {
      const now = todayOf(Date.now());
      setToday(now);
      if (document.visibilityState === "visible" && date === now) void load();
    }, 60_000);
    return () => window.clearInterval(id);
  }, [date, load]);

  const days = useMemo(() => {
    const out: Partial<Record<Who, Day>> = {};
    if (!read) return out;
    for (const who of ["owner", "guest"] as const) {
      const track = read.tracks[who];
      if (track) out[who] = buildDay(track, read.at);
    }
    return out;
  }, [read]);

  const scene = useMemo(() => {
    const pick = (who: Who): Person[] => {
      const day = days[who];
      return day ? [{ who, me: who === me, day }] : [];
    };
    const people = view === "me" ? pick(me) : view === "partner" ? pick(partner) : [...pick(me), ...pick(partner)];
    return people.length ? sceneOf(people) : null;
  }, [days, view, me, partner]);

  const shown: Who[] = view === "me" ? [me] : view === "partner" ? [partner] : [me, partner];
  const error = shown.map((who) => read?.errors[who]).find(Boolean);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />
      {scene ? (
        <Viewer
          scene={scene}
          date={date}
          today={today}
          onDate={setDate}
          loading={read?.date !== date}
          view={view}
          onView={choose}
          me={me}
          tracks={read?.tracks ?? {}}
          readAt={read?.at ?? 0}
          onReload={load}
        />
      ) : (
        <Waiting error={read?.date === date ? error : undefined} onRetry={load} view={view} onView={choose} />
      )}
      <TabBar pending={waiting.length} />
    </div>
  );
}

/** Before a day has come back: the first read, or why it did not come. */
function Waiting({
  error,
  onRetry,
  view,
  onView,
}: {
  error?: string;
  onRetry: () => unknown;
  view: View;
  onView: (v: View) => void;
}) {
  return (
    <main className="inset-x-safe relative flex min-h-0 flex-1 flex-col items-center justify-center px-8 pb-tabbar text-center">
      <ViewSwitch view={view} onView={onView} />
      {error ? (
        <div>
          <p className="text-[20px] leading-[1.3] font-medium text-foreground">위치를 불러오지 못했어요</p>
          <p role="alert" className="mt-2 max-w-[30rem] text-[13.5px] leading-[1.6] break-keep text-pretty text-dim">
            {error} 계속 이러면 상태 탭에서 위치 서비스(context)가 응답하는지 보세요.
          </p>
          <Retry onRetry={onRetry} />
        </div>
      ) : (
        <p className="text-[13px] text-faint">불러오는 중이에요.</p>
      )}
    </main>
  );
}

/** The replay: where the moment shown is, and how it moves. */
function usePlayback(scene: Scene) {
  const [t, setT] = useState(scene.end);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(1);
  const frame = useRef(0);
  // What the frame loop reads, so it is not set up again sixty times a second.
  const current = useRef({ scene, t, speed });
  useEffect(() => {
    current.current = { scene, t, speed };
  });

  const pause = useCallback(() => {
    cancelAnimationFrame(frame.current);
    setPlaying(false);
  }, []);

  const play = useCallback(() => {
    const { scene, t } = current.current;
    if (scene.clock.length <= 0) return;
    cancelAnimationFrame(frame.current);
    // From the start again once at the end.
    let clock = scene.clock;
    let shown = t >= scene.end ? scene.start : t;
    let pos = clock.replayAt(shown);
    let last = performance.now();
    setPlaying(true);
    if (t >= scene.end) setT(scene.start);
    const tick = (now: number) => {
      const { scene, speed } = current.current;
      // Another clock - a person shown or put away mid-replay: on from the moment shown.
      if (scene.clock !== clock) {
        clock = scene.clock;
        pos = clock.replayAt(shown);
      }
      // A frame held back (the app in the background) does not jump the day forward.
      pos += Math.min(100, now - last) * speed;
      last = now;
      if (pos >= clock.length) {
        setT(scene.end);
        setPlaying(false);
        return;
      }
      shown = clock.realAt(pos);
      setT(shown);
      frame.current = requestAnimationFrame(tick);
    };
    frame.current = requestAnimationFrame(tick);
  }, []);

  const seek = useCallback(
    (to: number) => {
      pause();
      setT(to);
    },
    [pause],
  );

  // Another day opens on its end; the same day read again keeps the moment
  // shown, and at the end it follows the end - now - as it moves on.
  const was = useRef(scene);
  useEffect(() => {
    const before = was.current;
    was.current = scene;
    if (before === scene) return;
    if (before.date !== scene.date) {
      cancelAnimationFrame(frame.current);
      setPlaying(false);
      setT(scene.end);
      return;
    }
    setT((cur) => (cur >= before.end ? scene.end : Math.min(cur, scene.end)));
  }, [scene]);

  useEffect(() => () => cancelAnimationFrame(frame.current), []);

  return { t, playing, speed, setSpeed, play, pause, seek };
}

function Viewer({
  scene,
  date,
  today,
  onDate,
  loading,
  view,
  onView,
  me,
  tracks,
  readAt,
  onReload,
}: {
  scene: Scene;
  date: string;
  today: string;
  onDate: (d: string) => void;
  loading: boolean;
  view: View;
  onView: (v: View) => void;
  me: Who;
  tracks: Partial<Record<Who, Track>>;
  readAt: number;
  onReload: () => Promise<void>;
}) {
  const { t, playing, speed, setSpeed, play, pause, seek } = usePlayback(scene);
  const [connecting, setConnecting] = useState(false);
  // 위치 등록: the panel under the map asks the name while the map shows the
  // circle; ⓘ says what it does and how each phone watches now.
  const [registering, setRegistering] = useState(false);
  const [spot, setSpot] = useState<Spot | null>(null);
  const [explaining, setExplaining] = useState(false);
  const atEnd = t >= scene.end;
  const live = scene.today && atEnd && !playing;
  const first = earliest(scene, today);
  const both = scene.people.length > 1;
  const still = scene.people.every((p) => p.day.nodes.length === 0);
  const mine = tracks[me];
  // How each phone watches is now's to say: on today's screen, at now.
  const watchOf = (p: Person) => (live ? tracks[p.who]?.watch : undefined);

  function register() {
    setExplaining(false);
    pause();
    if (scene.today) seek(scene.end);
    setRegistering(true);
  }
  function stopRegistering() {
    setRegistering(false);
    setSpot(null);
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col sm:landscape:flex-row">
      <div className="relative min-h-0 flex-1">
        <DayMap scene={scene} t={t} live={live} follow={playing} spot={spot} />
        <ViewSwitch view={view} onView={onView} />
        {registering ? null : (
          <>
            <Empty scene={scene} onConnect={() => setConnecting(true)} />
            <PlaceButtons onExplain={() => setExplaining(true)} onRegister={register} />
          </>
        )}
      </div>
      <aside className="inset-x-safe flex shrink-0 flex-col border-t border-edge-soft bg-background pb-tabbar sm:landscape:w-[22rem] sm:landscape:border-t-0 sm:landscape:border-l sm:landscape:pl-0 lg:landscape:w-[25rem]">
        {registering ? (
          <div className="mx-auto w-full max-w-[42rem] px-4 pt-3 pb-4 sm:px-8 sm:landscape:px-5 sm:landscape:pt-4">
            <RegisterPanel
              who={me}
              names={(mine?.regions ?? []).map((r) => r.name)}
              fallback={lastFix(mine)}
              readAt={readAt}
              onSpot={setSpot}
              onClose={stopRegistering}
              onMade={() => void onReload()}
            />
          </div>
        ) : null}
        <div
          className={cn(
            "mx-auto w-full max-w-[42rem] px-4 pt-2 pb-3 sm:px-8 sm:landscape:px-5 sm:landscape:pt-4",
            registering && "hidden",
          )}
        >
          <div className="flex items-center justify-between gap-2">
            <div className="-ml-3 flex min-w-0 items-center">
              <Arrow label="전날" disabled={date <= first} onClick={() => onDate(shiftDay(date, -1))}>
                <ChevronLeft aria-hidden className="size-[18px]" strokeWidth={1.8} />
              </Arrow>
              <label className="relative flex min-h-11 items-center rounded-lg px-1 text-[14px] text-foreground/90 focus-within:ring-3 focus-within:ring-ring/50">
                {dateLabel(date)}
                <input
                  type="date"
                  aria-label="날짜 고르기"
                  min={first}
                  max={today}
                  value={date}
                  onChange={(e) => e.target.value && onDate(e.target.value)}
                  // A click on the hidden field itself opens no calendar on a desktop browser.
                  onClick={(e) => e.currentTarget.showPicker?.()}
                  className="absolute inset-0 cursor-pointer opacity-0"
                />
              </label>
              <Arrow label="다음 날" disabled={date >= today} onClick={() => onDate(shiftDay(date, 1))}>
                <ChevronRight aria-hidden className="size-[18px]" strokeWidth={1.8} />
              </Arrow>
            </div>
            {date === today ? (
              <span className="text-[12px] text-faint">{loading ? "불러오는 중…" : "오늘"}</span>
            ) : (
              <button
                type="button"
                onClick={() => onDate(today)}
                className="tap -mr-2 rounded-lg px-2 text-[12.5px] text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-0 mouse:min-w-0 mouse:py-1"
              >
                {loading ? "불러오는 중…" : "오늘로"}
              </button>
            )}
          </div>

          <div className="mt-0.5 flex items-center justify-between gap-4">
            <p className="tnum text-[40px] leading-none font-medium tracking-[-0.025em] text-foreground">
              {live ? "지금" : hm(t, scene.dayEnd)}
            </p>
            <div className="flex shrink-0 items-center gap-1">
              <button
                type="button"
                onClick={() => setSpeed(speed === 4 ? 1 : speed * 2)}
                aria-label={`재생 속도 ${speed}배`}
                className="tnum tap rounded-full px-2 text-[12.5px] text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                {speed}×
              </button>
              <button
                type="button"
                onClick={playing ? pause : play}
                disabled={scene.clock.length <= 0 || still}
                aria-label={playing ? "멈춤" : atEnd ? "하루 처음부터 보기" : "이어서 보기"}
                className="flex size-12 items-center justify-center rounded-full bg-glass-raised text-foreground transition-colors outline-none hover:bg-foreground/15 focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-40"
              >
                {playing ? (
                  <Pause aria-hidden className="size-5" strokeWidth={1.8} fill="currentColor" />
                ) : (
                  <Play aria-hidden className="ml-0.5 size-5" strokeWidth={1.8} fill="currentColor" />
                )}
              </button>
            </div>
          </div>
          {/* The whole width: the line runs long while a way is being gone. */}
          {both ? (
            <div aria-live={playing ? "off" : "polite"} className="mt-1.5 space-y-0.5">
              {scene.people.map((p) => (
                <p key={p.who} className="flex min-w-0 items-center gap-2 text-[13.5px] text-dim">
                  <span aria-hidden className={cn("size-[6px] shrink-0 rounded-full", p.me ? "bg-mine" : "bg-partner")} />
                  <span className="sr-only">{p.me ? "나" : "상대"}: </span>
                  <span className="truncate">{sentence(p.day, t, live)}</span>
                  <ModeTag watch={watchOf(p)} onOpen={() => setExplaining(true)} />
                </p>
              ))}
            </div>
          ) : (
            <p aria-live={playing ? "off" : "polite"} className="mt-1.5 flex min-w-0 items-center gap-2 text-[13.5px] text-dim">
              <span className="truncate">{sentence(scene.people[0].day, t, live)}</span>
              <ModeTag watch={watchOf(scene.people[0])} onOpen={() => setExplaining(true)} />
            </p>
          )}

          <div className="mt-3">
            <Strip scene={scene} t={t} onSeek={seek} onHold={(held) => held && pause()} />
          </div>
        </div>
        {registering ? null : <DayList scene={scene} t={t} onSeek={seek} />}
      </aside>

      <ModesSheet
        open={explaining}
        onOpenChange={setExplaining}
        phones={([me, me === "owner" ? "guest" : "owner"] as Who[]).map((who) => ({ who, me: who === me, track: tracks[who] }))}
        today={scene.today}
        readAt={readAt}
        onRegister={register}
      />

      <Sheet open={connecting} onOpenChange={setConnecting}>
        <SheetContent side="bottom" className="inset-x-safe pb-safe max-h-[88dvh] overflow-y-auto border-edge-soft bg-background">
          <SheetHeader className="px-4 pt-4 pb-1">
            <SheetTitle className="text-[14px] font-medium text-foreground">내 폰 연결</SheetTitle>
          </SheetHeader>
          <div className="px-4 pb-6 text-[13px]">
            <PhoneSetup who="guest" />
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
}

/**
 * ⓘ and 위치 등록, over the map's bottom right: register where the viewer's own
 * phone is now as one of its regions, or read what that does and how the
 * phones watch.
 */
function PlaceButtons({ onExplain, onRegister }: { onExplain: () => void; onRegister: () => void }) {
  return (
    <div className="absolute right-3 bottom-3 z-10 flex items-center gap-0.5 rounded-full bg-popover p-1 shadow-[0_6px_16px_-6px_oklch(0_0_0/70%)]">
      <button
        type="button"
        onClick={onExplain}
        aria-label="위치 등록과 모드 설명"
        title="위치 등록과 모드"
        className="flex size-9 items-center justify-center rounded-full text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 mouse:size-7"
      >
        <Info aria-hidden className="size-[17px]" strokeWidth={1.8} />
      </button>
      <button
        type="button"
        onClick={onRegister}
        className="flex h-9 items-center gap-1.5 rounded-full bg-glass-raised pr-3.5 pl-3 text-[12.5px] text-foreground transition-colors outline-none hover:bg-foreground/15 focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-7"
      >
        <LocateFixed aria-hidden className="size-4 text-mine" strokeWidth={1.8} />
        위치 등록
      </button>
    </div>
  );
}

/** The phone's last trusted fix (100 m or better), for 위치 등록 when the browser gives none. */
function lastFix(track: Track | undefined): Here | null {
  const f = [...(track?.fixes ?? [])].reverse().find((x) => x.acc <= 100) ?? track?.before;
  return f ? { lat: f.lat, lon: f.lon, acc: Math.round(f.acc), at: Date.parse(f.at), from: "phone" } : null;
}

/** 나 · 함께 · 상대, over the map's top left: the calendar's own words and dots for the two people. */
function ViewSwitch({ view, onView }: { view: View; onView: (v: View) => void }) {
  return (
    <div
      role="group"
      aria-label="누구의 하루"
      className="absolute top-3 left-3 z-10 flex rounded-full bg-popover p-1 shadow-[0_6px_16px_-6px_oklch(0_0_0/70%)]"
    >
      {VIEWS.map((v) => (
        <button
          key={v.view}
          type="button"
          aria-pressed={view === v.view}
          onClick={() => onView(v.view)}
          className={cn(
            "flex h-9 items-center gap-1.5 rounded-full px-3 text-[12.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 mouse:h-7 mouse:px-2.5",
            view === v.view ? "bg-glass-raised text-foreground" : "text-dim hover:text-foreground",
          )}
        >
          <span aria-hidden className={cn("size-[6px] rounded-full", v.dot)} />
          {v.label}
        </button>
      ))}
    </div>
  );
}

/** How far back a day can be opened: the oldest day with fixes still kept. */
function earliest(scene: Scene, today: string): string {
  const keep = shiftDay(today, -(scene.keepDays - 1));
  return scene.since && scene.since > keep ? scene.since : keep;
}

function Arrow({ label, disabled, onClick, children }: { label: string; disabled: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className="flex size-11 items-center justify-center rounded-lg text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30"
    >
      {children}
    </button>
  );
}

/**
 * Over the map when there is nothing on it: why, and what to do about it -
 * for the guest's own phone, the way to connect it.
 */
function Empty({ scene, onConnect }: { scene: Scene; onConnect: () => void }) {
  const role = useRole();
  if (scene.people.some((p) => p.day.nodes.length > 0)) return null;
  const p = scene.people[0];
  const never = !p.day.since;
  const when = p.day.today ? "오늘은 아직" : "이날은";
  let title: string;
  let body: ReactNode;
  if (p.me && role === "guest" && (never || p.day.today)) {
    title = "내 폰 위치가 아직 없어요";
    body = (
      <>
        아이폰의 OwnTracks 가 위치를 보내면 여기 내 고양이가 그려져요.
        <button
          type="button"
          onClick={onConnect}
          className="tap mt-3 flex w-full items-center justify-center rounded-lg bg-glass-raised px-4 text-[13px] text-foreground transition-colors outline-none hover:bg-foreground/15 focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-9"
        >
          내 폰 연결
        </button>
      </>
    );
  } else if (p.me && never) {
    title = "아직 폰 위치가 들어온 적이 없어요";
    body = (
      <>
        아이폰의 OwnTracks 가 위치를 보내야 여기 그려져요.{" "}
        <Link href="/status?focus=check:phone" className="underline decoration-edge underline-offset-[3px] hover:text-foreground">
          상태 탭의 폰 연결
        </Link>
        에서 설정하세요.
      </>
    );
  } else if (!p.me && never && role !== "guest") {
    title = "상대 폰이 아직 위치를 보내지 않았어요";
    body = "상대가 jarvis 위치 탭의 '내 폰 연결'로 OwnTracks 를 설정하면 여기 그려져요.";
  } else {
    title = `${when} ${p.me ? "" : "상대 "}위치 기록이 없어요`;
    body = `위치는 ${p.day.keepDays}일 동안만 남겨요.`;
  }
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center px-8">
      <div className="pointer-events-auto max-w-[22rem] rounded-xl bg-popover px-5 py-4 text-center shadow-[0_10px_30px_-12px_oklch(0_0_0/80%)]">
        <p className="text-[14.5px] text-foreground/90">{title}</p>
        <div className="mt-1 text-[12.5px] leading-[1.55] break-keep text-pretty text-dim">{body}</div>
      </div>
    </div>
  );
}

/**
 * The line under the clock: what was happening at the moment shown, as a
 * person says it. At the end of today it says where they are now.
 */
function sentence(day: Day, t: number, live: boolean): string {
  const it = itemAt(day, t);
  if (!it) {
    const first = day.nodes[0];
    if (!first) return day.today ? "오늘 들어온 위치가 없어요" : "이날 들어온 위치가 없어요";
    if (t < first.start) return `첫 위치는 ${hm(first.start)}에 들어왔어요`;
    // Today, past the last fix on the way: where it was last seen, as of
    // the read (today's end is the moment it was read).
    const last = day.nodes.at(-1)!;
    const seen = day.lastReport ?? last.leave;
    return live ? `이동 중 · 마지막 위치 ${ago(day.end - seen)}` : `마지막 위치 ${hm(last.leave)}`;
  }
  if (it.type === "stay") {
    const name = it.node.name ?? "이름 없는 곳";
    if (live && it.ongoing) return it.start <= day.start ? `${name}에 있어요` : `${name}에 있어요 · ${hm(it.start)}부터`;
    const from = it.start <= day.start ? "" : hm(it.start);
    const to = it.end >= day.dayEnd ? "" : it.ongoing ? "지금" : hm(it.end);
    const hours = from && to ? `${from}–${to}` : from ? `${from}부터` : to ? `${to}까지` : "종일";
    return `${name} · ${hours} · ${span(it.end - it.start)}`;
  }
  const where =
    it.from?.name && it.to?.name
      ? `${it.from.name}에서 ${toward(it.to.name)}`
      : it.to?.name
        ? toward(it.to.name)
        : it.from?.name
          ? `${it.from.name}에서`
          : "";
  const step = headAt(day, t)?.step;
  const mode = step?.mode ?? it.mode;
  const kmh = step && mode !== "gap" ? Math.round(step.b.vel ?? step.kmh) : null;
  return [`${where ? `${where} ` : ""}${MODE_WORD[mode]} 중`, kmh !== null && kmh > 0 ? `시속 ${kmh}km` : null]
    .filter(Boolean)
    .join(" · ");
}

/**
 * On a wide screen, the day as a list beside the strip: each stay and each
 * way, with its hours; a row goes to its moment. Both people: one list in
 * time, each row with its person's dot.
 */
function DayList({ scene, t, onSeek }: { scene: Scene; t: number; onSeek: (t: number) => void }) {
  const both = scene.people.length > 1;
  const rows = scene.people
    .flatMap((p) => p.day.items.map((it) => ({ it, p })))
    .sort((a, b) => a.it.start - b.it.start);
  if (rows.length === 0) return null;
  const moves = scene.people.length === 1 ? scene.people[0].day.items.filter((it) => it.type === "move") : [];
  const moved = moves.reduce((s, it) => s + (it.type === "move" ? it.meters : 0), 0);
  const time = moves.reduce((s, it) => s + (it.end - it.start), 0);
  return (
    <section aria-labelledby="day-list" className="hidden min-h-0 flex-1 flex-col border-t border-edge-soft sm:landscape:flex">
      <div className="flex items-baseline justify-between gap-3 px-5 pt-4 pb-1">
        <h2 id="day-list" className="text-[12px] text-faint">
          하루
        </h2>
        {moves.length ? (
          <span className="tnum text-[11.5px] text-faint">
            이동 {span(time)} · {distance(moved)}
          </span>
        ) : null}
      </div>
      <ol className="scrollbar-hairline min-h-0 flex-1 overflow-y-auto px-2 pb-6">
        {rows.map(({ it, p }) => {
          const day = p.day;
          const active = it.start <= t && t <= it.end;
          const from = it.start <= day.start ? "00:00" : hm(it.start);
          const to = it.type === "stay" && it.ongoing ? "지금" : hm(it.end, day.dayEnd);
          return (
            <li key={`${p.who}${it.type}${it.start}`}>
              <button
                type="button"
                onClick={() => onSeek(Math.max(it.start, day.start))}
                aria-current={active ? "true" : undefined}
                className={cn(
                  "flex min-h-11 w-full items-baseline gap-3 rounded-lg px-3 py-2.5 text-left transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  active ? "bg-glass-raised" : "hover:bg-glass",
                )}
              >
                {both ? (
                  <span
                    aria-label={p.me ? "나" : "상대"}
                    className={cn("size-[6px] shrink-0 -translate-y-px self-center rounded-full", p.me ? "bg-mine" : "bg-partner")}
                  />
                ) : null}
                <span className="tnum w-[6.25rem] shrink-0 text-[12px] text-faint">
                  {from}–{to}
                </span>
                <span className={cn("min-w-0 flex-1 truncate text-[14px]", it.type === "stay" ? "text-foreground/90" : "text-dim")}>
                  {it.type === "stay" ? (it.node.name ?? "이름 없는 곳") : `${MODE_WORD[it.mode]} · ${distance(it.meters)}`}
                </span>
                <span className="tnum shrink-0 text-[12px] text-faint">{span(it.end - it.start)}</span>
              </button>
            </li>
          );
        })}
      </ol>
    </section>
  );
}
