"use client";

import { useEffect, useRef } from "react";
import { ArrowUp, Info, Mic } from "lucide-react";
import { diag } from "@/lib/diag";
import { cn } from "@/lib/utils";

/**
 * The page's last few uncaught errors. A throw part-way through React's commit
 * leaves what it had added and takes nothing away, so they go with a report.
 */
const lastErrors: string[] = [];
if (typeof window !== "undefined") {
  const keep = (what: string) => {
    lastErrors.push(what.slice(0, 200));
    if (lastErrors.length > 5) lastErrors.shift();
  };
  window.addEventListener("error", (e) => keep(`${e.message} @${(e.filename || "").split("/").pop() ?? ""}:${e.lineno}`));
  window.addEventListener("unhandledrejection", (e) =>
    keep(`rejection: ${e.reason instanceof Error ? e.reason.message : String(e.reason)}`),
  );
}

const native = (f: unknown) => typeof f === "function" && /\[native code\]/.test(Function.prototype.toString.call(f));

/**
 * Whether React still holds the node as its own. React takes its marks off a
 * node it removed in the passive phase after the commit - so asked in an
 * effect (useEffect), not a layout effect, a node removed in the same commit
 * already reads as let go.
 */
export const reactHolds = (el: Element) => Object.keys(el).some((k) => k.startsWith("__reactFiber$"));

/**
 * What the page runs on, for a report - the browser, whether its DOM methods
 * are its own or wrapped by a script, scripts from elsewhere than this app's
 * build, the last errors. Hosts and paths only, no words.
 */
export function pageState() {
  return {
    native: {
      removeChild: native(Node.prototype.removeChild),
      insertBefore: native(Node.prototype.insertBefore),
      appendChild: native(Node.prototype.appendChild),
      remove: native(Element.prototype.remove),
    },
    scripts: [...document.scripts]
      .map((s) => s.src)
      .filter((src) => src && !new URL(src, location.href).pathname.startsWith("/_next/"))
      .map((src) => {
        const u = new URL(src, location.href);
        return u.host + u.pathname.slice(0, 40);
      }),
    errors: [...lastErrors],
    ua: navigator.userAgent.match(/(Chrome|Version|Firefox|Mobile)\/[\w.]+/g)?.join(" ") ?? "",
    age: Math.round(performance.now() / 1000),
    vis: document.visibilityState,
  };
}

const HIDDEN = "pointer-events-none absolute size-px overflow-hidden opacity-0";
const round =
  "tap flex size-12 shrink-0 items-center justify-center rounded-full transition-colors focus-ring";

/** Reports with nothing left over, so the probe is known to have run where it ran. */
const CLEAN_EVERY = 6;
const MAX_CLEAN = 3;
const MAX_LEFT = 5;

/**
 * The composer's controls after the words, drawn the way that leaves them
 * behind wherever React's removals do not take: the ⓘ and the mic made when
 * the field empties and thrown away when it fills, the send button's arrow
 * swapped for stop's square in the same button. Out of sight and out of
 * reach, in the composer's own row, beside the controls the row draws once
 * (chat/composer.tsx).
 *
 * After every render, once React is done with what it removed (useEffect),
 * it looks for nodes of its own that are no longer the ones drawn. Any it
 * finds go to the diagnostics at once (lib/diag.ts), with whether React still
 * holds them and what the page runs on; those React has let go of are taken
 * out. Every few swaps with nothing left over it says so too, a few times a
 * page.
 */
export function RemovalProbe({ guide, mic, stop }: { guide: boolean; mic: boolean; stop: boolean }) {
  const guideRef = useRef<HTMLButtonElement>(null);
  const micRef = useRef<HTMLButtonElement>(null);
  const actRef = useRef<HTMLButtonElement>(null);
  const squareRef = useRef<HTMLSpanElement>(null);
  const arrowRef = useRef<SVGSVGElement>(null);
  const seen = useRef({ guide, stop, swaps: 0, clean: 0, left: 0 });

  useEffect(() => {
    const act = actRef.current;
    const row = act?.parentElement;
    const track = act?.firstElementChild;
    if (!act || !row || !track) return;
    const s = seen.current;
    if (s.guide !== guide || s.stop !== stop) {
      s.swaps += 1;
      s.guide = guide;
      s.stop = stop;
    }

    const drawn = new Set<Element | null>([guideRef.current, micRef.current, stop ? squareRef.current : arrowRef.current]);
    const leftover = [
      ...row.querySelectorAll(':scope > [data-probe="guide"], :scope > [data-probe="mic"]'),
      ...track.children,
    ].filter((el) => !drawn.has(el));

    if (leftover.length === 0) {
      if (s.swaps < (s.clean + 1) * CLEAN_EVERY || s.clean >= MAX_CLEAN) return;
      s.clean += 1;
      diag("composer", { ev: "probe", swaps: s.swaps, left: 0, ...pageState() }, { always: true });
      return;
    }

    if (s.left >= MAX_LEFT) return;
    s.left += 1;
    const found = leftover.map((el) => {
      const what = el.getAttribute("data-probe") ?? (el.tagName.toLowerCase() === "svg" ? "arrow" : "square");
      const react = reactHolds(el);
      // Only what React has let go of: one it still holds, it will remove itself.
      if (!react) el.remove();
      return { what, react, removed: !el.isConnected };
    });
    console.warn("입력창 탐침: React 가 지운 노드가 남아 있었어요", found);
    diag("composer", { ev: "probe", swaps: s.swaps, left: leftover.length, found, ...pageState() }, { always: true });
  });

  return (
    <>
      {guide ? (
        <button ref={guideRef} type="button" tabIndex={-1} inert aria-hidden data-probe="guide" className={cn(round, HIDDEN)}>
          <Info aria-hidden className="size-5" strokeWidth={1.8} />
        </button>
      ) : null}
      {mic ? (
        <button ref={micRef} type="button" tabIndex={-1} inert aria-hidden data-probe="mic" className={cn(round, HIDDEN)}>
          <Mic aria-hidden className="size-5" strokeWidth={1.8} />
        </button>
      ) : null}
      <button ref={actRef} type="button" tabIndex={-1} inert aria-hidden data-probe="act" className={cn(round, HIDDEN)}>
        <span className="flex size-9 items-center justify-center rounded-full">
          {stop ? (
            <span ref={squareRef} className="size-2.5 rounded-[2px] bg-current" />
          ) : (
            <ArrowUp ref={arrowRef} className="size-[18px]" />
          )}
        </span>
      </button>
    </>
  );
}
