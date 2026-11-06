import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

/**
 * Lines of text, set like the console's fields (ui/input.tsx): 16px type on a
 * touch screen, the well or the glass behind it, and as tall as what is in it.
 */
const textareaVariants = cva(
  "flex field-sizing-content min-h-20 w-full rounded-lg border border-edge-soft px-3 py-2.5 text-field leading-relaxed text-foreground transition-[color,box-shadow] placeholder:text-faint focus-visible:border-edge focus-ring disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 mouse:text-ui",
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

function Textarea({
  className,
  surface,
  ...props
}: React.ComponentProps<"textarea"> & VariantProps<typeof textareaVariants>) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(textareaVariants({ surface }), className)}
      {...props}
    />
  )
}

export { Textarea, textareaVariants }
