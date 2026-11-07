"use client";

import { useState } from "react";
import { ThumbsDown, ThumbsUp } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { TurnFeedback } from "@/lib/thread";
import { cn } from "@/lib/utils";

/** Leaves (or, with "", takes off) a mark on an answer; resolves to what to say when it could not. */
export type MarkAnswer = (turnId: string, mark: "good" | "bad" | "", note?: string) => Promise<string | null>;

/**
 * What an answer is usually wrong for, a tap each: the kinds of miss the
 * conversation's tests are kept by (scripts/convo).
 */
const WRONG_FOR = ["사실이 틀렸어", "시키지 않은 걸 했어", "너무 길어", "다른 걸 물었어"];

/**
 * The two marks under an answer, in the copy button's grammar: quiet until
 * pressed, then in the signal colour of what was said - good in approve
 * green, wrong in reject red. The mark that is set takes itself off; 틀렸어요
 * asks why first (onWrong).
 */
export function MarkButtons({
  feedback,
  busy,
  onGood,
  onWrong,
  onClear,
  className,
}: {
  feedback?: TurnFeedback;
  busy?: boolean;
  onGood: () => void;
  onWrong: () => void;
  onClear: () => void;
  className?: string;
}) {
  const good = feedback?.mark === "good";
  const bad = feedback?.mark === "bad";
  // A thumb's width each and a gap between them - they mean opposite things.
  // Under a mouse, the mark's own size.
  const mark =
    "tap-y flex h-5 min-w-11 items-center justify-center rounded-md transition-colors focus-ring disabled:opacity-40 mouse:min-w-7";
  return (
    <span className={cn("flex items-center gap-1 mouse:gap-0", className)} data-search-skip>
      <button
        type="button"
        disabled={busy}
        onClick={good ? onClear : onGood}
        aria-pressed={good}
        aria-label={good ? "좋았어요 표시 지우기" : "좋았어요"}
        title="좋았어요"
        className={cn(mark, good ? "text-approve" : "text-faint hover:text-foreground")}
      >
        <ThumbsUp aria-hidden className="size-3.5" strokeWidth={good ? 2.2 : 1.8} />
      </button>
      <button
        type="button"
        disabled={busy}
        onClick={bad ? onClear : onWrong}
        aria-pressed={bad}
        aria-label={bad ? "틀렸어요 표시 지우기" : "틀렸어요"}
        title="틀렸어요"
        className={cn(mark, bad ? "text-reject" : "text-faint hover:text-foreground")}
      >
        <ThumbsDown aria-hidden className="size-3.5" strokeWidth={bad ? 2.2 : 1.8} />
      </button>
    </span>
  );
}

/** Why an answer was marked wrong, under it - the words Jarvis is told. */
export function WrongNote({ feedback }: { feedback?: TurnFeedback }) {
  if (feedback?.mark !== "bad" || !feedback.note) return null;
  return (
    <p className="mt-1.5 text-aside leading-snug text-dim" data-search-skip>
      <span className="text-reject">틀렸어요</span> · {feedback.note}
    </p>
  );
}

/**
 * 틀렸어요 asks, once, what was wrong - optional, a tap for the usual misses,
 * the reject form's shape made small. What is said goes to Jarvis before its
 * next turn, and with the mark into what the conversation is tested on.
 */
export function WrongForm({
  busy,
  error,
  onCancel,
  onSave,
}: {
  busy?: boolean;
  error?: string | null;
  onCancel: () => void;
  onSave: (why: string) => void;
}) {
  const [why, setWhy] = useState("");
  return (
    <div className="anim-settle mt-2 max-w-[30rem] space-y-2" data-search-skip>
      <p className="px-1 text-aside text-dim">무엇이 틀렸는지 알려 주면 다음 답부터 고쳐요.</p>
      <ToggleGroup
        type="single"
        aria-label="자주 틀리는 것"
        variant="chip"
        tone="reject"
        size="chip"
        spacing={1.5}
        value={WRONG_FOR.includes(why) ? why : ""}
        onValueChange={(v) => {
          if (v) setWhy(v);
        }}
        className="flex-wrap"
      >
        {WRONG_FOR.map((r) => (
          <ToggleGroupItem key={r} value={r} className="tap-y">
            {r}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <Input
        value={why}
        onChange={(e) => setWhy(e.target.value)}
        placeholder="직접 적기 (비워도 돼요)"
        aria-label="틀린 이유"
        maxLength={500}
        className="mouse:h-8 mouse:text-ui"
      />
      {error ? <p className="px-1 text-aside text-reject">{error}</p> : null}
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
          onClick={() => onSave(why)}
          disabled={busy}
        >
          틀렸다고 남기기
        </Button>
      </div>
    </div>
  );
}

/**
 * The marks' state for one answer: saving, asking why, and what failed - kept
 * by the row, which draws the buttons in two places (the rail on a wide
 * screen, under the answer on a phone).
 */
export function useAnswerMarks(turnId: string, onMark?: MarkAnswer) {
  const [busy, setBusy] = useState(false);
  const [asking, setAsking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const save = async (mark: "good" | "bad" | "", note?: string) => {
    if (!onMark) return;
    setBusy(true);
    setError(null);
    const failed = await onMark(turnId, mark, note);
    setBusy(false);
    setError(failed);
    if (!failed) setAsking(false);
  };
  return {
    busy,
    asking,
    error,
    good: () => void save("good"),
    wrong: () => {
      setError(null);
      setAsking(true);
    },
    clear: () => void save(""),
    cancel: () => {
      setAsking(false);
      setError(null);
    },
    saveWrong: (why: string) => void save("bad", why),
  };
}
