"use client";

import { useSyncExternalStore } from "react";

/**
 * The theme the phone asks for (globals.css follows it with a media query).
 * What draws its own colors - the map's renderer, a canvas - reads it here,
 * since neither can read the CSS tokens.
 */
export type Theme = "light" | "dark";

const QUERY = "(prefers-color-scheme: light)";

export function themeNow(): Theme {
  if (typeof window === "undefined") return "dark";
  return window.matchMedia(QUERY).matches ? "light" : "dark";
}

/** Calls back when the phone's theme changes (at sunset, say); returns the unsubscribe. */
export function onThemeChange(cb: (theme: Theme) => void): () => void {
  const mq = window.matchMedia(QUERY);
  const listen = () => cb(mq.matches ? "light" : "dark");
  mq.addEventListener("change", listen);
  return () => mq.removeEventListener("change", listen);
}

/** The theme, for a component that draws by it. Dark on the server, as the page was made. */
export function useTheme(): Theme {
  return useSyncExternalStore(
    (cb) => onThemeChange(() => cb()),
    themeNow,
    () => "dark",
  );
}
