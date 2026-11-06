"use client"

import type { CSSProperties } from "react"
import { Toaster as Sonner, type ToasterProps } from "sonner"

/**
 * Sonner, set for the console: the phone's theme, one toast at a time (a new one
 * takes the place of the last rather than stacking up over the page), and no
 * look of its own - each toast is drawn by its caller, on the glass the
 * floating things use. Its movement is one slide in and out, and none where
 * motion is reduced.
 */
function Toaster({ style, toastOptions, ...props }: ToasterProps) {
  return (
    <Sonner
      theme="system"
      visibleToasts={1}
      expand={false}
      containerAriaLabel="알림"
      toastOptions={{ unstyled: true, ...toastOptions }}
      style={{ "--width": "min(26rem, calc(100vw - 2rem))", ...style } as CSSProperties}
      {...props}
    />
  )
}

export { Toaster }
