"use client";

import { useEffect } from "react";
import { readCouple, type Block, type Check, type DayPhoto, type News, type Space } from "@/lib/couple";
import { keepValue, keptValue } from "@/lib/keep";
import { useResource, type Resource } from "@/lib/use-resource";

/**
 * How often what is on screen is read again while it is on screen: the
 * partner changes the space from their phone, and those changes arrive with
 * no word to this console.
 */
const LIVE = 60_000;

/**
 * What was last read under a key, shown while it is read again: a screen's
 * resource goes with the screen, so going from 우리 to a block's page and back
 * started from an empty screen every time. Kept while the app is open
 * (lib/keep.ts), with when it was read - a read that fails over it says so.
 */
function useLastRead<T>(key: string | null, read: Resource<T>): Resource<T> {
  const own = key !== null && read.dataKey === key ? read.data : undefined;
  const at = read.readAt;
  useEffect(() => {
    if (key !== null && own !== undefined) keepValue(key, { data: own, at });
  }, [key, own, at]);
  if (key === null || read.data !== undefined) return read;
  const kept = keptValue<{ data: T; at: Date | null }>(key);
  return kept ? { ...read, data: kept.data, dataKey: key, readAt: kept.at } : read;
}

/** The couple's home: the two, since when, every block with what the home shows of it; none read when not wanted. */
export function useSpace(wanted = true) {
  const key = wanted ? "couple:space" : null;
  return useLastRead(key, useResource<Space>(key, (signal) => readCouple<Space>("", signal), { interval: LIVE }));
}

/** One block whole - a date map with every photo and record. */
export function useBlock(id: string) {
  const key = `couple:block:${id}`;
  return useLastRead(
    key,
    useResource<Block>(key, (signal) => readCouple<Block>(`/blocks/${encodeURIComponent(id)}`, signal), { interval: LIVE }),
  );
}

/**
 * A habit block's checks between two days, both people's - those days' own
 * or none yet. A month paged to keeps the month before in the resource until
 * its own read lands (or for good, when it fails), and shown under the new
 * name those were its figures, its unchecked days and a 체크하기 for a day
 * already checked.
 */
export function useChecks(block: string | null, from: string, to: string) {
  const key = block ? `couple:checks:${block}:${from}:${to}` : null;
  const checks = useResource<Check[]>(key, (signal) =>
    readCouple<{ checks: Check[] }>(`/blocks/${encodeURIComponent(block!)}/checks?from=${from}&to=${to}`, signal).then((r) => r.checks ?? []),
    { interval: LIVE },
  );
  return checks.dataKey === key ? checks : { ...checks, data: undefined };
}

/** What the two did in the space, newest first. The sheet opens on the forty the home's line read last. */
export function useNews(limit = 40) {
  const key = `couple:news:${limit}`;
  return useLastRead(
    key,
    useResource<News[]>(key, (signal) => readCouple<{ news: News[] }>(`/news?limit=${limit}`, signal).then((r) => r.news ?? []), {
      interval: LIVE,
    }),
  );
}

/** The photos pinned to calendar days between two days; none read when not wanted. */
export function useDayPhotos(from: string, to: string, wanted = true) {
  return useResource<DayPhoto[]>(wanted ? `couple:photos:${from}:${to}` : null, (signal) =>
    readCouple<{ photos: DayPhoto[] }>(`/photos?from=${from}&to=${to}`, signal).then((r) => r.photos ?? []),
    { interval: LIVE },
  );
}

/** The calendar card's photo for a day. */
export function useCover(date: string) {
  return useResource<DayPhoto | null>(`couple:cover:${date}`, (signal) =>
    readCouple<{ cover: DayPhoto | null }>(`/cover?date=${date}`, signal).then((r) => r.cover ?? null),
    { interval: LIVE },
  );
}

/** The blocks put away (보관), the last put away first; none read when not wanted. */
export function useArchived(wanted: boolean) {
  return useResource<Block[]>(wanted ? "couple:archived" : null, (signal) =>
    readCouple<{ blocks: Block[] }>("/archived", signal).then((r) => r.blocks ?? []),
  );
}
