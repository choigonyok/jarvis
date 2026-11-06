"use client"

import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Tabs as TabsPrimitive } from "radix-ui"

/**
 * Views of one screen, moved between with the arrow keys and tied to what
 * they show (aria-controls). The root draws nothing of its own: a screen
 * places its list and its panels where its layout has them.
 */
function Tabs({
  className,
  orientation = "horizontal",
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Root>) {
  return (
    <TabsPrimitive.Root
      data-slot="tabs"
      data-orientation={orientation}
      className={cn("group/tabs", className)}
      {...props}
    />
  )
}

/**
 * segment: the views in a track, the current one lifted out of it.
 * pills: lit capsules with no track, for a row that may run sideways.
 */
const tabsListVariants = cva("group/tabs-list", {
  variants: {
    variant: {
      segment: "inline-flex w-fit items-center gap-0.5 rounded-lg border border-edge-soft bg-glass p-0.5",
      pills: "flex items-center gap-1",
    },
  },
  defaultVariants: {
    variant: "segment",
  },
})

const TabsListContext = React.createContext<"segment" | "pills">("segment")

function TabsList({
  className,
  variant = "segment",
  ...props
}: React.ComponentProps<typeof TabsPrimitive.List> &
  VariantProps<typeof tabsListVariants>) {
  return (
    <TabsListContext.Provider value={variant ?? "segment"}>
      <TabsPrimitive.List
        data-slot="tabs-list"
        data-variant={variant}
        className={cn(tabsListVariants({ variant }), className)}
        {...props}
      />
    </TabsListContext.Provider>
  )
}

// Plain classes rather than variants keyed to the list's attribute, so a
// screen's own sizes (cn) win over these.
const tabsTriggerVariants = cva(
  "tap-y inline-flex items-center justify-center gap-1.5 whitespace-nowrap transition-colors focus-ring disabled:pointer-events-none disabled:opacity-50 data-active:bg-glass-raised data-active:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        segment: "min-h-8 rounded-md px-3 text-aside text-faint hover:text-dim",
        pills: "h-9 shrink-0 rounded-full px-3.5 text-ui text-dim hover:text-foreground mouse:h-7",
      },
    },
  }
)

function TabsTrigger({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
  const variant = React.useContext(TabsListContext)
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      className={cn(tabsTriggerVariants({ variant }), className)}
      {...props}
    />
  )
}

function TabsContent({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return (
    <TabsPrimitive.Content
      data-slot="tabs-content"
      className={cn("outline-none", className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent, tabsListVariants }
