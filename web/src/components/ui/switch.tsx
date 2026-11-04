"use client"

import * as React from "react"
import { cn } from "cn"
import { Switch as SwitchPrimitive } from "radix-ui"

/**
 * On or off, without a hue: on is the bright track with the dark knob, off the
 * faint one - green is approval's here, and a setting being on approves
 * nothing. The ledger's sheets drew this by hand first; it is the one switch.
 * A label around it (or for it) is what the thumb presses.
 */
function Switch({
  className,
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        "peer group/switch relative inline-flex h-6 w-10 shrink-0 items-center rounded-full transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 data-disabled:cursor-not-allowed data-disabled:opacity-50 data-checked:bg-foreground/80 data-unchecked:bg-glass-raised",
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="pointer-events-none block size-5 rounded-full bg-background transition-transform motion-reduce:transition-none data-checked:translate-x-[18px] data-unchecked:translate-x-0.5"
      />
    </SwitchPrimitive.Root>
  )
}

/**
 * A setting as one line: its name (and what it does, under it) and the
 * switch, the whole line pressing it - a thumb need not find the knob.
 */
function SwitchRow({
  label,
  hint,
  className,
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root> & {
  label: React.ReactNode
  hint?: React.ReactNode
}) {
  return (
    <label
      data-slot="switch-row"
      className={cn(
        "flex min-h-11 cursor-pointer items-center justify-between gap-3 py-1 has-disabled:cursor-not-allowed has-disabled:opacity-50",
        className
      )}
    >
      <span className="min-w-0 flex-1">
        <span className="block text-[13.5px] text-foreground/90">{label}</span>
        {hint ? (
          <span className="block text-[11.5px] text-faint">{hint}</span>
        ) : null}
      </span>
      <Switch {...props} />
    </label>
  )
}

export { Switch, SwitchRow }
