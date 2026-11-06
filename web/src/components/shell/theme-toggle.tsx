"use client";

import { Contrast } from "lucide-react";
import { setTheme, themeNow } from "@/lib/theme";

/**
 * Dark or light, beside the bell: one tap turns the page over, and this
 * device keeps the choice (lib/theme-name.ts). The mark is the half-filled
 * circle the phone's own dark mode switch wears - a sun here would read as
 * the 오늘 tab's - and it turns over with the page.
 *
 * What the button says is the stylesheet's to pick (the dark variant reads
 * the root's data-theme), not the component's: the server sent the page in
 * the chosen theme, and there is no state here to come back from the server
 * different from it.
 */
export function ThemeToggle() {
  return (
    <button
      type="button"
      onClick={() => setTheme(themeNow() === "dark" ? "light" : "dark")}
      className="flex size-11 items-center justify-center rounded-lg text-faint transition-colors hover:text-dim focus-ring mouse:size-8"
    >
      <Contrast
        aria-hidden
        className="size-[18px] rotate-180 transition-transform duration-300 dark:rotate-0 motion-reduce:transition-none mouse:size-4"
      />
      <span className="sr-only hidden dark:inline">라이트 모드로 바꾸기</span>
      <span className="sr-only dark:hidden">다크 모드로 바꾸기</span>
    </button>
  );
}
