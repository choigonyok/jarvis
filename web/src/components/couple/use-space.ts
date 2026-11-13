"use client";

import { readCouple, type Block, type Check, type DayPhoto, type News, type Space } from "@/lib/couple";
import { useResource } from "@/lib/use-resource";

/** The couple's home: the two, since when, every block with what the home shows of it. */
export function useSpace() {
  return useResource<Space>("couple:space", (signal) => readCouple<Space>("", signal));
}

/** One block whole - a date map with every photo and record. */
export function useBlock(id: string) {
  return useResource<Block>(`couple:block:${id}`, (signal) => readCouple<Block>(`/blocks/${encodeURIComponent(id)}`, signal));
}

/** A habit block's checks between two days, both people's. */
export function useChecks(block: string | null, from: string, to: string) {
  return useResource<Check[]>(block ? `couple:checks:${block}:${from}:${to}` : null, (signal) =>
    readCouple<{ checks: Check[] }>(`/blocks/${encodeURIComponent(block!)}/checks?from=${from}&to=${to}`, signal).then((r) => r.checks ?? []),
  );
}

/** What the two did in the space, newest first. */
export function useNews(limit = 40) {
  return useResource<News[]>(`couple:news:${limit}`, (signal) =>
    readCouple<{ news: News[] }>(`/news?limit=${limit}`, signal).then((r) => r.news ?? []),
  );
}

/** The photos pinned to calendar days between two days; none read when not wanted. */
export function useDayPhotos(from: string, to: string, wanted = true) {
  return useResource<DayPhoto[]>(wanted ? `couple:photos:${from}:${to}` : null, (signal) =>
    readCouple<{ photos: DayPhoto[] }>(`/photos?from=${from}&to=${to}`, signal).then((r) => r.photos ?? []),
  );
}

/** The calendar card's photo for a day. */
export function useCover(date: string) {
  return useResource<DayPhoto | null>(`couple:cover:${date}`, (signal) =>
    readCouple<{ cover: DayPhoto | null }>(`/cover?date=${date}`, signal).then((r) => r.cover ?? null),
  );
}
