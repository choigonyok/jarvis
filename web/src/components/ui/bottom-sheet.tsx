"use client"

import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"

import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

/**
 * The console's one sheet: up from the bottom on a phone, clear of the notch
 * and the home indicator, the page's own ground; on a wide screen a column in
 * the middle with its top corners rounded rather than a strip as wide as the
 * screen. How tall it may get and how wide it sits are the two things that
 * differ between the sheets.
 */
const bottomSheetVariants = cva(
  "inset-x-safe pb-safe gap-0 border-edge-soft bg-background",
  {
    variants: {
      width: {
        md: "sm:mx-auto sm:max-w-lg sm:rounded-t-2xl sm:border-x",
        lg: "sm:mx-auto sm:max-w-xl sm:rounded-t-2xl sm:border-x",
        xl: "sm:mx-auto sm:max-w-2xl sm:rounded-t-2xl sm:border-x",
        full: "",
      },
      height: {
        fit: "max-h-[88dvh]",
        tall: "max-h-[92dvh]",
        full: "h-[92dvh] max-h-[92dvh]",
      },
    },
    defaultVariants: {
      width: "md",
      height: "fit",
    },
  }
)

function BottomSheetContent({
  className,
  width,
  height,
  keepFocus = true,
  onOpenAutoFocus,
  ...props
}: Omit<React.ComponentProps<typeof SheetContent>, "side"> &
  VariantProps<typeof bottomSheetVariants> & {
    /**
     * Opening a sheet does not throw up the keyboard (or a date picker)
     * before anything in it has been read: focus stays put unless a sheet
     * says otherwise.
     */
    keepFocus?: boolean
  }) {
  return (
    <SheetContent
      side="bottom"
      onOpenAutoFocus={(e) => {
        if (keepFocus) e.preventDefault()
        onOpenAutoFocus?.(e)
      }}
      className={cn(bottomSheetVariants({ width, height }), className)}
      {...props}
    />
  )
}

function BottomSheetHeader({
  className,
  ...props
}: React.ComponentProps<typeof SheetHeader>) {
  return (
    <SheetHeader
      className={cn("shrink-0 px-4 pt-4 pb-2 sm:px-5", className)}
      {...props}
    />
  )
}

function BottomSheetTitle({
  className,
  ...props
}: React.ComponentProps<typeof SheetTitle>) {
  return (
    <SheetTitle
      className={cn("pr-8 text-[15px] font-medium text-foreground", className)}
      {...props}
    />
  )
}

function BottomSheetDescription({
  className,
  ...props
}: React.ComponentProps<typeof SheetDescription>) {
  return (
    <SheetDescription
      className={cn("text-[12px] text-faint", className)}
      {...props}
    />
  )
}

/** What scrolls inside the sheet, under its header. */
function BottomSheetBody({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="bottom-sheet-body"
      className={cn(
        "scrollbar-hairline min-h-0 overflow-y-auto overscroll-contain px-4 pb-4 sm:px-5",
        className
      )}
      {...props}
    />
  )
}

/**
 * A sheet with a title (and a line under it) over what scrolls: the shape
 * the ledger's, the workout's and the orders' sheets each used to draw for
 * themselves.
 */
function Panel({
  open,
  onOpenChange,
  title,
  description,
  width,
  height,
  bodyClassName,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description?: React.ReactNode
  bodyClassName?: string
  children: React.ReactNode
} & VariantProps<typeof bottomSheetVariants>) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <BottomSheetContent
        width={width}
        height={height}
        // Without a line under the title there is nothing to describe the
        // sheet by; said so, rather than left for Radix to warn about.
        {...(description ? {} : { "aria-describedby": undefined })}
      >
        <BottomSheetHeader>
          <BottomSheetTitle>{title}</BottomSheetTitle>
          {description ? (
            <BottomSheetDescription>{description}</BottomSheetDescription>
          ) : null}
        </BottomSheetHeader>
        <BottomSheetBody className={bodyClassName}>{children}</BottomSheetBody>
      </BottomSheetContent>
    </Sheet>
  )
}

export {
  BottomSheetContent,
  BottomSheetHeader,
  BottomSheetTitle,
  BottomSheetDescription,
  BottomSheetBody,
  Panel,
  bottomSheetVariants,
}
