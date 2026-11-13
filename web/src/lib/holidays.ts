"use client";

import { useResource } from "@/lib/use-resource";

/**
 * Korea's public holidays between two days, as the calendar's adapter knows
 * them: read off a public feed there once a day, so a substitute day or an
 * election day the government adds shows up without a new build.
 */
export function useHolidays(from: string, to: string) {
  return useResource<Record<string, string>>(`holidays:${from}:${to}`, async (signal) => {
    const res = await fetch(`/api/calendar/holidays?from=${from}&to=${to}`, { signal });
    if (!res.ok) throw new Error(`holidays ${res.status}`);
    const body = (await res.json()) as { holidays?: Record<string, string> };
    return body.holidays ?? {};
  });
}
