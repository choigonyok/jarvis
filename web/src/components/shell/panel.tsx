"use client";

import type { ReactNode } from "react";
import { PopoverContent } from "@/components/ui/popover";

/**
 * The header's popovers - notifications, usage - in one shape: the same
 * width, the same title row, the same place on a phone (pinned under the
 * header, a gutter from each edge, rather than hanging off a 44px button:
 * globals.css puts the popover back there below the tablet width).
 *
 * A Popover: a tap outside or Esc closes it and the focus goes back to the
 * button that opened it. It appears without a flourish, as it always has.
 */
export function HeaderPanel({
  title,
  aside,
  children,
  label,
}: {
  title: string;
  /** Right of the title: actions, or a quiet fact ("14:05 기준"). */
  aside?: ReactNode;
  children: ReactNode;
  label?: string;
}) {
  return (
    <PopoverContent
      align="end"
      sideOffset={4}
      data-header-panel=""
      aria-label={label ?? title}
      className="w-[21rem] gap-0 overflow-hidden rounded-xl border border-edge bg-popover p-0 text-base shadow-lg ring-0 data-open:animate-none data-closed:animate-none max-sm:w-full"
    >
      <div className="flex min-h-11 items-center justify-between gap-2 border-b border-edge-soft px-3.5 py-1.5">
        <p className="text-ui text-foreground">{title}</p>
        {aside ? <div className="flex items-center gap-1">{aside}</div> : null}
      </div>
      {children}
    </PopoverContent>
  );
}
