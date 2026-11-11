"use client";

import { memo, useEffect, useRef } from "react";
import Link from "next/link";
import type { Job } from "@/lib/jobs";
import { HttpError, useResource } from "@/lib/use-resource";
import { cn } from "@/lib/utils";

/** Rows shown before the rest are counted ("외 N개"). */
const ROWS = 2;
const NONE: Job[] = [];

/**
 * The jobs with a run in flight now, read from mind (?running=1 - only those,
 * not every job there is). Read every few seconds while one runs, so the step
 * on screen moves; and again at once when something says a run may have
 * started or ended - an answer finished (a job made in it starts within a
 * second), or a job's news arrived.
 */
export function useRunningJobs({ thinking, newsAt }: { thinking: boolean; newsAt: string }) {
  const read = useResource<Job[]>(
    "jobs:running",
    async (signal) => {
      const res = await fetch("/api/jobs/jobs?running=1", { cache: "no-store", signal });
      if (!res.ok) throw new HttpError(res.status);
      const body = (await res.json()) as { jobs?: Job[] };
      // Only those running, whatever the service sent: one that does not yet
      // read ?running= answers with every job.
      return (body.jobs ?? []).filter((j) => j.running);
    },
    { interval: (jobs) => (jobs?.length ? 4_000 : 20_000) },
  );
  const reload = read.reload;
  const wasThinking = useRef(thinking);
  useEffect(() => {
    if (wasThinking.current && !thinking) void reload();
    wasThinking.current = thinking;
  }, [thinking, reload]);
  const lastNews = useRef(newsAt);
  useEffect(() => {
    if (newsAt && newsAt !== lastNews.current) void reload();
    lastNews.current = newsAt;
  }, [newsAt, reload]);
  return { jobs: read.data ?? NONE, reload };
}

/**
 * The jobs working behind the conversation, one line each under the waiting
 * line, in its grammar: a dot, what it is, a word for what a tap does. The
 * dot is the 작업 tab's own for a run in flight - green, breathing - and the
 * step it is on carries the light that passes over a job's running state in
 * its sheet. A tap opens that sheet here, its steps arriving as they happen,
 * without leaving the conversation. There only while a run is in flight.
 */
function RunningJobsView({ jobs, onOpen }: { jobs: Job[]; onOpen: (id: number) => void }) {
  if (jobs.length === 0) return null;
  const shown = jobs.length > ROWS ? jobs.slice(0, ROWS - 1) : jobs;
  const rest = jobs.length - shown.length;
  const row =
    "tap mx-auto flex w-full max-w-page items-center gap-2.5 px-4 text-left outline-none focus-visible:bg-glass-raised sm:px-8 mouse:min-h-9";
  return (
    <div className="inset-x-safe shrink-0 border-t border-edge-soft bg-glass" aria-label="진행 중인 작업">
      {shown.map((j) => (
        <button key={j.id} type="button" onClick={() => onOpen(j.id)} className={cn(row, "group")}>
          <span aria-hidden className="anim-breathe size-[5px] shrink-0 rounded-full bg-approve" />
          <span className="min-w-0 flex-1 truncate text-aside text-dim">
            <span className="text-ink">{j.title}</span>
            {j.currentStep ? (
              <>
                {" · "}
                <span className="shimmer motion-reduce:shimmer-none">{j.currentStep}</span>
              </>
            ) : (
              " 진행 중"
            )}
          </span>
          <span className="shrink-0 rounded-lg px-2 text-aside text-foreground transition-colors group-hover:text-ink">열기</span>
        </button>
      ))}
      {rest > 0 ? (
        <Link href="/jobs" className={cn(row, "group")}>
          <span aria-hidden className="anim-breathe size-[5px] shrink-0 rounded-full bg-approve" />
          <span className="min-w-0 flex-1 truncate text-aside text-dim">
            작업 <span className="tnum text-ink">{rest}개</span> 더 진행 중
          </span>
          <span className="shrink-0 rounded-lg px-2 text-aside text-foreground transition-colors group-hover:text-ink">작업 탭</span>
        </Link>
      ) : null}
    </div>
  );
}

// Drawn again only when the jobs change: the thread above it renders on every
// frame of an answer.
export const RunningJobs = memo(RunningJobsView);
