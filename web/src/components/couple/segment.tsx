"use client";

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { cn } from "@/lib/utils";

/** One of a few, as a row of cells in one frame - the console's segment. */
export function Segment<T extends string>({
  value,
  options,
  onChange,
  label,
  className,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
  label: string;
  className?: string;
}) {
  return (
    <ToggleGroup
      type="single"
      aria-label={label}
      variant="segment"
      size="segment"
      spacing={0.5}
      value={value}
      onValueChange={(v) => {
        if (v) onChange(v as T);
      }}
      className={cn("w-full rounded-lg border border-edge-soft bg-glass p-0.5", className)}
    >
      {options.map((o) => (
        <ToggleGroupItem key={o.value} value={o.value} className="min-h-9 flex-1 mouse:min-h-0 mouse:py-1">
          {o.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
