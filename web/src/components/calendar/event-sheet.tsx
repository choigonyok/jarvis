"use client";

import { useId, useState } from "react";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import type { CalendarEvent } from "@/lib/thread";

/**
 * One of your own entries, changed in place. A typo in a title, an hour moved
 * - these used to take a sentence to Jarvis and a card to approve (2026-11-06
 * review: the way to save an edit was there, lib/use-calendar.ts, and nothing
 * opened it). What you write here is yours and is saved as you save it; a new
 * entry still comes through the conversation, where Jarvis reads the rest of
 * the day with it. The partner's entries are not opened here: changing
 * someone else's day is a conversation, not a form.
 *
 * The place lives in the memo's first line (calendar-svc keeps no field for
 * it), so it is edited there.
 */
export function EventSheet({
  event,
  onOpenChange,
  onSave,
}: {
  event: CalendarEvent | null;
  onOpenChange: (open: boolean) => void;
  /** Saves what changed; resolves false (and says why elsewhere) when it did not. */
  onSave: (patch: Partial<CalendarEvent> & { id: string }) => Promise<boolean>;
}) {
  return (
    <Panel
      open={event !== null}
      onOpenChange={onOpenChange}
      title="일정 고치기"
      description="저장하면 바로 공유 캘린더에 반영돼요. 새 일정은 대화로 맡기세요."
    >
      {event ? <Form key={event.id} event={event} onSave={onSave} onClose={() => onOpenChange(false)} /> : null}
    </Panel>
  );
}

function Form({
  event,
  onSave,
  onClose,
}: {
  event: CalendarEvent;
  onSave: (patch: Partial<CalendarEvent> & { id: string }) => Promise<boolean>;
  onClose: () => void;
}) {
  const id = useId();
  const [title, setTitle] = useState(event.title);
  const [date, setDate] = useState(event.date);
  const [endDate, setEndDate] = useState(event.endDate ?? "");
  const [start, setStart] = useState(event.start ?? "");
  const [end, setEnd] = useState(event.end ?? "");
  const [memo, setMemo] = useState(event.place && !event.memo?.includes(event.place) ? [event.place, event.memo].filter(Boolean).join("\n") : (event.memo ?? ""));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const changed =
    title.trim() !== event.title ||
    date !== event.date ||
    endDate !== (event.endDate ?? "") ||
    start !== (event.start ?? "") ||
    end !== (event.end ?? "") ||
    memo.trim() !== (event.memo ?? "");
  const ready = title.trim() !== "" && /^\d{4}-\d{2}-\d{2}$/.test(date) && (!end || Boolean(start)) && (!endDate || endDate >= date);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!ready || !changed || busy) return;
    setBusy(true);
    setError(null);
    const ok = await onSave({
      id: event.id,
      title: title.trim(),
      date,
      endDate: endDate || undefined,
      start: start || undefined,
      end: end || undefined,
      memo: memo.trim(),
    });
    setBusy(false);
    if (ok) onClose();
    else setError("저장하지 못했어요. 잠시 뒤 다시 해 주세요.");
  }

  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      <Field>
        <FieldLabel htmlFor={`${id}-title`}>제목</FieldLabel>
        <Input id={`${id}-title`} surface="glass" value={title} maxLength={120} onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <div className="grid grid-cols-2 gap-2">
        <Field>
          <FieldLabel htmlFor={`${id}-date`}>날짜</FieldLabel>
          <Input id={`${id}-date`} surface="glass" type="date" required value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-end-date`}>마지막 날 (여러 날일 때)</FieldLabel>
          <Input id={`${id}-end-date`} surface="glass" type="date" min={date} value={endDate} onChange={(e) => setEndDate(e.target.value)} />
        </Field>
      </div>
      <div className="grid grid-cols-2 gap-2">
        <Field>
          <FieldLabel htmlFor={`${id}-start`}>시작 (비우면 종일)</FieldLabel>
          <Input id={`${id}-start`} surface="glass" type="time" value={start} onChange={(e) => setStart(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-end`}>끝</FieldLabel>
          <Input id={`${id}-end`} surface="glass" type="time" value={end} disabled={!start} onChange={(e) => setEnd(e.target.value)} />
        </Field>
      </div>
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
        <Button type="button" variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
          두기
        </Button>
        <Button type="submit" variant="raised" size="touch" disabled={!ready || !changed || busy} className="flex-1">
          {busy ? "저장하는 중…" : "저장"}
        </Button>
      </div>
    </form>
  );
}
