/** Pure date helpers for the month grid. No React, no fetching - just dates. */

export type Cursor = { year: number; month: number }; // month: 0-11

export const WEEKDAYS = ["일", "월", "화", "수", "목", "금", "토"] as const;

export function pad(n: number): string {
  return String(n).padStart(2, "0");
}

export function isoDate(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function parseISO(iso: string): Date {
  // Local midnight, not UTC: "2026-09-25" is a day here, not an instant.
  const [y, m, d] = iso.split("-").map(Number);
  return new Date(y, m - 1, d);
}

export function cursorOf(iso: string): Cursor {
  const d = parseISO(iso);
  return { year: d.getFullYear(), month: d.getMonth() };
}

export function sameMonth(iso: string, { year, month }: Cursor): boolean {
  return iso.startsWith(`${year}-${pad(month + 1)}`);
}

export function shiftMonth({ year, month }: Cursor, by: number): Cursor {
  const d = new Date(year, month + by, 1);
  return { year: d.getFullYear(), month: d.getMonth() };
}

export function addDays(iso: string, days: number): string {
  const d = parseISO(iso);
  d.setDate(d.getDate() + days);
  return isoDate(d);
}

/** "9월 25일 (금)" - the same sentence the agent's cards use. */
export function korDate(iso: string): string {
  const d = parseISO(iso);
  return `${d.getMonth() + 1}월 ${d.getDate()}일 (${WEEKDAYS[d.getDay()]})`;
}

export type Cell = {
  date: string;
  day: number;
  weekday: number;
  inMonth: boolean;
};

/**
 * Always six rows. A month that fits in five would otherwise shift the day
 * list up and down as you page through the year, and a calendar that moves
 * under the cursor is a calendar you stop trusting.
 */
export function monthCells(cursor: Cursor): Cell[] {
  const first = new Date(cursor.year, cursor.month, 1);
  const start = new Date(first);
  start.setDate(1 - first.getDay()); // 그 주의 일요일부터

  return Array.from({ length: 42 }, (_, i) => {
    const d = new Date(start);
    d.setDate(start.getDate() + i);
    return {
      date: isoDate(d),
      day: d.getDate(),
      weekday: d.getDay(),
      inMonth: d.getMonth() === cursor.month,
    };
  });
}

/** Both bounds inclusive, covering every cell the grid will show. */
export function gridBounds(cursor: Cursor): { from: string; to: string } {
  const cells = monthCells(cursor);
  return { from: cells[0].date, to: cells[cells.length - 1].date };
}
