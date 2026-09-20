"use client";

import { useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { CalendarEvent } from "@/lib/thread";

/**
 * Direct entry, shaped like the chat composer: same glass bar in the same
 * place, so the two surfaces are typed into the same way. What the operator
 * writes here is saved outright - asking them to approve their own typing
 * would be theatre.
 */
export function EventComposer({
  date,
  onDateChange,
  onSave,
}: {
  /** The selected day, owned by the calendar: typing a date here moves the
   *  grid's selection, and picking a day in the grid fills this in. One date
   *  on screen, not two that can disagree. */
  date: string;
  onDateChange: (date: string) => void;
  onSave: (event: Partial<CalendarEvent>) => Promise<boolean>;
}) {
  const [start, setStart] = useState("");
  const [title, setTitle] = useState("");
  const [saving, setSaving] = useState(false);

  const ready = Boolean(date) && title.trim().length > 0;

  async function save() {
    if (!ready || saving) return;
    setSaving(true);
    const ok = await onSave({ date, start: start || undefined, title: title.trim() });
    setSaving(false);
    if (ok) {
      setTitle("");
      setStart("");
    }
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
      className="rounded-2xl border border-edge bg-glass px-2 py-2 backdrop-blur-xl transition-colors focus-within:border-white/18"
    >
      <div className="flex items-center gap-2">
        <Input
          type="date"
          value={date}
          onChange={(e) => {
            if (e.target.value) onDateChange(e.target.value);
          }}
          aria-label="날짜"
          className="tnum w-[8.5rem] shrink-0 border-transparent bg-transparent text-[13px] text-dim"
        />
        <Input
          type="time"
          value={start}
          onChange={(e) => setStart(e.target.value)}
          aria-label="시작 시각 (비우면 종일)"
          className="tnum w-[5.5rem] shrink-0 border-transparent bg-transparent text-[13px] text-dim"
        />
        <Input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="무슨 일정인가요"
          aria-label="일정 제목"
          className="h-9 flex-1 border-transparent bg-transparent text-[14.5px] text-foreground placeholder:text-faint"
        />
        <Button
          type="submit"
          variant="outline"
          size="icon-sm"
          disabled={!ready || saving}
          aria-label="일정 추가"
          className="size-8 shrink-0 rounded-lg border-edge bg-glass-raised text-dim hover:text-foreground disabled:opacity-40 dark:bg-glass-raised dark:hover:bg-white/12"
        >
          <Plus aria-hidden className="size-4" />
        </Button>
      </div>
    </form>
  );
}
