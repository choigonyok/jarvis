"use client";

import { kg, signedKg } from "@/lib/workout";
import { cn } from "@/lib/utils";

/**
 * This session's volume against last time's, as a bar against a tick.
 *
 * The same device the invest surface uses: a baseline you own, and how far
 * you are from it. There the line is what you paid; here it is what you lifted
 * last time. One question - "어디쯤인가" - asked the same way twice, so the two
 * surfaces read as one product rather than two apps.
 *
 * The tick is the thing to look at. Past it is progress; short of it is not.
 * No arrow, no badge - the mark either sits behind the bar's end or ahead of it.
 */
export function VolumeBar({
  volume,
  previous,
  className,
}: {
  volume: number;
  previous: number | null;
  className?: string;
}) {
  // Both fit inside the track with headroom, so a big jump does not clip and
  // a tie does not put the tick exactly on the edge.
  const ceiling = Math.max(volume, previous ?? 0) * 1.15 || 1;
  const width = `${(volume / ceiling) * 100}%`;
  const mark = previous ? `calc(${(previous / ceiling) * 100}% - 1px)` : null;
  const delta = previous === null ? null : volume - previous;
  // Matching last time is not beating it. Only a genuine increase earns the
  // approve hue; level is level, and saying otherwise would make the colour
  // mean nothing.
  const ahead = delta !== null && delta > 0;
  const level = delta === 0;

  return (
    <div className={className}>
      <div className="relative h-2 w-full overflow-hidden rounded-[2px] bg-well">
        <div
          className={cn(
            "absolute inset-y-0 left-0 rounded-[2px]",
            previous === null ? "bg-dim/50" : ahead ? "bg-approve/55" : "bg-dim/45",
          )}
          style={{ width }}
        />
        {/* The tick is the thing to read, so it is drawn over the bar and at
            full strength - at equal volume it sits exactly on the bar's end
            and a faint mark would simply disappear there. */}
        {mark ? (
          <span
            aria-hidden
            className="absolute inset-y-[-2px] w-[2px] rounded-full bg-foreground/80"
            style={{ left: mark }}
          />
        ) : null}
      </div>

      <p className="tnum mt-1 text-[11.5px] text-faint">
        {kg(volume)}
        {delta === null ? (
          <span className="ms-2">첫 기록</span>
        ) : (
          <span className={cn("ms-2", ahead ? "text-approve/75" : "text-dim")}>
            지난 회차 {kg(previous ?? 0)} ·{" "}
            {level ? "같음" : signedKg(delta)}
          </span>
        )}
      </p>
    </div>
  );
}
