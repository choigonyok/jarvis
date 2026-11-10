"use client";

import { useEffect } from "react";
import { keepBarsInTheme } from "@/lib/theme";

/**
 * Keeps the browser's bars - the home-screen app's status bar - in the
 * page's theme across navigations (lib/theme.ts). Draws nothing.
 */
export function ThemeBars() {
  useEffect(() => keepBarsInTheme(), []);
  return null;
}
