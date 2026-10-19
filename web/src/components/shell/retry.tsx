"use client";

import { useState } from "react";

/**
 * Under a screen's "불러오지 못했어요": the same read, again, now. Before this
 * the way back was to leave the tab and come back, or reload the app.
 */
export function Retry({ onRetry }: { onRetry: () => unknown }) {
  const [busy, setBusy] = useState(false);
  return (
    <button
      type="button"
      disabled={busy}
      onClick={async () => {
        setBusy(true);
        try {
          await onRetry();
        } finally {
          setBusy(false);
        }
      }}
      className="tap mt-3 inline-flex items-center justify-center rounded-lg bg-glass-raised px-4 text-[13px] text-foreground transition-colors outline-none hover:bg-foreground/15 focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 mouse:min-h-9"
    >
      {busy ? "불러오는 중…" : "다시 시도"}
    </button>
  );
}
