"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Copy, X } from "lucide-react";
import { cn } from "@/lib/utils";

/** Puts text on the clipboard; false when the browser would not have it. */
async function toClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // No clipboard API here: a field selected and copied the old way.
    const field = document.createElement("textarea");
    field.value = text;
    field.setAttribute("readonly", "");
    field.style.cssText = "position:fixed;opacity:0";
    document.body.append(field);
    field.select();
    try {
      return document.execCommand("copy");
    } catch {
      return false;
    } finally {
      field.remove();
    }
  }
}

/**
 * Copies what a row says. A long press opens nothing on a touch screen here
 * (globals.css), so what is worth taking elsewhere has this: quiet until
 * pressed, then the mark says it went.
 */
export function CopyButton({ text, label, className }: { text: string; label: string; className?: string }) {
  const [said, setSaid] = useState<"copied" | "failed" | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  async function copy() {
    const ok = await toClipboard(text);
    setSaid(ok ? "copied" : "failed");
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setSaid(null), 1600);
  }

  const words = said === "copied" ? "복사했어요" : said === "failed" ? "복사하지 못했어요" : label;
  return (
    <button
      type="button"
      onClick={() => void copy()}
      aria-label={words}
      title={words}
      className={cn(
        "flex size-11 shrink-0 items-center justify-center rounded-md text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 mouse:size-8",
        className,
      )}
    >
      {said === "copied" ? (
        <Check aria-hidden className="anim-settle size-3.5 text-approve" />
      ) : said === "failed" ? (
        <X aria-hidden className="anim-settle size-3.5 text-reject" />
      ) : (
        <Copy aria-hidden className="size-3.5" />
      )}
      <span aria-live="polite" className="sr-only">
        {said ? words : ""}
      </span>
    </button>
  );
}
