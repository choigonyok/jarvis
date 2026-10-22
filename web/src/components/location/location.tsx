"use client";

import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ChevronLeft, ChevronRight, Pause, Play } from "lucide-react";
import { Header, TabBar } from "@/components/shell/header";
import { Retry } from "@/components/shell/retry";
import { StandingBar } from "@/components/shell/standing-bar";
import { DayMap } from "@/components/location/day-map";
import { useRole } from "@/components/shell/role";
import { Strip } from "@/components/location/strip";
import { onResume } from "@/lib/resume";
import { isPending } from "@/lib/thread";
import {
  type Day,
  type Track,
  MODE_WORD,
  ago,
  buildDay,
  dateLabel,
  distance,
  headAt,
  hm,
  itemAt,
  readTrack,
  shiftDay,
  span,
  todayOf,
  toward,
} from "@/lib/track";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

type Read = { date: string; track?: Track; error?: string; at: number };

/**
 * Where the operator went in a day, replayed (context-svc's fixes from the
 * phone's OwnTracks). It opens on now: the whole of today as light on the
 * map, and where they are. ▶ replays the day from midnight - slowly where
 * they moved, quickly where they stayed - and lands back on now. Other days
 * are a tap back, as far as the fixes are kept.
 */
export function Location() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const [today, setToday] = useState(() => todayOf(Date.now()));
  const [date, setDate] = useState(today);
  const [read, setRead] = useState<Read | null>(null);

  const load = useCallback(
    () =>
      readTrack(date).then(
        (track) => setRead({ date, track, at: Date.now() }),
        (e: Error) =>
          // A read that fails keeps what was shown of the same day.
          setRead((r) => ({ date, error: e.message, at: Date.now(), track: r?.date === date ? r.track : undefined })),
      ),
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

  const day = useMemo(() => (read?.track ? buildDay(read.track, read.at) : null), [read]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />
      {day ? (
        <Viewer day={day} date={date} today={today} onDate={setDate} loading={read?.date !== date} />
      ) : (
        <Waiting read={read} date={date} onRetry={load} />
      )}
      <TabBar pending={waiting.length} />
    </div>
  );
}

/** Before the first day has come back: the first read, or why it did not come. */
function Waiting({ read, date, onRetry }: { read: Read | null; date: string; onRetry: () => unknown }) {
  return (
    <main className="inset-x-safe flex min-h-0 flex-1 flex-col items-center justify-center px-8 pb-tabbar text-center">
      {read?.error && read.date === date ? (
        <div>
          <p className="text-[20px] leading-[1.3] font-medium text-foreground">위치를 불러오지 못했어요</p>
          <p role="alert" className="mt-2 max-w-[30rem] text-[13.5px] leading-[1.6] break-keep text-pretty text-dim">
            {read.error} 계속 이러면 상태 탭에서 위치 서비스(context)가 응답하는지 보세요.
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
function usePlayback(day: Day) {
  const [t, setT] = useState(day.end);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(1);
  const frame = useRef(0);
  // What the frame loop reads, so it is not set up again sixty times a second.
  const current = useRef({ day, t, speed });
  useEffect(() => {
    current.current = { day, t, speed };
  });

  const pause = useCallback(() => {
    cancelAnimationFrame(frame.current);
    setPlaying(false);
  }, []);

  const play = useCallback(() => {
    const { day, t } = current.current;
    if (day.clock.length <= 0) return;
    cancelAnimationFrame(frame.current);
    // From the start again once at the end.
    let pos = t >= day.end ? 0 : day.clock.replayAt(t);
    let last = performance.now();
    setPlaying(true);
    if (t >= day.end) setT(day.start);
    const tick = (now: number) => {
      const { day, speed } = current.current;
      // A frame held back (the app in the background) does not jump the day forward.
      pos += Math.min(100, now - last) * speed;
      last = now;
      if (pos >= day.clock.length) {
        setT(day.end);
        setPlaying(false);
        return;
      }
      setT(day.clock.realAt(pos));
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

  // Another day opens on its end; today read again keeps the moment shown,
  // and at the end it follows the end - now - as it moves on.
  const was = useRef(day);
  useEffect(() => {
    const before = was.current;
    was.current = day;
    if (before === day) return;
    if (before.date !== day.date) {
      cancelAnimationFrame(frame.current);
      setPlaying(false);
      setT(day.end);
      return;
    }
    setT((cur) => (cur >= before.end ? day.end : Math.min(cur, day.end)));
  }, [day]);

  useEffect(() => () => cancelAnimationFrame(frame.current), []);

  return { t, playing, speed, setSpeed, play, pause, seek };
}

function Viewer({
  day,
  date,
  today,
  onDate,
  loading,
}: {
  day: Day;
  date: string;
  today: string;
  onDate: (d: string) => void;
  loading: boolean;
}) {
  const { t, playing, speed, setSpeed, play, pause, seek } = usePlayback(day);
  const atEnd = t >= day.end;
  const live = day.today && atEnd && !playing;
  const first = earliest(day, today);

  return (
    <div className="flex min-h-0 flex-1 flex-col sm:landscape:flex-row">
      <div className="relative min-h-0 flex-1">
        <DayMap day={day} t={t} live={live} follow={playing} />
        <Empty day={day} />
      </div>
      <aside className="inset-x-safe flex shrink-0 flex-col border-t border-edge-soft bg-background pb-tabbar sm:landscape:w-[22rem] sm:landscape:border-t-0 sm:landscape:border-l sm:landscape:pl-0 lg:landscape:w-[25rem]">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-2 pb-3 sm:px-8 sm:landscape:px-5 sm:landscape:pt-4">
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
              {live ? "지금" : hm(t, day.dayEnd)}
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
                disabled={day.clock.length <= 0 || day.nodes.length === 0}
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
          <p aria-live={playing ? "off" : "polite"} className="mt-1.5 truncate text-[13.5px] text-dim">
            {sentence(day, t, live)}
          </p>

          <div className="mt-3">
            <Strip day={day} t={t} onSeek={seek} onHold={(held) => held && pause()} />
          </div>
        </div>
        <DayList day={day} t={t} onSeek={seek} />
      </aside>
    </div>
  );
}

/** How far back a day can be opened: the oldest day with fixes still kept. */
function earliest(day: Day, today: string): string {
  const since = day.since;
  const keep = shiftDay(today, -(day.keepDays - 1));
  return since && since > keep ? since : keep;
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

/** Over the map when there is nothing on it: why, and what to do about it. */
function Empty({ day }: { day: Day }) {
  const role = useRole();
  if (day.nodes.length > 0) return null;
  const never = !day.since;
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center px-8">
      <div className="pointer-events-auto max-w-[22rem] rounded-xl bg-popover px-5 py-4 text-center shadow-[0_10px_30px_-12px_oklch(0_0_0/80%)]">
        <p className="text-[14.5px] text-foreground/90">
          {never ? "아직 폰 위치가 들어온 적이 없어요" : day.today ? "오늘은 아직 위치 기록이 없어요" : "이날은 위치 기록이 없어요"}
        </p>
        <p className="mt-1 text-[12.5px] leading-[1.55] break-keep text-pretty text-dim">
          {never && role === "guest" ? (
            "폰이 위치를 보내기 시작하면 여기 그려져요."
          ) : never ? (
            <>
              아이폰의 OwnTracks 가 위치를 보내야 여기 그려져요.{" "}
              <Link href="/status?focus=check:phone" className="underline decoration-edge underline-offset-[3px] hover:text-foreground">
                상태 탭의 폰 연결
              </Link>
              에서 설정하세요.
            </>
          ) : (
            `위치는 ${day.keepDays}일 동안만 남겨요.`
          )}
        </p>
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
 * way, with its hours; a row goes to its moment.
 */
function DayList({ day, t, onSeek }: { day: Day; t: number; onSeek: (t: number) => void }) {
  if (day.items.length === 0) return null;
  const moves = day.items.filter((it) => it.type === "move");
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
        {day.items.map((it) => {
          const active = it.start <= t && t <= it.end;
          const from = it.start <= day.start ? "00:00" : hm(it.start);
          const to = it.type === "stay" && it.ongoing ? "지금" : hm(it.end, day.dayEnd);
          return (
            <li key={`${it.type}${it.start}`}>
              <button
                type="button"
                onClick={() => onSeek(Math.max(it.start, day.start))}
                aria-current={active ? "true" : undefined}
                className={cn(
                  "flex min-h-11 w-full items-baseline gap-3 rounded-lg px-3 py-2.5 text-left transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                  active ? "bg-glass-raised" : "hover:bg-glass",
                )}
              >
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
