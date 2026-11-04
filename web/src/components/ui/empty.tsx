import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"
import { Slot } from "radix-ui"

/**
 * Nothing here yet, said as what to do about it: what would be here, and the
 * one control that starts it. No picture - the words are the invitation.
 * `align="start"` where the empty list is the page itself (목표), centred
 * where it sits in the middle of something (출발, the map).
 */
function Empty({
  className,
  align = "center",
  ...props
}: React.ComponentProps<"div"> & { align?: "center" | "start" }) {
  return (
    <div
      data-slot="empty"
      data-align={align}
      className={cn(
        "group/empty flex w-full min-w-0 flex-col items-center justify-center gap-5 text-center data-[align=start]:items-start data-[align=start]:text-left",
        className
      )}
      {...props}
    />
  )
}

function EmptyHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty-header"
      className={cn(
        "flex max-w-[30rem] flex-col items-center gap-1.5 group-data-[align=start]/empty:items-start",
        className
      )}
      {...props}
    />
  )
}

const emptyMediaVariants = cva(
  "mb-2 flex shrink-0 items-center justify-center [&_svg]:pointer-events-none [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: "bg-transparent",
        icon: "flex size-8 shrink-0 items-center justify-center rounded-lg bg-glass text-dim [&_svg:not([class*='size-'])]:size-4",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function EmptyMedia({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof emptyMediaVariants>) {
  return (
    <div
      data-slot="empty-icon"
      data-variant={variant}
      className={cn(emptyMediaVariants({ variant, className }))}
      {...props}
    />
  )
}

/** The line that says what is missing; `asChild` makes it the page's heading. */
function EmptyTitle({
  className,
  asChild = false,
  ...props
}: React.ComponentProps<"div"> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "div"
  return (
    <Comp
      data-slot="empty-title"
      className={cn("text-[15px] leading-snug font-medium text-foreground/90", className)}
      {...props}
    />
  )
}

function EmptyDescription({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty-description"
      className={cn(
        "text-[13.5px] leading-relaxed break-keep text-pretty text-dim [&_a]:underline [&_a]:decoration-edge [&_a]:underline-offset-[3px] [&_a:hover]:text-foreground",
        className
      )}
      {...props}
    />
  )
}

function EmptyContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="empty-content"
      className={cn(
        "flex w-full max-w-sm min-w-0 flex-col items-center gap-2.5 group-data-[align=start]/empty:items-start",
        className
      )}
      {...props}
    />
  )
}

export {
  Empty,
  EmptyHeader,
  EmptyTitle,
  EmptyDescription,
  EmptyContent,
  EmptyMedia,
}
