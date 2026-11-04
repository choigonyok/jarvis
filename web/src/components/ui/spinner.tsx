import * as React from "react"
import { cn } from "cn"
import { Loader2Icon, type LucideIcon } from "lucide-react"

/**
 * Something is on its way. A turning mark, stopped where motion is reduced;
 * the icon is the caller's where the thing turning means something of its
 * own (the refresh arrow that was being pulled).
 */
function Spinner({
  className,
  icon: Icon = Loader2Icon,
  ...props
}: React.ComponentProps<"svg"> & { icon?: LucideIcon }) {
  return (
    <Icon
      data-slot="spinner"
      role="status"
      aria-label="불러오는 중"
      className={cn("size-4 animate-spin motion-reduce:animate-none", className)}
      {...props}
    />
  )
}

export { Spinner }
