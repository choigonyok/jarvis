"use client";

import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { keepValue, keptValue } from "@/lib/keep";

/** The reasons a card is usually turned down, a tap each. */
const QUICK_REASONS = ["지금은 필요 없어", "이미 했어", "틀렸어", "다시 묻지 마"];


/**
 * 반려 asks, once, why - optional, one tap for the usual reasons. The reason is
 * kept with the card and is what the next suggestion of the kind reads, so it
 * is asked wherever a card can be turned down: the conversation, the record,
 * the calendar. Enter sends it, as the button does; Escape is 취소.
 */
export function RejectForm({
  busy,
  onCancel,
  onReject,
  label = "반려",
  keepKey,
}: {
  busy?: boolean;
  onCancel: () => void;
  onReject: (reason: string) => void;
  /** The answer being given, as its button says it: 반려, or the card's own word for it. */
  label?: string;
  /**
   * Where the reason as written is kept while the app is open (lib/keep.ts),
   * by the card: drawn again - another tab and back, a send refused - the
   * form has it still. Whoever names the place lets go of it with the card.
   */
  keepKey?: string;
}) {
  const [why, setWhy] = useState(() => (keepKey ? (keptValue<string>(keepKey) ?? "") : ""));
  useEffect(() => {
    if (keepKey) keepValue(keepKey, why || undefined);
  }, [keepKey, why]);
  // 취소 lets go of the reason too: the next 반려 starts from nothing.
  const cancel = () => {
    if (keepKey) keepValue(keepKey, undefined);
    onCancel();
  };

  // Escape lets go of the form, and of nothing else: a sheet the card stands
  // in (a job's) stays open. The sheet hears Escape on the document before
  // anything inside it does, so the key is taken on its way down, at the
  // window - only from inside this form, and only while 취소 could be pressed.
  const form = useRef<HTMLFormElement>(null);
  const latest = useRef({ busy, cancel });
  useEffect(() => {
    latest.current = { busy, cancel };
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.isComposing || latest.current.busy) return;
      if (!(e.target instanceof Node) || !form.current?.contains(e.target)) return;
      e.preventDefault();
      e.stopPropagation();
      latest.current.cancel();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);

  return (
    // Enter in the field presses the button that sends: one way out, shut
    // while it is busy.
    <form
      ref={form}
      onSubmit={(e) => {
        e.preventDefault();
        if (!busy) onReject(why);
      }}
      className="anim-settle space-y-2"
    >
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
        enterKeyHint="send"
        className="mouse:h-8 mouse:text-ui"
      />
      <div className="grid grid-cols-2 gap-2 sm:flex sm:justify-end sm:gap-1.5">
        <Button
          type="button"
          variant="quiet"
          size="pair"
          onClick={cancel}
          disabled={busy}
        >
          취소
        </Button>
        <Button
          type="submit"
          variant="refuse"
          size="pair"
          disabled={busy}
        >
          {label}
        </Button>
      </div>
    </form>
  );
}
