import type { ReactNode } from "react";
import { Retry } from "@/components/shell/retry";
import { cn } from "@/lib/utils";

/**
 * What could not be read or done, where it was to be: the words behind a red
 * edge - the edge a turned-down card takes - and, where reading again is the
 * way back, 다시 시도 under them.
 *
 * One of the console's three ways of saying a failure (web/DESIGN.md "실패는
 * 실패로 보인다"): this, for a screen or a part of one that did not come; the
 * AsOf line (shell/as-of.tsx) over figures still shown from before; and a red
 * line beside the control that was pressed. Eight screens drew this one by hand.
 */
export function Failed({
  children,
  onRetry,
  quiet,
  className,
}: {
  children: ReactNode;
  onRetry?: () => unknown;
  /** A smaller line under something else that did come (a venue among several). */
  quiet?: boolean;
  className?: string;
}) {
  return (
    <div className={className}>
      <p
        role="status"
        className={cn("border-l-2 pl-3 leading-relaxed text-dim", quiet ? "border-reject-fill text-aside" : "border-reject text-ui")}
      >
        {children}
      </p>
      {onRetry ? <Retry onRetry={onRetry} /> : null}
    </div>
  );
}
