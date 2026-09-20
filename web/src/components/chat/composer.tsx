"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowUp } from "lucide-react";
import { Button } from "@/components/ui/button";

export function Composer({ onSend }: { onSend: (text: string) => void }) {
  const [value, setValue] = useState("");
  const areaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const el = areaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 168)}px`;
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
      <div className="flex items-end gap-2">
        <textarea
          ref={areaRef}
          rows={1}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              send();
            }
          }}
          placeholder="Jarvis에게 메시지"
          aria-label="Jarvis에게 보낼 메시지"
          className="scrollbar-hairline max-h-[168px] flex-1 resize-none bg-transparent px-2.5 py-2 text-[14.5px] leading-relaxed text-foreground outline-none placeholder:text-faint"
        />
        {/* Deliberately quiet: the brightest control in this product is 승인. */}
        <Button
          variant="outline"
          size="icon-sm"
          onClick={send}
          disabled={!value.trim()}
          aria-label="메시지 보내기"
          className="mb-0.5 size-8 rounded-lg border-edge bg-glass-raised text-dim hover:text-foreground disabled:opacity-40 dark:bg-glass-raised dark:hover:bg-white/12"
        >
          <ArrowUp aria-hidden className="size-4" />
        </Button>
      </div>
    </div>
  );
}
