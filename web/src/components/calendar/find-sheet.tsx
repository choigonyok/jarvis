"use client";

import { useMemo, useState } from "react";
import { Panel } from "@/components/ui/bottom-sheet";
import { Input } from "@/components/ui/input";
import { OwnerDot, ownerOf } from "@/components/calendar/owner";
import { HttpError, useResource } from "@/lib/use-resource";
import { SAY } from "@/lib/say";
import { shiftDay, dateLabel, dayOf } from "@/lib/when";
import type { CalendarEvent } from "@/lib/thread";

/**
 * Finding an entry by its words, a year either way of today - as far as the
 * shared calendar is read. A repeating entry is found once, at its next day.
 */
export function FindSheet({ open, onOpenChange, onFound }: { open: boolean; onOpenChange: (open: boolean) => void; onFound: (date: string) => void }) {
  const [query, setQuery] = useState("");
  // A sheet opened again starts empty.
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (!open) setQuery("");
  }
  const now = dayOf(new Date());
  const from = shiftDay(now, -365);
  const to = shiftDay(now, 365);
  const year = useResource<CalendarEvent[]>(open ? `calendar:find:${from}` : null, async (signal) => {
    const res = await fetch(`/api/calendar/events?from=${from}&to=${to}`, { cache: "no-store", signal });
    if (!res.ok) throw new HttpError(res.status);
    return ((await res.json()) as { events: CalendarEvent[] }).events ?? [];
  });
  const found = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q || !year.data) return [];
    const seen = new Set<string>();
    const hits: CalendarEvent[] = [];
    // Nearest first: what is coming, then what was.
    const order = [...year.data].sort((a, b) => {
      const fa = a.date >= now, fb = b.date >= now;
      if (fa !== fb) return fa ? -1 : 1;
      return fa ? a.date.localeCompare(b.date) : b.date.localeCompare(a.date);
    });
    for (const e of order) {
      const series = e.id.split("#")[0];
      if (seen.has(series)) continue;
      if (`${e.title}\n${e.memo ?? ""}`.toLowerCase().includes(q)) {
        seen.add(series);
        hits.push(e);
      }
      if (hits.length >= 60) break;
    }
    return hits;
  }, [query, year.data, now]);

  return (
    <Panel open={open} onOpenChange={onOpenChange} title="일정 찾기" height="tall">
      <Input autoFocus surface="glass" type="search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="제목이나 메모로 찾기" aria-label="일정 찾기" />
      <div className="mt-3 min-h-40">
        {year.error && !year.data ? (
          <p className="text-ui text-reject">{SAY.couldNotLoad}</p>
        ) : !year.data ? (
          <p className="text-ui text-faint">{SAY.loading}</p>
        ) : !query.trim() ? (
          <p className="text-ui text-faint">앞뒤 1년의 일정에서 찾아요.</p>
        ) : found.length === 0 ? (
          <p className="text-ui text-faint">찾는 일정이 없어요.</p>
        ) : (
          <ul className="divide-y divide-edge-soft">
            {found.map((e) => (
              <li key={e.id}>
                <button
                  type="button"
                  onClick={() => {
                    onFound(e.date);
                    onOpenChange(false);
                  }}
                  className="-mx-2 flex w-[calc(100%+1rem)] items-start gap-3 rounded-lg px-2 py-3 text-left transition-colors hover:bg-glass focus-ring"
                >
                  <OwnerDot owner={ownerOf(e)} className="mt-[7px]" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-body text-foreground">{e.title}</span>
                    <span className="block text-aside text-faint">
                      {dateLabel(e.date)}
                      {e.start ? ` ${e.start}` : ""}
                      {e.recurrence ? " · 반복" : ""}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Panel>
  );
}
