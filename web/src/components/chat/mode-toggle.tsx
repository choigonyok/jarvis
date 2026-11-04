"use client";

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

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
    <ToggleGroup
      type="single"
      aria-label="대화 방식"
      variant="segment"
      spacing={0.5}
      value={mode}
      onValueChange={(v) => {
        if (v) onChange(v as ChatMode);
      }}
      className="rounded-lg border border-edge-soft bg-glass p-0.5"
    >
      {modes.map((item) => (
        <ToggleGroupItem key={item.value} value={item.value} className="h-auto px-2.5 py-1 text-[12px]">
          {item.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
