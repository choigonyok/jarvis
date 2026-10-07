"use client";

import { useEffect, useState } from "react";

/**
 * Add ?vp=1 to any page to see how this device reports its screen - the numbers
 * the home-screen layout depends on. Nothing renders otherwise.
 */
export function ViewportProbe() {
  const [lines, setLines] = useState<string[] | null>(null);

  useEffect(() => {
    if (!new URLSearchParams(window.location.search).has("vp")) return;
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
  }, []);

  if (!lines) return null;
  return (
    <pre className="pointer-events-none fixed top-24 left-2 z-[100] rounded-lg bg-black/85 p-2 font-mono text-[10px] leading-snug text-white">
      {lines.join("\n")}
    </pre>
  );
}
