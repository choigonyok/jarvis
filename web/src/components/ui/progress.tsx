"use client"

import * as React from "react"
import { cn } from "cn"
import { Progress as ProgressPrimitive } from "radix-ui"

/**
 * How far along: a thin ground and a fill. The fill's colour says what the
 * amount means where it is used (a limit nearly reached, a list finished in
 * approving green), so it is the caller's (`indicatorClassName`). The fill is
 * drawn by its width rather than slid in, so a fill may also animate itself
 * (a checklist's last tick draws the rest of its line).
 */
function Progress({
  className,
  indicatorClassName,
  indicatorStyle,
  inline = false,
  value,
  ...props
}: React.ComponentProps<typeof ProgressPrimitive.Root> & {
  indicatorClassName?: string
  indicatorStyle?: React.CSSProperties
  /** Drawn with spans, for a bar inside a button (the header's usage lines). */
  inline?: boolean
}) {
  const share = Math.min(100, Math.max(0, value ?? 0))
  const Box = inline ? "span" : "div"
  return (
    <ProgressPrimitive.Root
      data-slot="progress"
      value={value == null ? null : share}
      asChild
      {...props}
    >
      <Box className={cn("relative block h-1 w-full overflow-hidden rounded-full bg-foreground/12", className)}>
        <ProgressPrimitive.Indicator data-slot="progress-indicator" asChild>
          <Box
            className={cn("block h-full rounded-full bg-foreground/60", indicatorClassName)}
            style={{ width: `${share}%`, ...indicatorStyle }}
          />
        </ProgressPrimitive.Indicator>
      </Box>
    </ProgressPrimitive.Root>
  )
}

export { Progress }
