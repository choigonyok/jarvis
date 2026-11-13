"use client";

import { useKept } from "@/lib/keep";
import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { ChevronDown, ChevronLeft, ChevronRight, Images, Plus, Search } from "lucide-react";
import { EntrySheet, type Entry } from "@/components/calendar/entry-sheet";
import { EventRow, GhostRow } from "@/components/calendar/event-row";
import { EventSheet } from "@/components/calendar/event-sheet";
import { FindSheet } from "@/components/calendar/find-sheet";
import { MonthPicker } from "@/components/calendar/month-picker";
import { PhotoTiles, PickPhotos, ViewerAction, useUploads } from "@/components/couple/photos";
import { useDayPhotos, useSpace } from "@/components/couple/use-space";
import { useRole } from "@/components/shell/role";
import { Lightbox } from "@/components/ui/lightbox";
import { type DayMark } from "@/components/calendar/month-grid";
import { MonthSwipe, type MonthSwipeHandle } from "@/components/calendar/month-swipe";
import { OWNER_LABEL, OWNERS, OwnerDot, ownerOf, type Owner } from "@/components/calendar/owner";
import { Failed } from "@/components/shell/failed";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { StandingBar } from "@/components/shell/standing-bar";
import { useUndo } from "@/components/shell/undo";
import { Button } from "@/components/ui/button";
import { HOLIDAYS, between, photoSrc, writeCouple } from "@/lib/couple";
import {
  addDays,
  cursorOf,
  gridBounds,
  isoDate,
  korDate,
  monthCells,
  sameMonth,
  shiftMonth,
  type Cursor,
} from "@/lib/month";
import { onOpenParams } from "@/lib/focus";
import { ghostsFor, type CreateGhost, type Ghost } from "@/lib/proposed";
import { covers, type CalendarEvent } from "@/lib/thread";
import { useCalendar } from "@/lib/use-calendar";
import { cn } from "@/lib/utils";


/**
 * The month is the map; the selected day is the place. Tapping a date does
 * not open anything - the detail is always on screen under the grid, so the
 * calendar never costs a round trip to answer "what is on the 25th".
 */
export function Calendar() {
  const today = isoDate(new Date());
  // The day picked stays picked across a tab switch (lib/keep.ts).
  const [selected, setSelected] = useKept("calendar:selected", today);
  // A notification about an entry opens its day (?date=, lib/focus.ts); the
  // header then brings the entry itself into view.
  useEffect(() => {
    const open = (params: URLSearchParams) => {
      const date = params.get("date");
      if (date && /^\d{4}-\d{2}-\d{2}$/.test(date)) setSelected(date);
    };
    open(new URLSearchParams(window.location.search));
    return onOpenParams(open);
  }, [setSelected]);
  // The month on screen follows the selected day - there is no second piece
  // of state that could disagree with it.
  const cursor: Cursor = useMemo(() => cursorOf(selected), [selected]);

  // Whose entries are on screen. All three by default; tapping a legend chip
  // narrows the month to one person's days and back.
  const [hidden, setHidden] = useState<ReadonlySet<Owner>>(new Set());

  const { events: fetched, proposed, waiting, pendingCount, connection, error, loaded, reload, save, create, remove, decide } =
    useCalendar(cursor);

  // The operator writes entries here: a new one on the form, one of theirs
  // changed on the same form (entry-sheet.tsx). The guest's console keeps the
  // short edit sheet - the couple's space opens only on the operator's side.
  const owner = useRole() === "owner";
  const [entry, setEntry] = useState<{ event: CalendarEvent | null; day: string } | null>(null);
  const bounds = useMemo(() => gridBounds(cursor), [cursor]);
  const photos = useDayPhotos(bounds.from, bounds.to, owner);
  const [viewing, setViewing] = useState<number | null>(null);
  // The day's photo in its cell instead of dots, while the app is open.
  const [photoCells, setPhotoCells] = useKept("calendar:photos", false);
  const [finding, setFinding] = useState(false);
  const [choosingMonth, setChoosingMonth] = useState(false);
  const [photoNote, setPhotoNote] = useState<string | null>(null);
  const router = useRouter();
  // The space's blocks: where a day's photo is uploaded for, and the D-day
  // an anniversary entry belongs to.
  const space = useSpace(owner);
  const calendarBlock = space.data?.blocks.find((b) => b.type === "calendar")?.id;
  const ddayBlock = space.data?.blocks.find((b) => b.type === "dday")?.id;
  const uploads = useUploads(calendarBlock ?? null);

  async function photoChange(run: () => Promise<{ ok: boolean; error?: string }>) {
    setPhotoNote(null);
    const sent = await run();
    if (!sent.ok) setPhotoNote(sent.error ?? "사진을 바꾸지 못했어요.");
    await photos.reload();
  }

  // One of your own entries being changed in place (event-sheet.tsx). The
  // partner's are not: changing someone else's day is a conversation. Nor
  // one day of a repeating entry: the shared calendar takes that only in its
  // own app (calendar-svc refuses it).
  const [editing, setEditing] = useState<CalendarEvent | null>(null);
  // An anniversary the app writes from the D-day block is changed there.
  const editable = (e: CalendarEvent) => ownerOf(e) !== "partner" && !e.id.includes("#") && !e.origin;

  // A removal waits five seconds with 되돌리기 on screen before it is sent:
  // the row is gone at once, and an undo means the shared calendar never
  // heard of it (components/shell/undo.tsx).
  const { offer } = useUndo();
  const [leaving, setLeaving] = useState<ReadonlySet<string>>(new Set());
  const allEvents = useMemo(() => fetched.filter((e) => !leaving.has(e.id)), [fetched, leaving]);
  function erase(event: CalendarEvent) {
    const keep = (on: boolean) =>
      setLeaving((prev) => {
        const next = new Set(prev);
        if (on) next.add(event.id);
        else next.delete(event.id);
        return next;
      });
    keep(true);
    offer(
      event.recurrence ? `${event.title} - 반복 중 이 날만 지웠어요` : `${event.title} 일정을 지웠어요`,
      () => keep(false),
      () => void remove(event.id).then((gone) => !gone && keep(false)),
    );
  }

  const events = useMemo(
    () => allEvents.filter((e) => !hidden.has(ownerOf(e))),
    [allEvents, hidden],
  );

  // Counted before the filter, so a hidden chip still says what it hides.
  const monthCounts = useMemo(() => {
    const counts = new Map<Owner, number>();
    for (const e of allEvents) {
      if (!sameMonth(e.date, cursor)) continue;
      counts.set(ownerOf(e), (counts.get(ownerOf(e)) ?? 0) + 1);
    }
    return counts;
  }, [allEvents, cursor]);

  function toggle(owner: Owner) {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(owner)) next.delete(owner);
      else next.add(owner);
      return next;
    });
  }

  // Ghosts look at every event: a proposal to change a hidden entry must not
  // lose its target and reappear as something new.
  const ghosts = useMemo(() => ghostsFor(proposed, allEvents), [proposed, allEvents]);

  // An edit or a removal belongs on the event it would change; only a new
  // event needs a place of its own in the day it would land in.
  const { attached, creates } = useMemo(() => {
    const onEvents = new Map<string, Ghost>();
    const standalone: CreateGhost[] = [];
    for (const ghost of ghosts) {
      if (ghost.op === "create") standalone.push(ghost);
      else onEvents.set(ghost.targetId, ghost);
    }
    return { attached: onEvents, creates: standalone };
  }, [ghosts]);

  // One pass builds every cell's dots: what is settled, and what is waiting.
  const marks = useMemo(() => {
    const out = new Map<string, DayMark>();
    const mark = (date: string): DayMark => {
      let found = out.get(date);
      if (!found) {
        found = { owners: [], pending: false };
        out.set(date, found);
      }
      return found;
    };

    // A trip from the 3rd to the 5th is a dot on all three days, or the 4th
    // would read as free.
    for (const event of events) {
      const owner = ownerOf(event);
      for (let d = event.date; d <= (event.endDate || event.date); d = addDays(d, 1)) {
        mark(d).owners.push(owner);
      }
    }
    // Same order in every cell, so a row of days reads as a pattern.
    for (const m of out.values()) m.owners.sort((a, b) => OWNERS.indexOf(a) - OWNERS.indexOf(b));
    for (const ghost of creates) mark(ghost.preview.date).pending = true;
    for (const [targetId, ghost] of attached) {
      const target = allEvents.find((e) => e.id === targetId);
      if (target) mark(target.date).pending = true;
      if (ghost.op === "update") mark(ghost.preview.date).pending = true;
    }
    if (photoCells) {
      for (const p of photos.data ?? []) {
        const m = mark(p.date);
        if (!m.photo || p.cover) m.photo = p.asset;
      }
    }
    for (const [date, name] of Object.entries(HOLIDAYS)) if (out.has(date) || date.startsWith(selected.slice(0, 4))) mark(date).holiday = name;
    return out;
  }, [events, allEvents, creates, attached, photoCells, photos.data, selected]);

  const dayEvents = events.filter((e) => covers(e, selected));
  const dayPhotos = owner ? (photos.data ?? []).filter((p) => p.date === selected) : [];
  const dayCreates = creates.filter((g) => g.preview.date === selected);

  // Anything waiting outside the six weeks on screen would otherwise be
  // invisible; the operator is told it exists and can jump to it.
  const visible = useMemo(() => new Set(monthCells(cursor).map((c) => c.date)), [cursor]);
  const elsewhere = creates.filter((g) => !visible.has(g.preview.date));

  // The month's page: turned by a swipe, a trackpad, the arrows or PageUp/PageDown.
  const swipe = useRef<MonthSwipeHandle>(null);

  // Paging months keeps a selection: the same weekday-ish slot, clamped.
  function page(by: number) {
    const next = shiftMonth(cursor, by);
    const day = Number(selected.slice(8, 10));
    const lastDay = new Date(next.year, next.month + 1, 0).getDate();
    setSelected(
      isoDate(new Date(next.year, next.month, Math.min(day, lastDay))),
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={pendingCount} />
        <StandingBar pending={waiting} />
      </PageTop>

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar">
        {/* The month and the day it selects are one view, so on a screen wide
            enough to hold both side by side they stop taking turns down the
            page: the grid keeps its place while the day's detail changes
            beside it. Below that width the detail follows underneath, which is
            what a phone can show honestly. */}
        <div className="mx-auto grid w-full max-w-page-narrow grid-cols-1 gap-x-10 px-4 pt-5 pb-12 sm:px-8 sm:pt-6 sm:pb-6 lg:max-w-page-wide lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
          <div>
            <div className="mb-4 flex items-center justify-between">
              <button
                type="button"
                onClick={() => setChoosingMonth(true)}
                aria-label={`${cursor.year}년 ${cursor.month + 1}월 - 다른 달로`}
                className="-ml-1 inline-flex items-center gap-1 rounded-lg px-1 py-1 text-title font-medium tracking-tight text-foreground focus-ring"
              >
                {cursor.year}년 {cursor.month + 1}월
                <ChevronDown aria-hidden className="size-4 text-faint" />
              </button>
              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => setFinding(true)}
                  aria-label="일정 찾기"
                  className="tap text-dim hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
                >
                  <Search aria-hidden className="size-[18px] mouse:size-4" />
                </Button>
                {owner ? (
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => setEntry({ event: null, day: selected })}
                    aria-label="일정 추가"
                    className="tap text-dim hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
                  >
                    <Plus aria-hidden className="size-5 mouse:size-4" />
                  </Button>
                ) : null}
                {sameMonth(today, cursor) ? null : (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setSelected(today)}
                    className="h-10 px-3 text-ui text-dim hover:text-foreground mouse:h-7 mouse:px-2.5 mouse:text-aside"
                  >
                    오늘
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => (swipe.current ? swipe.current.go(-1) : page(-1))}
                  aria-label="이전 달"
                  className="tap text-faint hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
                >
                  <ChevronLeft aria-hidden className="size-5 mouse:size-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => (swipe.current ? swipe.current.go(1) : page(1))}
                  aria-label="다음 달"
                  className="tap text-faint hover:text-foreground mouse:size-7 mouse:min-h-0 mouse:min-w-0"
                >
                  <ChevronRight aria-hidden className="size-5 mouse:size-4" />
                </Button>
              </div>
            </div>

            <MonthSwipe
              ref={swipe}
              cursor={cursor}
              selected={selected}
              today={today}
              marks={marks}
              onSelect={setSelected}
              onPage={page}
            />

            {/* The legend is also the filter: the key to the colors is the
                place you would reach for to see only one person's days. */}
            <div className="mt-3 flex flex-wrap gap-1.5" role="group" aria-label="누구의 일정을 볼지">
              {OWNERS.map((owner) => {
                const on = !hidden.has(owner);
                const count = monthCounts.get(owner) ?? 0;
                return (
                  <button
                    key={owner}
                    type="button"
                    aria-pressed={on}
                    onClick={() => toggle(owner)}
                    className={cn(
                      "tap-y flex h-9 items-center gap-2 rounded-full border px-3 text-aside transition-colors outline-none mouse:h-7 mouse:px-2.5",
                      "focus-ring",
                      on
                        ? "border-edge bg-glass text-ink hover:bg-glass-raised"
                        : "border-edge-soft text-faint hover:text-dim",
                    )}
                  >
                    <OwnerDot owner={owner} className={cn(!on && "opacity-30")} />
                    {OWNER_LABEL[owner]}
                    <span className="tnum text-faint">{count}</span>
                  </button>
                );
              })}
              {owner ? (
                // The days' photos in their cells, in place of the dots.
                <button
                  type="button"
                  aria-pressed={photoCells}
                  onClick={() => setPhotoCells((v) => !v)}
                  className={cn(
                    "tap-y flex h-9 items-center gap-1.5 rounded-full border px-3 text-aside transition-colors outline-none mouse:h-7 mouse:px-2.5",
                    "focus-ring",
                    photoCells ? "border-edge bg-glass-raised text-foreground" : "border-edge-soft text-faint hover:text-dim",
                  )}
                >
                  <Images aria-hidden className="size-3.5" />
                  사진
                </button>
              ) : null}
            </div>

            {elsewhere.length > 0 ? (
              <p className="mt-4 flex items-center gap-2 text-aside text-dim">
                <span aria-hidden className="anim-breathe size-[5px] rounded-full bg-dim" />
                이 달 밖에 결재 대기 {elsewhere.length}건
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => setSelected(elsewhere[0].preview.date)}
                  className="h-8 px-2.5 text-aside text-dim hover:text-foreground mouse:h-6 mouse:px-2"
                >
                  보기
                </Button>
              </p>
            ) : null}
          </div>

          <section
            className="mt-7 border-t border-edge-soft pt-5 lg:mt-0 lg:border-t-0 lg:border-l lg:border-edge-soft lg:pt-0 lg:pl-10"
            aria-live="polite"
            data-focus="day"
          >
            {/* A row is a time, a title and a way to remove it. Let the column
                run to 1000px and those three sit a screen apart. */}
            <div className="lg:max-w-[26rem]">
            <h3 className="mb-3 px-2 text-ui text-dim">
              {korDate(selected)}
              {selected === today ? <span className="text-faint"> · 오늘</span> : null}
              {HOLIDAYS[selected] ? <span className="text-faint"> · {HOLIDAYS[selected]}</span> : null}
            </h3>

            {owner ? (
              // The day's photos: tap one to look at it, make it the day's face
              // or take it off; a new one goes on the day itself.
              <div className="mb-3 px-2">
                <PhotoTiles
                  assets={dayPhotos.map((p) => ({
                    key: p.id,
                    asset: p.asset,
                    mark: p.cover ? <span className="rounded bg-black/60 px-1 text-tiny text-white">대표</span> : undefined,
                  }))}
                  onOpen={setViewing}
                  trailing={
                    calendarBlock ? (
                      <PickPhotos
                        variant="quiet"
                        label={dayPhotos.length ? "사진" : "이 날에 사진 추가"}
                        busy={uploads.busy}
                        onPick={(files) =>
                          void uploads.upload(files).then(async (assets) => {
                            for (const asset of assets) {
                              const sent = await writeCouple("POST", "/photos", { date: selected, asset });
                              if (!sent.ok) {
                                setPhotoNote(sent.error);
                                break;
                              }
                            }
                            await photos.reload();
                          })
                        }
                      />
                    ) : null
                  }
                />
                {photoNote || uploads.error ? (
                  <p role="alert" className="mt-1 text-aside text-reject">
                    {photoNote ?? uploads.error}
                  </p>
                ) : null}
              </div>
            ) : null}

            {dayEvents.length === 0 && dayCreates.length === 0 ? (
              owner ? (
                // An empty day is a place to write one, here or in the conversation.
                <div className="px-2">
                  <p className="text-ui leading-relaxed text-faint">비어 있는 날이에요.</p>
                  <Button variant="quiet" size="touch-sm" onClick={() => setEntry({ event: null, day: selected })} className="-ml-2 gap-1.5">
                    <Plus aria-hidden className="size-4" />
                    일정 추가
                  </Button>
                </div>
              ) : (
                <p className="px-2 text-ui leading-relaxed text-faint">비어 있는 날이에요. 일정은 대화로 Jarvis에게 맡기세요.</p>
              )
            ) : null}

            <div className="space-y-1.5">
              {dayEvents.map((event) => {
                const ghost = attached.get(event.id);
                return (
                  // A repeating entry's days are "<series>#<date>"; a
                  // notification names the series.
                  <div key={event.id} className="space-y-1.5" data-focus={`event:${event.id.split("#")[0]}`}>
                    <EventRow
                      event={event}
                      pending={ghost}
                      onEdit={
                        event.origin === "dday" && owner && ddayBlock
                          ? () => router.push(`/couple/${ddayBlock}`)
                          : editable(event) && !ghost
                            ? () => (owner ? setEntry({ event, day: event.date }) : setEditing(event))
                            : undefined
                      }
                      onDelete={() => erase(event)}
                    />
                    {ghost ? (
                      <GhostRow
                        ghost={ghost}
                        onDecide={(d, reason) => decide(ghost.proposal.id, d, reason)}
                      />
                    ) : null}
                  </div>
                );
              })}
              {dayCreates.map((ghost) => (
                <GhostRow
                  key={ghost.proposal.id}
                  ghost={ghost}
                  onDecide={(d, reason) => decide(ghost.proposal.id, d, reason)}
                />
              ))}
            </div>
            </div>
          </section>

          {error ? (
            // The month that could not be read, read again on the spot - the
            // way back was leaving the tab.
            <Failed onRetry={loaded ? reload : undefined} className="mt-6 lg:col-span-2">
              {error}
            </Failed>
          ) : null}
        </div>

      </main>

      <EntrySheet
        open={entry !== null}
        event={entry?.event ?? null}
        day={entry?.day ?? selected}
        block={calendarBlock}
        photos={entry?.event ? (photos.data ?? []).filter((p) => p.event && p.event === entry.event!.id.split("#")[0]) : []}
        onOpenChange={(open) => {
          if (!open) setEntry(null);
        }}
        onSave={async (next: Entry) => {
          const { photos: change, ...rest } = next;
          let id = rest.id;
          if (id) {
            if (!(await save(rest))) return "저장하지 못했어요. 잠시 뒤 다시 해 주세요.";
          } else {
            const made = await create(rest);
            if (made.error) return made.error;
            id = made.event?.id;
            setSelected(rest.date ?? selected);
          }
          // The photos go on the entry's first day, filed under it.
          if (change && id) {
            for (const asset of change.add) {
              const sent = await writeCouple("POST", "/photos", { date: rest.date, asset, event: id });
              if (!sent.ok) return `일정은 저장했지만 사진을 붙이지 못했어요: ${sent.error}`;
            }
            for (const photo of change.drop) {
              const sent = await writeCouple("DELETE", `/photos/${photo}`);
              if (!sent.ok) return `일정은 저장했지만 사진을 빼지 못했어요: ${sent.error}`;
            }
            await photos.reload();
          }
          return null;
        }}
        onCopy={async (next, days) => {
          // Each day gets the entry whole, its length kept.
          const span = next.endDate && next.date ? between(next.date, next.endDate) : 0;
          for (const day of days) {
            const made = await create({ ...next, date: day, endDate: span ? addDays(day, span) : undefined });
            if (made.error) return made.error;
          }
          return null;
        }}
        onDelete={(event) => {
          setEntry(null);
          erase(event);
        }}
      />

      {viewing !== null && dayPhotos.length ? (
        <Lightbox
          items={dayPhotos.map((p) => ({ src: photoSrc(p.asset, "display"), title: korDate(p.date) }))}
          start={viewing}
          onClose={() => setViewing(null)}
          actions={(i) => {
            const p = dayPhotos[i];
            if (!p) return null;
            return (
              <>
                {!p.cover ? (
                  <ViewerAction
                    onClick={() => {
                      setViewing(null);
                      void photoChange(() => writeCouple("POST", `/photos/${p.id}/cover`));
                    }}
                  >
                    대표로
                  </ViewerAction>
                ) : null}
                <ViewerAction
                  danger
                  onClick={() => {
                    setViewing(null);
                    void photoChange(() => writeCouple("DELETE", `/photos/${p.id}`));
                  }}
                >
                  지우기
                </ViewerAction>
              </>
            );
          }}
        />
      ) : null}

      <FindSheet open={finding} onOpenChange={setFinding} onFound={setSelected} />
      <MonthPicker
        open={choosingMonth}
        onOpenChange={setChoosingMonth}
        cursor={cursor}
        onPick={(c) => {
          const day = Number(selected.slice(8, 10));
          const last = new Date(c.year, c.month + 1, 0).getDate();
          setSelected(isoDate(new Date(c.year, c.month, Math.min(day, last))));
        }}
      />

      <EventSheet
        event={editing}
        onOpenChange={(open) => {
          if (!open) setEditing(null);
        }}
        // The whole entry with what was changed: the calendar takes an edit as
        // the entry it is to be, and what is left out would be dropped. The
        // place is in the memo's first line now (the sheet put it there).
        onSave={(patch) => (editing ? save({ ...editing, ...patch, place: undefined }) : Promise.resolve(false))}
      />

      <TabBar pending={pendingCount} />
    </div>
  );
}
