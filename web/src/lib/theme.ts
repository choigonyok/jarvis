"use client";

import { useSyncExternalStore } from "react";
import { BAR_COLOR, THEME_COOKIE, type Theme } from "@/lib/theme-name";

export type { Theme };

/**
 * The theme the page is in: the root's data-theme, which the server set from
 * the cookie and the header's switch changes (lib/theme-name.ts). What draws
 * its own colors - the map's renderer, a canvas - reads it here, since
 * neither can read the CSS tokens.
 */
export function themeNow(): Theme {
  if (typeof document === "undefined") return "dark";
  return document.documentElement.dataset.theme === "light" ? "light" : "dark";
}

const CHANGED = "jarvis:theme";

/** Calls back when the switch changes the theme; returns the unsubscribe. */
export function onThemeChange(cb: (theme: Theme) => void): () => void {
  const listen = () => cb(themeNow());
  window.addEventListener(CHANGED, listen);
  return () => window.removeEventListener(CHANGED, listen);
}

/**
 * Puts the page in a theme, at once and for the next visit: the root's
 * attribute (globals.css reads it), the cookie the server reads, and the
 * browser's own bars.
 */
export function setTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  const secure = location.protocol === "https:" ? "; secure" : "";
  // Two years: a choice, not a session.
  document.cookie = `${THEME_COOKIE}=${theme}; path=/; max-age=${60 * 60 * 24 * 730}; samesite=lax${secure}`;
  for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
    meta.content = BAR_COLOR[theme];
    meta.removeAttribute("media");
  }
  window.dispatchEvent(new Event(CHANGED));
}

/** The theme, for a component that draws by it. Dark on the server, until the page is read. */
export function useTheme(): Theme {
  return useSyncExternalStore(
    (cb) => onThemeChange(() => cb()),
    themeNow,
    () => "dark",
  );
}
