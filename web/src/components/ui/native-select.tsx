import * as React from "react"
import { cn } from "@/lib/utils"
import { ChevronDownIcon } from "lucide-react"

type NativeSelectProps = Omit<React.ComponentProps<"select">, "size"> & {
  size?: "sm" | "default"
  /**
   * Its ground, a field's two (ui/input.tsx). Glass unless said: the selects
   * sit in the sheets' forms, and beside them, among glass fields.
   */
  surface?: "glass" | "well"
}

function NativeSelect({
  className,
  size = "default",
  surface = "glass",
  ...props
}: NativeSelectProps) {
  return (
    <div
      className={cn(
        "group/native-select relative w-fit shrink-0 has-[select:disabled]:opacity-50",
        className
      )}
      data-slot="native-select-wrapper"
      data-size={size}
    >
      <select
        data-slot="native-select"
        data-size={size}
        // The phone's own picker opens on it: a wheel under the thumb, which a
        // drawn list would only imitate. 16px on a touch screen so Safari
        // does not zoom; a pointer's size only under a mouse. What is chosen
        // reads as a field's value: its ground, its ink and its left edge -
        // the page's ground and an explaining grey read as a different field.
        className={cn(
          "h-11 w-full min-w-0 appearance-none rounded-lg border border-edge-soft py-1 pr-8 pl-3 text-field text-foreground transition-colors select-none focus-visible:border-edge focus-ring disabled:pointer-events-none disabled:cursor-not-allowed aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 mouse:h-8 mouse:text-aside data-[size=sm]:h-9 data-[size=sm]:mouse:h-7",
          surface === "well" ? "bg-well" : "bg-glass"
        )}
        {...props}
      />
      <ChevronDownIcon className="pointer-events-none absolute top-1/2 right-2.5 size-4 -translate-y-1/2 text-faint select-none mouse:size-3.5" aria-hidden="true" data-slot="native-select-icon" />
    </div>
  )
}

function NativeSelectOption({
  className,
  ...props
}: React.ComponentProps<"option">) {
  return (
    <option
      data-slot="native-select-option"
      className={cn("bg-[Canvas] text-[CanvasText]", className)}
      {...props}
    />
  )
}

function NativeSelectOptGroup({
  className,
  ...props
}: React.ComponentProps<"optgroup">) {
  return (
    <optgroup
      data-slot="native-select-optgroup"
      className={cn("bg-[Canvas] text-[CanvasText]", className)}
      {...props}
    />
  )
}

export { NativeSelect, NativeSelectOptGroup, NativeSelectOption }
