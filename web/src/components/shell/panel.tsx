"use client";

import type { ReactNode } from "react";

/**
 * The header's popovers - notifications, usage - in one shape: the same
 * width, the same title row, the same place on a phone (pinned under the
 * header, a gutter from each edge, rather than hanging off a 44px button).
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
    <div
      role="dialog"
      aria-label={label ?? title}
      className="absolute top-full right-0 z-50 mt-1 w-[21rem] overflow-hidden rounded-xl border border-edge bg-popover shadow-lg max-sm:fixed max-sm:inset-x-3 max-sm:top-[calc(env(safe-area-inset-top)+3.5rem)] max-sm:w-auto"
    >
      <div className="flex min-h-11 items-center justify-between gap-2 border-b border-edge-soft px-3.5 py-1.5">
        <p className="text-[13px] text-foreground">{title}</p>
        {aside ? <div className="flex items-center gap-1">{aside}</div> : null}
      </div>
      {children}
    </div>
  );
}
