import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

/**
 * A field as the console sets one: a thumb's height on a touch screen and 16px
 * type there (Safari zooms into anything smaller), a pointer's size only
 * where there is a mouse. Two grounds: the sheets' glass, and the well a card
 * or a form sets its fields into.
 */
const inputVariants = cva(
  "flex h-11 w-full min-w-0 rounded-lg border border-edge-soft px-3 text-field text-foreground transition-[color,box-shadow] file:inline-flex file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-faint selection:bg-primary selection:text-primary-foreground focus-visible:border-edge focus-ring disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 mouse:h-9 mouse:text-ui",
  {
    variants: {
      surface: {
        glass: "bg-glass",
        well: "bg-well",
      },
    },
    defaultVariants: {
      surface: "well",
    },
  }
)

function Input({
  className,
  type,
  surface,
  ...props
}: React.ComponentProps<"input"> & VariantProps<typeof inputVariants>) {
  return (
    <input
      type={type}
      data-slot="input"
      data-surface={surface ?? "well"}
      className={cn(inputVariants({ surface }), className)}
      {...props}
    />
  )
}

export { Input, inputVariants }
