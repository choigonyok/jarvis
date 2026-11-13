"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

/** The reasons a card is usually turned down, a tap each. */
const QUICK_REASONS = ["지금은 필요 없어", "이미 했어", "틀렸어", "다시 묻지 마"];


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
  label = "반려",
}: {
  busy?: boolean;
  onCancel: () => void;
  onReject: (reason: string) => void;
  /** The answer being given, as its button says it: 반려, or the card's own word for it. */
  label?: string;
}) {
  const [why, setWhy] = useState("");
  return (
    <div className="anim-settle space-y-2">
      <p className="px-1 text-aside text-dim">
        {label === "반려" ? "반려하는" : "이렇게 답하는"} 이유가 있으면 알려 주세요. 다음 제안에 반영해요.
      </p>
      {/* The chip lit is the reason written: typing another lights none. */}
      <ToggleGroup
        type="single"
        aria-label="자주 쓰는 이유"
        variant="chip"
        tone="reject"
        size="chip"
        spacing={1.5}
        value={QUICK_REASONS.includes(why) ? why : ""}
        onValueChange={(v) => {
          if (v) setWhy(v);
        }}
        className="flex-wrap"
      >
        {QUICK_REASONS.map((r) => (
          <ToggleGroupItem key={r} value={r} className="tap-y">
            {r}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <Input
        value={why}
        onChange={(e) => setWhy(e.target.value)}
        placeholder="직접 적기 (비워도 돼요)"
        aria-label="반려 이유"
        maxLength={300}
        className="mouse:h-8 mouse:text-ui"
      />
      <div className="grid grid-cols-2 gap-2 sm:flex sm:justify-end sm:gap-1.5">
        <Button
          variant="quiet"
          size="pair"
          onClick={onCancel}
          disabled={busy}
        >
          취소
        </Button>
        <Button
          variant="refuse"
          size="pair"
          onClick={() => onReject(why)}
          disabled={busy}
        >
          {label}
        </Button>
      </div>
    </div>
  );
}
