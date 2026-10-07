"use client";

import { useEffect, useState } from "react";

/**
 * How this device reports its screen - the numbers the home-screen layout
 * depends on. On with ?vp=1, or by tapping the header's "Jarvis" five times
 * (a home-screen app has no address bar to type into). Nothing renders otherwise.
 */
export const PROBE_KEY = "jarvis.viewport-probe";
export function ViewportProbe() {
  const [lines, setLines] = useState<string[] | null>(null);

  const [on, setOn] = useState(false);
  useEffect(() => {
    const read = () => {
      let stored = false;
      try {
        stored = localStorage.getItem(PROBE_KEY) === "1";
      } catch {}
      setOn(new URLSearchParams(window.location.search).has("vp") || stored);
    };
    read();
    addEventListener("jarvis:probe", read);
    return () => removeEventListener("jarvis:probe", read);
  }, []);

  useEffect(() => {
    if (!on) return;
    const probe = document.createElement("div");
    probe.style.cssText =
      "position:fixed;left:0;top:0;width:0;visibility:hidden;" +
      "padding-top:env(safe-area-inset-top);padding-bottom:env(safe-area-inset-bottom);" +
      "height:100dvh";
    const sized = (h: string) => {
      const el = document.createElement("div");
      el.style.cssText = `position:fixed;visibility:hidden;width:0;height:${h}`;
      document.body.appendChild(el);
      const v = el.getBoundingClientRect().height;
      el.remove();
      return Math.round(v);
    };
    const read = () => {
      document.body.appendChild(probe);
      const cs = getComputedStyle(probe);
      const bar = document.querySelector("[data-tabbar]")?.getBoundingClientRect();
      setLines([
        `standalone ${matchMedia("(display-mode: standalone)").matches} / ${String((navigator as { standalone?: boolean }).standalone)}`,
        `screen ${screen.width}x${screen.height}  inner ${innerWidth}x${innerHeight}`,
        `visualViewport ${Math.round(window.visualViewport?.height ?? 0)}`,
        `vh ${sized("100vh")} dvh ${sized("100dvh")} svh ${sized("100svh")} lvh ${sized("100lvh")}`,
        `safe top ${cs.paddingTop} bottom ${cs.paddingBottom}`,
        `gap ${getComputedStyle(document.documentElement).getPropertyValue("--standalone-gap") || "-"}`,
        `html ${Math.round(document.documentElement.getBoundingClientRect().height)}  bar bottom ${bar ? Math.round(bar.bottom) : "-"}`,
      ]);
      probe.remove();
    };
    read();
    addEventListener("resize", read);
    return () => removeEventListener("resize", read);
  }, [on]);

  if (!on || !lines) return null;
  return (
    <pre className="pointer-events-none fixed top-24 left-2 z-[100] rounded-lg bg-black/85 p-2 font-mono text-[10px] leading-snug text-white">
      {lines.join("\n")}
    </pre>
  );
}
