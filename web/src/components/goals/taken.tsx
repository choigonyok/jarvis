import { cn } from "@/lib/utils";

/**
 * A change mind has taken in and not done yet (lib/goals.ts): said the way a
 * decision on its way is, by the breathing dot - not in red, since nothing
 * failed, and with nothing to press again.
 */
export function Taken({ className }: { className?: string }) {
  return (
    <p role="status" className={cn("flex items-center gap-2 text-aside text-dim", className)}>
      <span aria-hidden className="anim-breathe size-[5px] shrink-0 rounded-full bg-dim" />
      <span>
        받았어요<span className="text-faint"> · 반영 중</span>
      </span>
    </p>
  );
}
