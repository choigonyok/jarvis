"use client"

import * as React from "react"
import { cn } from "cn"

import { CopyButton } from "@/components/ui/copy-button"

/**
 * Something literal - a command the CLI would run, code in an answer - in
 * the well: recessed, not outlined, as the decision card is the console's
 * one edge. Set in JetBrains Mono and never re-wrapped, because a broken
 * line changes what a command says; it scrolls sideways instead. Copying
 * sits beside it, not over it, so no word is hidden under the button, and
 * it is there on a touch screen too (no hover to find it by).
 */
function CodeBlock({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="code-block"
      className={cn(
        "group/code-block flex min-w-0 items-start rounded-md bg-well has-[>[data-slot=code-block-content]:focus-visible]:ring-3 has-[>[data-slot=code-block-content]:focus-visible]:ring-ring/50",
        className
      )}
      {...props}
    />
  )
}

/** The lines, unwrapped; the code element inside is the caller's. */
function CodeBlockContent({
  className,
  ...props
}: React.ComponentProps<"pre">) {
  return (
    <pre
      data-slot="code-block-content"
      dir="ltr"
      // Reachable from the keyboard, to scroll it sideways with the arrows.
      tabIndex={0}
      className={cn(
        "scrollbar-hairline min-w-0 flex-1 overflow-x-auto px-3 py-2.5 font-mono text-[12px] outline-none",
        className
      )}
      {...props}
    />
  )
}

function CodeBlockCopyButton({
  value,
  label,
  className,
}: {
  value: string
  label: string
  className?: string
}) {
  return (
    <CopyButton
      text={value}
      label={label}
      className={cn("-ms-1 me-0.5 mt-0.5 mouse:mt-1 mouse:me-1", className)}
    />
  )
}

export { CodeBlock, CodeBlockContent, CodeBlockCopyButton }
