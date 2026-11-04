"use client"

import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"
import { Toggle as TogglePrimitive } from "radix-ui"

/**
 * A choice that stays pressed. The console's are of a few kinds, told by how
 * the pressed one shows: lifted out of a track (segment), a lit capsule with
 * no track (pill), a box among boxes (tile), and a bordered chip that takes a
 * colour only when the colour means something - a reason for turning down in
 * reject red, a place of one's own in one's own violet (tone).
 */
const toggleVariants = cva(
  "group/toggle inline-flex items-center justify-center gap-1.5 whitespace-nowrap transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default:
          "rounded-lg bg-transparent text-sm font-medium hover:bg-muted hover:text-foreground aria-pressed:bg-muted data-[state=on]:bg-muted",
        outline:
          "rounded-lg border border-input bg-transparent text-sm font-medium hover:bg-muted aria-pressed:bg-muted data-[state=on]:bg-muted",
        segment:
          "rounded-md text-faint hover:text-dim data-[state=on]:bg-glass-raised data-[state=on]:text-foreground",
        pill: "rounded-full text-faint hover:text-dim data-[state=on]:bg-glass-raised data-[state=on]:text-foreground",
        tile: "rounded-lg border border-edge-soft text-dim hover:bg-glass data-[state=on]:border-edge data-[state=on]:bg-glass-raised data-[state=on]:text-foreground",
        chip: "rounded-full border border-edge-soft text-dim hover:text-foreground data-[state=on]:border-edge data-[state=on]:bg-glass-raised data-[state=on]:text-foreground",
      },
      tone: {
        neutral: "",
        reject:
          "data-[state=on]:border-reject/40 data-[state=on]:bg-reject/10 data-[state=on]:text-reject data-[state=on]:hover:text-reject",
        mine: "border-edge data-[state=on]:border-mine/45 data-[state=on]:bg-mine/12 data-[state=on]:text-foreground",
      },
      size: {
        default: "h-8 min-w-8 px-2.5",
        sm: "h-7 min-w-7 px-2.5 text-[0.8rem]",
        lg: "h-9 min-w-9 px-2.5",
        // A thumb's on a touch screen, a pointer's under a mouse.
        touch: "min-h-11 px-3 text-[13px] mouse:min-h-9 mouse:text-[12.5px]",
        chip: "h-9 px-3 text-[12.5px] mouse:h-7 mouse:text-[12px]",
        segment: "tap-y min-h-8 px-3 text-[12.5px]",
      },
    },
    defaultVariants: {
      variant: "default",
      tone: "neutral",
      size: "default",
    },
  }
)

function Toggle({
  className,
  variant = "default",
  tone = "neutral",
  size = "default",
  ...props
}: React.ComponentProps<typeof TogglePrimitive.Root> &
  VariantProps<typeof toggleVariants>) {
  return (
    <TogglePrimitive.Root
      data-slot="toggle"
      className={cn(toggleVariants({ variant, tone, size, className }))}
      {...props}
    />
  )
}

export { Toggle, toggleVariants }
