"use client"

import * as React from "react"
import {
  MessageScroller as MessageScrollerPrimitive,
  useMessageScroller,
  useMessageScrollerScrollable,
} from "@shadcn/react/message-scroller"
import { cn } from "@/lib/utils"

/**
 * The conversation's scroller (@shadcn/react). It lands on the newest turn
 * before anything is painted, and follows the bottom while the reader is
 * there - an answer as it is written, a photo finishing its layout, the
 * keyboard taking room. A touch, the wheel or a key scrolling it is the
 * reader taking the view: following stops, and starts again only at the very
 * bottom. A row is gone to by its turn's id (messageId).
 *
 * What the console adds on top lives in chat/thread.tsx: when older turns are
 * asked for and where the reader is kept as they come in front, the hold at
 * the bottom while a phone's keyboard comes up, and a card a push landed on
 * being kept in view.
 */
function MessageScrollerProvider(
  props: React.ComponentProps<typeof MessageScrollerPrimitive.Provider>
) {
  return <MessageScrollerPrimitive.Provider {...props} />
}

function MessageScroller({
  className,
  ...props
}: React.ComponentProps<typeof MessageScrollerPrimitive.Root>) {
  return (
    <MessageScrollerPrimitive.Root
      data-slot="message-scroller"
      className={cn("relative flex min-h-0 flex-1 flex-col", className)}
      {...props}
    />
  )
}

/** What scrolls. Keyboard focus goes here so the arrows scroll it; it draws no ring of its own. */
function MessageScrollerViewport({
  className,
  ...props
}: React.ComponentProps<typeof MessageScrollerPrimitive.Viewport>) {
  return (
    <MessageScrollerPrimitive.Viewport
      data-slot="message-scroller-viewport"
      className={cn(
        "min-h-0 flex-1 overflow-y-auto overscroll-contain outline-none",
        className
      )}
      {...props}
    />
  )
}

/**
 * The rows, each a direct child. The room under the last row belongs here,
 * not to a wrapper: the bottom the scroller follows is this element's, and
 * room outside it would read as already being at the bottom while a line of
 * the answer is still out of sight.
 */
function MessageScrollerContent({
  className,
  ...props
}: React.ComponentProps<typeof MessageScrollerPrimitive.Content>) {
  return (
    <MessageScrollerPrimitive.Content
      data-slot="message-scroller-content"
      className={cn("flex flex-col", className)}
      {...props}
    />
  )
}

function MessageScrollerItem({
  className,
  ...props
}: React.ComponentProps<typeof MessageScrollerPrimitive.Item>) {
  return (
    <MessageScrollerPrimitive.Item
      data-slot="message-scroller-item"
      className={cn("min-w-0", className)}
      {...props}
    />
  )
}

export {
  MessageScrollerProvider,
  MessageScroller,
  MessageScrollerViewport,
  MessageScrollerContent,
  MessageScrollerItem,
  useMessageScroller,
  useMessageScrollerScrollable,
}
