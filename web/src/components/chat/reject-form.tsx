"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** The reasons a card is usually turned down, a tap each. */
const QUICK_REASONS = ["지금은 필요 없어", "이미 했어", "틀렸어", "다시 묻지 마"];

/** The field style a card's inputs share. */
export const cardInput =
  "w-full rounded-lg border border-edge-soft bg-well px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:border-edge focus-visible:ring-3 focus-visible:ring-ring/50 mouse:text-[13.5px]";

/**
 * 반려 asks, once, why - optional, one tap for the usual reasons. The reason is
 * kept with the card and is what the next suggestion of the kind reads, so it
 * is asked wherever a card can be turned down: the conversation, the record,
 * the calendar.
 */
export function RejectForm({
  busy,
  onCancel,
  onReject,
}: {
  busy?: boolean;
  onCancel: () => void;
  onReject: (reason: string) => void;
}) {
  const [why, setWhy] = useState("");
  return (
    <div className="anim-settle space-y-2">
      <p className="px-1 text-[12px] text-dim">반려하는 이유가 있으면 알려 주세요. 다음 제안에 반영해요.</p>
      <div className="flex flex-wrap gap-1.5">
        {QUICK_REASONS.map((r) => (
          <button
            key={r}
            type="button"
            onClick={() => setWhy(r)}
            aria-pressed={why === r}
            className={cn(
              "h-9 rounded-full border px-3 text-[12.5px] mouse:h-7 mouse:text-[12px]",
              why === r ? "border-reject/40 bg-reject/10 text-reject" : "border-edge-soft text-dim hover:text-foreground",
            )}
          >
            {r}
          </button>
        ))}
      </div>
      <input
        value={why}
        onChange={(e) => setWhy(e.target.value)}
        placeholder="직접 적기 (비워도 돼요)"
        aria-label="반려 이유"
        maxLength={300}
        className={cn(cardInput, "h-11 mouse:h-8")}
      />
      <div className="grid grid-cols-2 gap-2 sm:flex sm:justify-end sm:gap-1.5">
        <Button
          variant="ghost"
          size="sm"
          onClick={onCancel}
          disabled={busy}
          className="h-12 text-[13.5px] text-dim mouse:h-8 mouse:px-3 mouse:text-[0.8rem]"
        >
          취소
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() => onReject(why)}
          disabled={busy}
          className="h-12 border-edge text-[13.5px] text-reject hover:bg-reject/10 mouse:h-8 mouse:px-3.5 mouse:text-[0.8rem]"
        >
          반려
        </Button>
      </div>
    </div>
  );
}
