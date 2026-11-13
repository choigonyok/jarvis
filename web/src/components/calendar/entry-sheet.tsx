"use client";

import { useId, useMemo, useState } from "react";
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
import { REMINDERS, REPEATS, between, type DayPhoto } from "@/lib/couple";
import { addDays, cursorOf, monthCells, shiftMonth, type Cursor } from "@/lib/month";
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
 * A day of a repeating entry is not changed here: the shared calendar takes
 * that only in its own app. It can still be taken off, that day alone.
 */
export type Entry = Partial<CalendarEvent> & {
  repeat?: string;
  reminder?: number;
  /** Photos put on the entry's day with it (uploaded assets), and its photos taken off. */
  photos?: { add: string[]; drop: string[] };
};

export function EntrySheet({
  open,
  event,
  day,
  block,
  photos = [],
  onOpenChange,
  onSave,
  onCopy,
  onDelete,
}: {
  open: boolean;
  /** The entry being changed; none for a new one. */
  event: CalendarEvent | null;
  /** The day a new entry starts on. */
  day: string;
  /** The calendar block photos are uploaded for; none, no photos here. */
  block?: string;
  /** The entry's photos as they stand. */
  photos?: DayPhoto[];
  onOpenChange: (open: boolean) => void;
  /** Resolves to why it did not go, or null. */
  onSave: (entry: Entry) => Promise<string | null>;
  onCopy: (entry: Entry, days: string[]) => Promise<string | null>;
  onDelete: (event: CalendarEvent) => void;
}) {
  return (
    <Panel open={open} onOpenChange={onOpenChange} title={event ? "일정 고치기" : "일정 추가"} description={event ? undefined : "저장하면 바로 공유 캘린더에 들어가 상대 폰에도 보여요."}>
      {open ? (
        <Form
          key={event?.id ?? `new-${day}`}
          event={event}
          day={day}
          block={block}
          photos={photos}
          onSave={onSave}
          onCopy={onCopy}
          onDelete={onDelete}
          onClose={() => onOpenChange(false)}
        />
      ) : null}
    </Panel>
  );
}

function Form({
  event,
  day,
  block,
  photos,
  onSave,
  onCopy,
  onDelete,
  onClose,
}: {
  event: CalendarEvent | null;
  day: string;
  block?: string;
  photos: DayPhoto[];
  onSave: (entry: Entry) => Promise<string | null>;
  onCopy: (entry: Entry, days: string[]) => Promise<string | null>;
  onDelete: (event: CalendarEvent) => void;
  onClose: () => void;
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

  const multi = endDate > date;
  const ready =
    title.trim() !== "" &&
    /^\d{4}-\d{2}-\d{2}$/.test(date) &&
    (!multi || endDate >= date) &&
    (allDay || /^\d{2}:\d{2}$/.test(start)) &&
    (allDay || multi || !end || end >= start);

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
        onBack={() => setCopying(false)}
        onCopy={async (days) => {
          const failed = await onCopy({ ...entry(), id: undefined, recurrence: undefined, repeat: undefined, photos: undefined }, days);
          if (!failed) onClose();
          return failed;
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
        <Input surface="glass" type="time" aria-label="시작 시간" value={start} disabled={allDay} onChange={(e) => setStart(e.target.value)} className={cn("min-w-0", allDay && "invisible")} />
        <span className="text-ui text-dim">종료</span>
        <Input surface="glass" type="date" aria-label="종료 날짜" min={date} value={endDate} onChange={(e) => setEndDate(e.target.value)} className="min-w-0" />
        <Input
          surface="glass"
          type="time"
          aria-label="종료 시간"
          value={end}
          disabled={allDay}
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

      {block ? (
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

      {error ? (
        <p role="alert" className="text-aside text-reject">
          {error}
        </p>
      ) : null}

      <div className="flex gap-2 pt-1">
        {event ? (
          <Button type="button" variant="discard" size="touch-sm" onClick={() => onDelete(event)} disabled={busy}>
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
      {event ? (
        <Button type="button" variant="quiet" size="touch-sm" onClick={() => setCopying(true)} className="-ml-2 gap-1.5">
          <Copy aria-hidden className="size-4" />
          다른 날짜에 복사
        </Button>
      ) : null}
    </form>
  );
}

/** Days to copy an entry to, picked on a month. */
function CopyDays({ from, onBack, onCopy }: { from: string; onBack: () => void; onCopy: (days: string[]) => Promise<string | null> }) {
  const [cursor, setCursor] = useState<Cursor>(cursorOf(from));
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const cells = useMemo(() => monthCells(cursor), [cursor]);

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
        {["일", "월", "화", "수", "목", "금", "토"].map((n) => (
          <span key={n} className="text-meta text-faint">
            {n}
          </span>
        ))}
        {cells.map((c) => {
          const on = picked.has(c.date);
          const self = c.date === from;
          return (
            <button
              key={c.date}
              type="button"
              disabled={self}
              aria-pressed={on}
              aria-label={dateLabel(c.date)}
              onClick={() => toggle(c.date)}
              className={cn(
                "flex aspect-square items-center justify-center rounded-lg text-ui tabular-nums transition-colors focus-ring",
                on ? "bg-foreground font-semibold text-background" : c.inMonth ? "text-ink hover:bg-glass" : "text-faint",
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
          const failed = await onCopy([...picked].sort());
          setBusy(false);
          if (failed) setError(failed);
        }}
        className="mt-4 w-full"
      >
        {busy ? "복사하는 중…" : picked.size ? `${picked.size}개 날짜에 복사` : "날짜를 골라요"}
      </Button>
    </div>
  );
}
