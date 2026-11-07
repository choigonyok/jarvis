"use client";

import { Marker, MarkerContent } from "@/components/ui/marker";
import { Trace, TraceLine, TraceTitle } from "@/components/ui/trace";
import type { Step } from "@/lib/thread";

/**
 * What Jarvis is doing while there is nothing yet to read: 생각하는 중 until
 * its first step, then each step of this stretch in a line - those done
 * fainter, the one being done now with a light passing over its words
 * (shimmer; none where motion is reduced). The first words of the answer take
 * its place.
 */
export function Working({ steps }: { steps: Step[] }) {
  if (steps.length === 0) {
    return (
      <Marker className="min-h-6 text-ui text-dim">
        <MarkerContent className="shimmer motion-reduce:shimmer-none">생각하는 중</MarkerContent>
      </Marker>
    );
  }
  const now = steps.length - 1;
  return (
    <Trace className="gap-1 py-0.5">
      {steps.map((s, i) => (
        <TraceLine key={`${s.at}:${i}`} status={i === now ? "running" : "done"} className="leading-5">
          <TraceTitle>{s.text}</TraceTitle>
        </TraceLine>
      ))}
    </Trace>
  );
}
