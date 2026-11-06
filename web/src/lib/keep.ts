"use client";

import { useEffect, useState, type Dispatch, type SetStateAction } from "react";

/**
 * What a screen was showing - its month, its picked day, its filter, the sheet
 * left open, a number being typed into a card - kept while the app is open, so
 * coming back from another tab finds it as it was: every tab switch had put the
 * screen back to its start (2026-11-06 review). In memory, one value per key: a
 * reload starts fresh, which is what a reload is for.
 */
const kept = new Map<string, unknown>();

export function useKept<T>(key: string, initial: T | (() => T)): [T, Dispatch<SetStateAction<T>>] {
  const [value, setValue] = useState<T>(() =>
    kept.has(key) ? (kept.get(key) as T) : typeof initial === "function" ? (initial as () => T)() : initial,
  );
  useEffect(() => {
    kept.set(key, value);
  }, [key, value]);
  return [value, setValue];
}

/** A kept value read or set outside a component: a card's half-typed input, by the card's id. */
export function keptValue<T>(key: string): T | undefined {
  return kept.get(key) as T | undefined;
}

export function keepValue(key: string, value: unknown) {
  if (value === undefined) kept.delete(key);
  else kept.set(key, value);
}
