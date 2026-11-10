/**
 * The console's two themes: dark, how it was made, and light, the same page
 * for daylight. The header's switch picks one (shell/theme-toggle.tsx) and a
 * cookie keeps it, so the server sends each page already in it - no flash of
 * the other - and each device keeps its own. The phone's own setting does
 * not pick it.
 *
 * Kept apart from lib/theme.ts, which runs in the browser: the root layout
 * reads the cookie with these on the server.
 */
export type Theme = "light" | "dark";

export const THEME_COOKIE = "jarvis-theme";

/** A cookie's value as a theme: dark unless it says light. */
export const themeOf = (value: string | null | undefined): Theme => (value === "light" ? "light" : "dark");

/**
 * The browser's own bars, in each theme: the page's background exactly -
 * globals.css --background as Safari draws it - since the top of the header
 * is that colour and runs on from the bar as one surface (.glass-pane).
 */
export const BAR_COLOR: Record<Theme, string> = { light: "#f9fafb", dark: "#0c0d0f" };
