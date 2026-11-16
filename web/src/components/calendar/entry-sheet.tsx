"use client";

import { useEffect, useId, useMemo, useState } from "react";
import { ChevronLeft, ChevronRight, Copy } from "lucide-react";
import { PhotoTiles, PickPhotos, useUploads } from "@/components/couple/photos";
import { Segment } from "@/components/couple/segment";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { SwitchRow } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { REMINDERS, REPEATS, between, weekdays, type DayPhoto } from "@/lib/couple";
import { addDays, cursorOf, monthCells, pad, shiftMonth, type Cursor, type WeekStart } from "@/lib/month";
import type { CalendarEvent } from "@/lib/thread";
import { dateLabel } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * An entry on the shared calendar, as a person writes it: whose it is, the
 * title, all day or from a time to a time, how it repeats and when the app
 * reminds both phones, a memo (the place on its first line). A new one is
 * saved as typed - it is a person's, not the assistant's guess - and goes on
 * the partner's phone at once. An entry of mine can be copied to other days.
 *
 * A day of a repeating entry opens the whole series: what is changed here
 * changes every day of it. That one day can be taken off alone, or the
 * series with all its days.
 */
export type Entry = Partial<CalendarEvent> & {
  repeat?: string;
  reminder?: number;
  /** Photos put on the entry's day with it (uploaded assets), and its photos taken off. */
  photos?: { add: string[]; drop: string[] };
};

/** Why a copy stopped, if it did, and the days it went into before that. */
type Copied = { error: string | null; done: string[] };

const TIME = /^\d{2}:\d{2}$/;
const DAY_MINUTES = 24 * 60;
/** "19:30" as minutes into its day, and back. */
const minutes = (hm: string) => Number(hm.slice(0, 2)) * 60 + Number(hm.slice(3, 5));
const clockOf = (m: number) => `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;

export function EntrySheet({
  open,
  event,
  day,
  weekStart,
  occurrence,
  block,
  photos = [],
  onOpenChange,
  onSave,
  onCopy,
  onDelete,
}: {
  open: boolean;
  /** The entry being changed; none for a new one. A series is changed whole. */
  event: CalendarEvent | null;
  /** The day of a series that was tapped, by its own id: it can be taken off alone. */
  occurrence?: CalendarEvent;
  /** The day a new entry starts on. */
  day: string;
  /** Where the copy's month starts its weeks, as the calendar under it does. */
  weekStart?: WeekStart;
  /** The calendar block photos are uploaded for; none, no photos here. */
  block?: string;
  /** The entry's photos as they stand. */
  photos?: DayPhoto[];
  onOpenChange: (open: boolean) => void;
  /** Resolves to why it did not go, or null. */
  onSave: (entry: Entry) => Promise<string | null>;
  onCopy: (entry: Entry, days: string[]) => Promise<Copied>;
  onDelete: (event: CalendarEvent) => void;
}) {
  // The form has something in it that is not saved: a pull down, a tap
  // outside, Back or the ✕ asks before it goes (ui/sheet.tsx). The most
  // fields in the app, and the pull that closes it is the scroll's way.
  const [dirty, setDirty] = useState(false);
  return (
    <Panel
      open={open}
      onOpenChange={onOpenChange}
      guard={dirty}
      title={occurrence ? "반복 일정 고치기" : event ? "일정 고치기" : "일정 추가"}
      description={
        occurrence
          ? "바꾸면 반복되는 모든 날이 바뀌어요. 하루만 바꾸려면 그날을 지우고 새 일정으로 넣어요."
          : event
            ? undefined
            : "저장하면 바로 공유 캘린더에 들어가 상대 폰에도 보여요."
      }
    >
      {open ? (
        <Form
          key={occurrence?.id ?? event?.id ?? `new-${day}`}
          event={event}
          occurrence={occurrence}
          day={day}
          weekStart={weekStart}
          block={block}
          photos={photos}
          onSave={onSave}
          onCopy={onCopy}
          onDelete={onDelete}
          onClose={() => onOpenChange(false)}
          onDirty={setDirty}
        />
      ) : null}
    </Panel>
  );
}

function Form({
  event,
  occurrence,
  day,
  weekStart,
  block,
  photos,
  onSave,
  onCopy,
  onDelete,
  onClose,
  onDirty,
}: {
  event: CalendarEvent | null;
  occurrence?: CalendarEvent;
  day: string;
  weekStart?: WeekStart;
  block?: string;
  photos: DayPhoto[];
  onSave: (entry: Entry) => Promise<string | null>;
  onCopy: (entry: Entry, days: string[]) => Promise<Copied>;
  onDelete: (event: CalendarEvent) => void;
  onClose: () => void;
  /** Whether anything differs from what the form opened with. */
  onDirty: (dirty: boolean) => void;
}) {
  const [added, setAdded] = useState<string[]>([]);
  const [dropped, setDropped] = useState<ReadonlySet<string>>(new Set());
  const uploads = useUploads(block ?? null);
  const kept = photos.filter((p) => !dropped.has(p.id));
  const id = useId();
  const [owner, setOwner] = useState<"me" | "shared">(event?.owner === "shared" ? "shared" : "me");
  const [title, setTitle] = useState(event?.title ?? "");
  const [allDay, setAllDay] = useState(event ? !event.start : false);
  const [date, setDate] = useState(event?.date ?? day);
  const [endDate, setEndDate] = useState(event?.endDate ?? event?.date ?? day);
  const [start, setStart] = useState(event?.start ?? "09:00");
  const [end, setEnd] = useState(event?.end ?? (event ? "" : "10:00"));
  const [repeat, setRepeat] = useState(event?.recurrence || "none");
  const initialReminder = (event as Entry | null)?.reminder ?? -1;
  const [reminder, setReminder] = useState(initialReminder);
  const [memo, setMemo] = useState(
    event?.place && !event.memo?.includes(event.place) ? [event.place, event.memo].filter(Boolean).join("\n") : (event?.memo ?? ""),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copying, setCopying] = useState(false);
  // A series asks which to take off: the day tapped, or every day.
  const [erasing, setErasing] = useState(false);

  // Changed from what the form opened with - a new entry left as it came,
  // its 09:00-10:00 and all, is not - or photos on their way up.
  const [opened] = useState(() => ({ owner, title, allDay, date, endDate, start, end, repeat, reminder, memo }));
  const dirty =
    owner !== opened.owner ||
    title.trim() !== opened.title.trim() ||
    allDay !== opened.allDay ||
    date !== opened.date ||
    endDate !== opened.endDate ||
    start !== opened.start ||
    end !== opened.end ||
    repeat !== opened.repeat ||
    reminder !== opened.reminder ||
    memo.trim() !== opened.memo.trim() ||
    added.length > 0 ||
    dropped.size > 0 ||
    !!uploads.busy;
  useEffect(() => {
    onDirty(dirty);
    return () => onDirty(false);
  }, [dirty, onDirty]);

  const multi = endDate > date;
  // An end before its start is why 저장 does not go: said above it, and on
  // the field to change. A dimmed button alone sent a thumb up and down the
  // sheet looking for what was wrong.
  const endDateEarly = endDate !== "" && endDate < date;
  const endEarly = !allDay && !multi && end !== "" && end < start;
  const early = endDateEarly
    ? "종료 날짜가 시작 날짜보다 일러요."
    : endEarly
      ? "종료가 시작보다 일러요. 다음 날까지면 종료 날짜를 바꿔 주세요."
      : null;
  const ready =
    title.trim() !== "" &&
    /^\d{4}-\d{2}-\d{2}$/.test(date) &&
    (allDay || TIME.test(start)) &&
    !early;

  function entry(): Entry {
    return {
      ...(event ?? {}),
      place: undefined,
      owner,
      title: title.trim(),
      date,
      endDate: multi ? endDate : undefined,
      start: allDay ? undefined : start,
      end: allDay ? undefined : end || undefined,
      memo: memo.trim(),
      // Sent only when chosen here: a whole entry without them leaves them be.
      ...(event && repeat === (event.recurrence || "none") ? {} : { repeat }),
      ...(event && reminder === initialReminder ? {} : { reminder }),
      ...(added.length || dropped.size ? { photos: { add: added, drop: [...dropped] } } : {}),
    };
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!ready || busy) return;
    setBusy(true);
    setError(null);
    const failed = await onSave(entry());
    setBusy(false);
    if (failed) setError(failed);
    else onClose();
  }

  if (copying && event) {
    return (
      <CopyDays
        from={event.date}
        weekStart={weekStart}
        onBack={() => setCopying(false)}
        onCopy={async (days) => {
          const copied = await onCopy({ ...entry(), id: undefined, recurrence: undefined, repeat: undefined, photos: undefined }, days);
          if (!copied.error) onClose();
          return copied;
        }}
      />
    );
  }

  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      <Segment
        label="누구의 일정"
        value={owner}
        onChange={setOwner}
        options={[
          { value: "me", label: "나" },
          { value: "shared", label: "함께" },
        ]}
      />
      <Field>
        <FieldLabel htmlFor={`${id}-title`} className="sr-only">
          제목
        </FieldLabel>
        <Input id={`${id}-title`} surface="glass" value={title} maxLength={120} placeholder="제목" onChange={(e) => setTitle(e.target.value)} />
      </Field>

      <SwitchRow label="하루 종일" checked={allDay} onCheckedChange={setAllDay} />

      <div className="grid grid-cols-[2.5rem_minmax(0,1fr)_minmax(0,0.85fr)] items-center gap-x-2 gap-y-2">
        <span className="text-ui text-dim">시작</span>
        <Input
          surface="glass"
          type="date"
          aria-label="시작 날짜"
          className="min-w-0"
          value={date}
          onChange={(e) => {
            const next = e.target.value;
            // The end keeps its distance from the start.
            if (next && date) setEndDate(addDays(endDate, between(date, next)));
            setDate(next);
          }}
        />
        <Input
          surface="glass"
          type="time"
          aria-label="시작 시간"
          value={start}
          disabled={allDay}
          onChange={(e) => {
            const next = e.target.value;
            // The end keeps its length too: an hour from 09:00 moved to the
            // evening is an hour from 19:00, past midnight into the next day.
            if (TIME.test(next) && TIME.test(start) && TIME.test(end) && (multi || end >= start)) {
              const at = minutes(end) + minutes(next) - minutes(start);
              const days = Math.floor(at / DAY_MINUTES);
              const endsOn = multi ? endDate : date;
              setEnd(clockOf(at - days * DAY_MINUTES));
              if (days && endsOn) setEndDate(addDays(endsOn, days));
            }
            setStart(next);
          }}
          className={cn("min-w-0", allDay && "invisible")}
        />
        <span className="text-ui text-dim">종료</span>
        <Input
          surface="glass"
          type="date"
          aria-label="종료 날짜"
          min={date}
          value={endDate}
          aria-invalid={endDateEarly || undefined}
          onChange={(e) => setEndDate(e.target.value)}
          className="min-w-0"
        />
        <Input
          surface="glass"
          type="time"
          aria-label="종료 시간"
          value={end}
          disabled={allDay}
          aria-invalid={endEarly || undefined}
          onChange={(e) => setEnd(e.target.value)}
          className={cn("min-w-0", allDay && "invisible")}
        />
      </div>

      <div className="grid grid-cols-2 gap-2">
        <Field>
          <FieldLabel htmlFor={`${id}-repeat`}>반복</FieldLabel>
          <NativeSelect id={`${id}-repeat`} value={repeat} onChange={(e) => setRepeat(e.target.value)} className="w-full">
            {REPEATS.map((r) => (
              <NativeSelectOption key={r.value} value={r.value}>
                {r.label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-reminder`}>알림</FieldLabel>
          <NativeSelect id={`${id}-reminder`} value={String(reminder)} onChange={(e) => setReminder(Number(e.target.value))} className="w-full">
            {REMINDERS.map((r) => (
              <NativeSelectOption key={r.value} value={String(r.value)}>
                {r.label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
      </div>

      {block && !occurrence ? (
        <Field>
          <FieldLabel>사진</FieldLabel>
          <PhotoTiles
            size="sm"
            assets={[...kept.map((p) => ({ key: p.id, asset: p.asset })), ...added.map((a) => ({ key: a, asset: a }))]}
            onRemove={(i) => {
              if (i < kept.length) setDropped((s) => new Set(s).add(kept[i].id));
              else setAdded((list) => list.filter((_, j) => j !== i - kept.length));
            }}
            trailing={
              <PickPhotos label="사진" busy={uploads.busy} onPick={(files) => void uploads.upload(files).then((assets) => setAdded((list) => [...list, ...assets]))} />
            }
          />
          {uploads.error ? <p className="text-aside text-reject">{uploads.error}</p> : null}
        </Field>
      ) : null}

      <Field>
        <FieldLabel htmlFor={`${id}-memo`}>메모 · 장소는 첫 줄에</FieldLabel>
        <Textarea id={`${id}-memo`} surface="glass" rows={3} maxLength={500} value={memo} onChange={(e) => setMemo(e.target.value)} className="min-h-[5.5rem]" />
      </Field>

      {early ? (
        <p role="alert" className="text-aside text-reject">
          {early}
        </p>
      ) : null}
      {error ? (
        <p role="alert" className="text-aside text-reject">
          {error}
        </p>
      ) : null}

      {erasing && event && occurrence ? (
        <div role="group" aria-label="무엇을 지울지" className="space-y-2 rounded-xl bg-glass p-3">
          <p className="text-aside text-dim">반복 일정이에요. 무엇을 지울까요?</p>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="discard" size="touch-sm" onClick={() => onDelete(occurrence)}>
              {dateLabel(occurrence.date)}만 지우기
            </Button>
            <Button type="button" variant="discard" size="touch-sm" onClick={() => onDelete(event)}>
              반복 전체 지우기
            </Button>
            <Button type="button" variant="soft" size="touch-sm" onClick={() => setErasing(false)}>
              두기
            </Button>
          </div>
        </div>
      ) : null}

      <div className="flex gap-2 pt-1">
        {event ? (
          <Button
            type="button"
            variant="discard"
            size="touch-sm"
            onClick={() => (occurrence ? setErasing(true) : onDelete(event))}
            disabled={busy || erasing}
          >
            지우기
          </Button>
        ) : (
          <Button type="button" variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
            두기
          </Button>
        )}
        <Button type="submit" variant="raised" size="touch" disabled={!ready || busy || !!uploads.busy} className="flex-1">
          {busy ? "저장하는 중…" : "저장"}
        </Button>
      </div>
      {event && !occurrence ? (
        <Button type="button" variant="quiet" size="touch-sm" onClick={() => setCopying(true)} className="-ml-2 gap-1.5">
          <Copy aria-hidden className="size-4" />
          다른 날짜에 복사
        </Button>
      ) : null}
    </form>
  );
}

/** Days to copy an entry to, picked on a month. */
function CopyDays({
  from,
  weekStart = "sun",
  onBack,
  onCopy,
}: {
  from: string;
  weekStart?: WeekStart;
  onBack: () => void;
  onCopy: (days: string[]) => Promise<Copied>;
}) {
  const [cursor, setCursor] = useState<Cursor>(cursorOf(from));
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  // Days a copy that stopped went into before it did: no longer picked, and
  // not to be picked again - pressed again, the copy goes on from where it
  // stopped instead of into those days a second time.
  const [copied, setCopied] = useState<ReadonlySet<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const cells = useMemo(() => monthCells(cursor, weekStart), [cursor, weekStart]);

  function toggle(day: string) {
    setPicked((s) => {
      const next = new Set(s);
      if (next.has(day)) next.delete(day);
      else next.add(day);
      return next;
    });
  }

  return (
    <div>
      <div className="flex items-center justify-between">
        <Button variant="quiet" size="touch-sm" onClick={onBack} className="-ml-2 gap-0.5">
          <ChevronLeft aria-hidden className="size-4" />
          돌아가기
        </Button>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="icon-touch" onClick={() => setCursor((c) => shiftMonth(c, -1))} aria-label="이전 달" className="text-faint hover:text-foreground">
            <ChevronLeft aria-hidden className="size-5" />
          </Button>
          <span className="min-w-[6.5rem] text-center text-ui text-ink tabular-nums">
            {cursor.year}년 {cursor.month + 1}월
          </span>
          <Button variant="ghost" size="icon-touch" onClick={() => setCursor((c) => shiftMonth(c, 1))} aria-label="다음 달" className="text-faint hover:text-foreground">
            <ChevronRight aria-hidden className="size-5" />
          </Button>
        </div>
      </div>
      <p className="mt-1 text-aside text-faint">복사할 날을 골라요. {dateLabel(from)}의 일정 그대로 들어가요.</p>
      <div className="mt-3 grid grid-cols-7 gap-1 text-center">
        {weekdays(weekStart).map((n) => (
          <span key={n} className="text-meta text-faint">
            {n}
          </span>
        ))}
        {cells.map((c) => {
          const on = picked.has(c.date);
          const self = c.date === from;
          const went = copied.has(c.date);
          return (
            <button
              key={c.date}
              type="button"
              disabled={self || went}
              aria-pressed={on}
              aria-label={went ? `${dateLabel(c.date)}, 복사했어요` : dateLabel(c.date)}
              onClick={() => toggle(c.date)}
              className={cn(
                "flex aspect-square items-center justify-center rounded-lg text-ui tabular-nums transition-colors focus-ring",
                on
                  ? "bg-foreground font-semibold text-background"
                  : went
                    ? "bg-approve-wash text-approve"
                    : c.inMonth
                      ? "text-ink hover:bg-glass"
                      : "text-faint",
                self && "ring-1 ring-edge",
              )}
            >
              {c.day}
            </button>
          );
        })}
      </div>
      {error ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {error}
        </p>
      ) : null}
      <Button
        variant="raised"
        size="touch"
        disabled={picked.size === 0 || busy}
        onClick={async () => {
          setBusy(true);
          setError(null);
          const { error: failed, done } = await onCopy([...picked].sort());
          setBusy(false);
          if (!failed) return;
          setError(failed);
          setPicked((s) => new Set([...s].filter((d) => !done.includes(d))));
          setCopied((s) => new Set([...s, ...done]));
        }}
        className="mt-4 w-full"
      >
        {busy ? "복사하는 중…" : picked.size ? `${picked.size}개 날짜에 복사` : "날짜를 골라요"}
      </Button>
    </div>
  );
}
