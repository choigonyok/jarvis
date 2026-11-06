import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"
import { Slot } from "radix-ui"

const buttonVariants = cva(
  "group/button inline-flex shrink-0 items-center justify-center rounded-lg border border-transparent bg-clip-padding text-sm font-medium whitespace-nowrap transition-all outline-none select-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 active:not-aria-[haspopup]:translate-y-px disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/80",
        outline:
          "border-border bg-background hover:bg-muted hover:text-foreground aria-expanded:bg-muted aria-expanded:text-foreground dark:border-input dark:bg-input/30 dark:hover:bg-input/50",
        secondary:
          "bg-secondary text-secondary-foreground hover:bg-[color-mix(in_oklch,var(--secondary),var(--foreground)_5%)] aria-expanded:bg-secondary aria-expanded:text-secondary-foreground",
        ghost:
          "hover:bg-muted hover:text-foreground aria-expanded:bg-muted aria-expanded:text-foreground dark:hover:bg-muted/50",
        destructive:
          "bg-destructive/10 text-destructive hover:bg-destructive/20 focus-visible:border-destructive/40 focus-visible:ring-destructive/20 dark:bg-destructive/20 dark:hover:bg-destructive/30 dark:focus-visible:ring-destructive/40",
        link: "text-primary underline-offset-4 hover:underline",
        // Jarvis. A decision: approving is the brightest control there is,
        // and only lights up green under the pointer; turning down is quiet
        // until pointed at; the turn-down said for good (a reject form's
        // last word, a 지우기 confirmed) is red from the start.
        approve:
          "border-edge bg-glass-raised text-foreground hover:border-approve/35 hover:bg-approve/12 hover:text-approve aria-expanded:bg-glass-raised",
        reject: "text-dim hover:bg-reject/10 hover:text-reject",
        refuse: "border-edge bg-input/30 text-reject hover:bg-reject/10 focus-visible:border-reject/40",
        // The one bright control of a sheet or a panel: the one that saves.
        raised: "bg-glass-raised text-foreground hover:bg-foreground/15",
        // Everything else a sheet offers: words, no surface until pointed at.
        quiet: "text-dim hover:text-foreground",
        // The same on a faint ground, where it sits beside the bright one in
        // a row of equals (a running workout's 세트 추가 · 종목 추가 · 끝내기).
        soft: "bg-glass text-dim hover:text-foreground",
        // A way to throw something away, kept faint until pointed at.
        discard: "text-faint hover:text-reject",
        // The same, given an edge where several sit in a row (a goal's,
        // a job's controls).
        lined: "border-edge-soft text-dim hover:bg-glass hover:text-foreground",
        // The voice screen's way back: an outlined capsule beside its one
        // filled button.
        pill: "rounded-full border-edge text-dim hover:border-white/20 hover:text-foreground",
        // A word to press inside a sentence.
        underline:
          "h-auto rounded-sm px-0 font-normal text-dim underline decoration-edge underline-offset-[3px] hover:text-foreground disabled:text-faint disabled:opacity-100",
      },
      size: {
        default:
          "h-8 gap-1.5 px-2.5 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2",
        xs: "h-6 gap-1 rounded-[min(var(--radius-md),10px)] px-2 text-xs in-data-[slot=button-group]:rounded-lg has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 [&_svg:not([class*='size-'])]:size-3",
        sm: "h-7 gap-1 rounded-[min(var(--radius-md),12px)] px-2.5 text-[0.8rem] in-data-[slot=button-group]:rounded-lg has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-9 gap-1.5 px-2.5 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2",
        icon: "size-8",
        "icon-xs":
          "size-6 rounded-[min(var(--radius-md),10px)] in-data-[slot=button-group]:rounded-lg [&_svg:not([class*='size-'])]:size-3",
        "icon-sm":
          "size-7 rounded-[min(var(--radius-md),12px)] in-data-[slot=button-group]:rounded-lg",
        "icon-lg": "size-9",
        // Jarvis: a thumb's size on a touch screen, a pointer's only where
        // there is a mouse (the mouse: variant, not the width).
        pair: "h-12 gap-1 rounded-[min(var(--radius-md),12px)] px-2.5 text-[13.5px] mouse:h-8 mouse:px-3 mouse:text-[0.8rem]",
        "pair-sm": "h-11 gap-1 rounded-[min(var(--radius-md),12px)] px-2.5 text-[13.5px] mouse:h-8 mouse:px-3 mouse:text-[0.8rem]",
        touch: "h-11 gap-1.5 px-4 text-[14px] mouse:h-9 mouse:text-[13px]",
        "touch-sm": "h-11 gap-1.5 px-3 text-[13.5px] font-normal mouse:h-8 mouse:text-[13px]",
        "icon-touch": "size-11 mouse:size-8",
        voice: "h-12 gap-2 px-5 text-[14px] font-normal",
        // Inside a sentence: as tall as the words (the tap class gives a
        // thumb its band where it needs one).
        inline: "h-auto gap-1 rounded-sm px-0 text-[length:inherit] font-normal",
      },
    },
    compoundVariants: [
      // Approving gets a little more room than its neighbour under a pointer.
      { variant: "approve", size: ["pair", "pair-sm"], className: "mouse:px-3.5" },
    ],
    defaultVariants: {
      variant: "default",
      // A thumb's size unless a button says otherwise (44px; a pointer's 36).
      size: "touch",
    },
  }
)

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean
  }) {
  const Comp = asChild ? Slot.Root : "button"

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Button, buttonVariants }
