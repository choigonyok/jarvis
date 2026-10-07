"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowUp, Mic } from "lucide-react";
import { Button } from "@/components/ui/button";

export function Composer({
  onSend,
  onVoice,
  onFocusChange,
}: {
  onSend: (text: string) => void;
  /** Switches to voice. Only offered where the toggle below is hidden. */
  onVoice?: () => void;
  onFocusChange?: (focused: boolean) => void;
}) {
  const [value, setValue] = useState("");
  const areaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const el = areaRef.current;
    if (!el) return;
    el.style.height = "auto";
    // Five lines on a phone would leave the transcript a strip; the cap is
    // lower where the screen is shorter.
    const cap = window.innerWidth < 640 ? 112 : 168;
    el.style.height = `${Math.min(el.scrollHeight, cap)}px`;
  }, [value]);

  function send() {
    const text = value.trim();
    if (!text) return;
    onSend(text);
    setValue("");
    areaRef.current?.focus();
  }

  return (
    <div className="rounded-2xl border border-edge bg-glass px-2 py-2 backdrop-blur-xl transition-colors focus-within:border-white/18">
      <div className="flex items-end gap-1">
        <textarea
          ref={areaRef}
          rows={1}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onFocus={() => onFocusChange?.(true)}
          onBlur={() => onFocusChange?.(false)}
          onKeyDown={(e) => {
            // Enter on a phone keyboard is a newline, not a send: there is no
            // shift to hold, and a stray send is worse than a stray line.
            if (
              e.key === "Enter" &&
              !e.shiftKey &&
              !e.nativeEvent.isComposing &&
              window.innerWidth >= 640
            ) {
              e.preventDefault();
              send();
            }
          }}
          placeholder="Jarvis에게 메시지"
          aria-label="Jarvis에게 보낼 메시지"
          className="scrollbar-hairline max-h-28 flex-1 resize-none bg-transparent px-2.5 py-2.5 text-[14.5px] leading-relaxed text-foreground outline-none placeholder:text-faint sm:max-h-[168px] sm:py-2"
        />

        {/* The mode switch lives here on a phone. Stacking a separate toggle
            under the composer would put three bars between the transcript and
            the bottom of the screen; a mic beside the send button is the
            grammar every messaging app already taught. */}
        {onVoice ? (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onVoice}
            aria-label="음성으로 말하기"
            className="tap mb-0.5 shrink-0 rounded-lg text-faint hover:text-foreground sm:hidden"
          >
            <Mic aria-hidden className="size-[18px]" />
          </Button>
        ) : null}

        {/* Deliberately quiet: the brightest control in this product is 승인. */}
        <Button
          variant="outline"
          size="icon-sm"
          onClick={send}
          disabled={!value.trim()}
          aria-label="메시지 보내기"
          className="tap mb-0.5 shrink-0 rounded-lg border-edge bg-glass-raised text-dim hover:text-foreground disabled:opacity-40 sm:size-8 sm:min-h-0 sm:min-w-0 dark:bg-glass-raised dark:hover:bg-white/12"
        >
          <ArrowUp aria-hidden className="size-[18px] sm:size-4" />
        </Button>
      </div>
    </div>
  );
}
