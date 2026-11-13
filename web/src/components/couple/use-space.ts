"use client";

import { readCouple, type Block, type Check, type DayPhoto, type News, type Space } from "@/lib/couple";
import { useResource } from "@/lib/use-resource";

/**
 * How often what is on screen is read again while it is on screen: the
 * partner changes the space from their phone, and those changes arrive with
 * no word to this console.
 */
const LIVE = 60_000;

/** The couple's home: the two, since when, every block with what the home shows of it; none read when not wanted. */
export function useSpace(wanted = true) {
  return useResource<Space>(wanted ? "couple:space" : null, (signal) => readCouple<Space>("", signal), { interval: LIVE });
}

/** One block whole - a date map with every photo and record. */
export function useBlock(id: string) {
  return useResource<Block>(`couple:block:${id}`, (signal) => readCouple<Block>(`/blocks/${encodeURIComponent(id)}`, signal), {
    interval: LIVE,
  });
}

/** A habit block's checks between two days, both people's. */
export function useChecks(block: string | null, from: string, to: string) {
  return useResource<Check[]>(block ? `couple:checks:${block}:${from}:${to}` : null, (signal) =>
    readCouple<{ checks: Check[] }>(`/blocks/${encodeURIComponent(block!)}/checks?from=${from}&to=${to}`, signal).then((r) => r.checks ?? []),
    { interval: LIVE },
  );
}

/** What the two did in the space, newest first. */
export function useNews(limit = 40) {
  return useResource<News[]>(`couple:news:${limit}`, (signal) =>
    readCouple<{ news: News[] }>(`/news?limit=${limit}`, signal).then((r) => r.news ?? []),
    { interval: LIVE },
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
