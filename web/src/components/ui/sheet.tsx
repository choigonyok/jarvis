"use client"

import * as React from "react"
import { cn } from "@/lib/utils"
import { Dialog as SheetPrimitive } from "radix-ui"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { XIcon } from "lucide-react"

function Sheet({ ...props }: React.ComponentProps<typeof SheetPrimitive.Root>) {
  return <SheetPrimitive.Root data-slot="sheet" {...props} />
}

function SheetTrigger({
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Trigger>) {
  return <SheetPrimitive.Trigger data-slot="sheet-trigger" {...props} />
}

function SheetClose({
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Close>) {
  return <SheetPrimitive.Close data-slot="sheet-close" {...props} />
}

function SheetPortal({
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Portal>) {
  return <SheetPrimitive.Portal data-slot="sheet-portal" {...props} />
}

function SheetOverlay({
  className,
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Overlay>) {
  return (
    <SheetPrimitive.Overlay
      data-slot="sheet-overlay"
      className={cn(
        "fixed inset-0 z-50 bg-black/50 duration-100 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
        className
      )}
      {...props}
    />
  )
}

/**
 * A sheet is a place of its own in the browser's history: opening one adds an
 * entry, and going back - an edge swipe, Android's back - closes it, one sheet
 * at a time, where it went to the screen before or, in a home-screen app, out
 * of the app. Closed any other way, the entry it added goes
 * too; closed by going to another screen from inside it, the entry is left to
 * that screen's history. A sheet holding words not yet saved (held) does not
 * go back: its entry is put back and it asks first.
 */
function useSheetHistory(close: () => void, held: React.RefObject<boolean>, ask: () => void) {
  React.useEffect(() => {
    let depth = 0
    let path = ""
    let popped = false
    const onPop = () => {
      if (!depth || popped) return
      if ((Number(window.history.state?.jarvisSheet) || 0) < depth) {
        if (held.current) {
          window.history.pushState({ ...window.history.state, jarvisSheet: depth }, "")
          ask()
          return
        }
        popped = true
        close()
      }
    }
    // A moment later, not at once: a strict-mode remount must not leave an
    // entry behind it.
    const push = window.setTimeout(() => {
      depth = (Number(window.history.state?.jarvisSheet) || 0) + 1
      path = window.location.pathname
      window.history.pushState({ ...window.history.state, jarvisSheet: depth }, "")
      window.addEventListener("popstate", onPop)
    }, 0)
    return () => {
      window.clearTimeout(push)
      window.removeEventListener("popstate", onPop)
      if (depth && !popped && window.location.pathname === path && Number(window.history.state?.jarvisSheet) === depth) {
        window.history.back()
      }
    }
  }, [close, held, ask])
}

/**
 * The history entry of a sheet that is open: drawn inside the sheet's content,
 * which is there only while it is open (and closing) - the content's wrapper
 * is drawn for a closed sheet too, and three closed sheets of a screen had
 * pushed three entries as it opened.
 */
function SheetHistory({ close, held, ask }: { close: () => void; held: React.RefObject<boolean>; ask: () => void }) {
  useSheetHistory(close, held, ask)
  return null
}

/** How far down a sheet is pulled before letting go closes it, and how fast a flick must be. */
const DISMISS_PX = 96
const DISMISS_SPEED = 0.6

/**
 * A toast is not the page around a sheet: a tap on one (a push shown in the
 * app, 되돌리기) goes to the toast and leaves the sheet as it is - the toast
 * says where it goes. The toasters take taps over an open sheet
 * (globals.css), so the touch reaches them at all.
 */
export function keepForToasts(e: { target: EventTarget | null; preventDefault: () => void }) {
  if (e.target instanceof Element && e.target.closest("[data-sonner-toaster]")) e.preventDefault()
}

function SheetContent({
  className,
  children,
  side = "right",
  showCloseButton = true,
  style,
  onInteractOutside,
  onEscapeKeyDown,
  guard = false,
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Content> & {
  side?: "top" | "right" | "bottom" | "left"
  showCloseButton?: boolean
  /**
   * Words typed into it and not saved yet. Every way out but saving - a tap
   * outside, Esc, a pull down, going back, the ✕ - then asks before it goes:
   * the sheets are made to close at a touch, and a form in one lost a long
   * memo or a page of steps to a thumb that only meant to scroll.
   */
  guard?: boolean
}) {
  const content = React.useRef<HTMLDivElement>(null)
  const closer = React.useRef<HTMLButtonElement>(null)
  const close = React.useCallback(() => closer.current?.click(), [])
  // Read where a way out is taken, which is not where the sheet draws.
  const held = React.useRef(guard)
  React.useLayoutEffect(() => {
    held.current = guard
  })
  const [asking, setAsking] = React.useState(false)
  const ask = React.useCallback(() => setAsking(true), [])
  const leave = React.useCallback(() => (held.current ? setAsking(true) : close()), [close])

  // Pulled down by its top - the handle and the title - a bottom sheet goes,
  // as a phone's own sheets do: the ✕ sits in the corner a thumb reaches
  // last. Only under a finger; a mouse has the ✕ and the page around it.
  const drag = React.useRef<{ y: number; t: number; dy: number } | null>(null)
  const pull = {
    onPointerDown: (e: React.PointerEvent<HTMLDivElement>) => {
      const el = content.current
      if (side !== "bottom" || e.pointerType === "mouse" || !el) return
      if (e.clientY - el.getBoundingClientRect().top > 72) return
      if ((e.target as Element).closest("button, a, input, textarea, select, label, [role='button'], [contenteditable='true']")) return
      drag.current = { y: e.clientY, t: e.timeStamp, dy: 0 }
      try {
        el.setPointerCapture(e.pointerId)
      } catch {
        // A pointer the browser does not hold (not a live touch): the moves still come.
      }
    },
    onPointerMove: (e: React.PointerEvent<HTMLDivElement>) => {
      const d = drag.current
      const el = content.current
      if (!d || !el) return
      d.dy = Math.max(0, e.clientY - d.y)
      el.style.transition = "none"
      el.style.transform = `translateY(${d.dy}px)`
    },
    onPointerUp: (e: React.PointerEvent<HTMLDivElement>) => {
      const d = drag.current
      const el = content.current
      drag.current = null
      if (!d || !el) return
      const speed = d.dy / Math.max(1, e.timeStamp - d.t)
      el.style.transition = "transform 180ms ease-out, opacity 180ms ease-out"
      const away = d.dy > DISMISS_PX || (d.dy > 32 && speed > DISMISS_SPEED)
      if (away && held.current) {
        // Back up into place, and the question where the closing would be.
        el.style.transform = ""
        ask()
      } else if (away) {
        el.style.transform = "translateY(100%)"
        el.style.opacity = "0"
        // Its own way down, not the closing animation over it from the top.
        window.setTimeout(() => {
          el.style.animation = "none"
          close()
        }, 180)
      } else {
        el.style.transform = ""
      }
    },
  }

  return (
    <SheetPortal>
      <SheetOverlay />
      <SheetPrimitive.Content
        ref={content}
        data-slot="sheet-content"
        data-side={side}
        className={cn(
          "fixed z-50 flex flex-col gap-4 bg-popover bg-clip-padding text-sm text-popover-foreground shadow-lg transition duration-200 ease-in-out data-[side=bottom]:inset-x-0 data-[side=bottom]:bottom-0 data-[side=bottom]:h-auto data-[side=bottom]:border-t data-[side=left]:inset-y-0 data-[side=left]:left-0 data-[side=left]:h-full data-[side=left]:w-3/4 data-[side=left]:border-r data-[side=right]:inset-y-0 data-[side=right]:right-0 data-[side=right]:h-full data-[side=right]:w-3/4 data-[side=right]:border-l data-[side=top]:inset-x-0 data-[side=top]:top-0 data-[side=top]:h-auto data-[side=top]:border-b data-[side=left]:sm:max-w-sm data-[side=right]:sm:max-w-sm data-open:animate-in data-open:fade-in-0 data-[side=bottom]:data-open:slide-in-from-bottom-10 data-[side=left]:data-open:slide-in-from-left-10 data-[side=right]:data-open:slide-in-from-right-10 data-[side=top]:data-open:slide-in-from-top-10 data-closed:animate-out data-closed:fade-out-0 data-[side=bottom]:data-closed:slide-out-to-bottom-10 data-[side=left]:data-closed:slide-out-to-left-10 data-[side=right]:data-closed:slide-out-to-right-10 data-[side=top]:data-closed:slide-out-to-top-10",
          className
        )}
        style={style}
        {...pull}
        onPointerCancel={pull.onPointerUp}
        onInteractOutside={(e) => {
          onInteractOutside?.(e)
          keepForToasts(e)
          if (!e.defaultPrevented && held.current) {
            e.preventDefault()
            ask()
          }
        }}
        onEscapeKeyDown={(e) => {
          onEscapeKeyDown?.(e)
          if (!e.defaultPrevented && held.current) {
            e.preventDefault()
            ask()
          }
        }}
        {...props}
      >
        {side === "bottom" ? (
          // The handle: where the sheet is pulled from.
          <span
            aria-hidden
            data-slot="sheet-handle"
            className="pointer-events-none absolute top-1.5 left-1/2 h-1 w-9 -translate-x-1/2 rounded-full bg-faint/45 mouse:hidden"
          />
        ) : null}
        {children}
        <SheetHistory close={close} held={held} ask={ask} />
        {/* What closing by hand, by the handle or by going back clicks. */}
        <SheetPrimitive.Close ref={closer} tabIndex={-1} aria-hidden className="hidden" />
        {showCloseButton && (
          // A thumb's size, as every control here (44px; a pointer's 32).
          <Button
            data-slot="sheet-close"
            type="button"
            variant="ghost"
            className="absolute top-2 right-2"
            size="icon-touch"
            aria-label="닫기"
            onClick={leave}
          >
            <XIcon />
          </Button>
        )}
        {/* Drawn inside the sheet, so a tap or the focus in it is the sheet's
            own and not a way out (Radix follows the React tree, portal or not). */}
        <AlertDialog open={asking} onOpenChange={setAsking}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>쓰던 내용을 버릴까요?</AlertDialogTitle>
              <AlertDialogDescription>닫으면 적은 것이 사라져요. 되돌릴 수 없어요.</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>두기</AlertDialogCancel>
              <AlertDialogAction onClick={close}>버리기</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </SheetPrimitive.Content>
    </SheetPortal>
  )
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-header"
      className={cn("flex flex-col gap-0.5 p-4", className)}
      {...props}
    />
  )
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-footer"
      className={cn("mt-auto flex flex-col gap-2 p-4", className)}
      {...props}
    />
  )
}

function SheetTitle({
  className,
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Title>) {
  return (
    <SheetPrimitive.Title
      data-slot="sheet-title"
      className={cn(
        "font-heading text-base font-medium text-foreground",
        className
      )}
      {...props}
    />
  )
}

function SheetDescription({
  className,
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Description>) {
  return (
    <SheetPrimitive.Description
      data-slot="sheet-description"
      className={cn("text-sm text-muted-foreground", className)}
      {...props}
    />
  )
}

export {
  Sheet,
  SheetTrigger,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetFooter,
  SheetTitle,
  SheetDescription,
}
