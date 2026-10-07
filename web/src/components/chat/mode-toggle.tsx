"use client";

import { cn } from "@/lib/utils";

export type ChatMode = "text" | "voice";

const modes: { value: ChatMode; label: string }[] = [
  { value: "text", label: "채팅" },
  { value: "voice", label: "음성" },
];

/**
 * Two ways to hold the same conversation. It sits on the composer rather than
 * in the header because it changes how you talk to Jarvis, not where you are.
 */
export function ModeToggle({
  mode,
  onChange,
}: {
  mode: ChatMode;
  onChange: (mode: ChatMode) => void;
}) {
  return (
    <div
      role="radiogroup"
      aria-label="대화 방식"
      className="inline-flex items-center gap-0.5 rounded-lg border border-edge-soft bg-glass p-0.5 backdrop-blur-md"
    >
      {modes.map((item) => {
        const active = mode === item.value;
        return (
          <button
            key={item.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(item.value)}
            className={cn(
              "rounded-md px-2.5 py-1 text-[12px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
              active
                ? "bg-glass-raised text-foreground"
                : "text-faint hover:text-dim",
            )}
          >
            {item.label}
          </button>
        );
      })}
    </div>
  );
}
