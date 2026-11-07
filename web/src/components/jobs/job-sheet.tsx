"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowUp, ChevronRight, ExternalLink } from "lucide-react";
import { WithUploads } from "@/components/chat/inline-photos";
import { ProposalCard } from "@/components/chat/proposal-card";
import { AsOf } from "@/components/shell/as-of";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { BottomSheetContent, BottomSheetDescription, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { CopyButton } from "@/components/ui/copy-button";
import { Input } from "@/components/ui/input";
import { Lightbox } from "@/components/ui/lightbox";
import { Pager } from "@/components/ui/pager";
import { Sheet } from "@/components/ui/sheet";
import {
  Trace,
  TraceCollapsible,
  TraceCollapsibleContent,
  TraceCollapsibleTrigger,
  TraceDescription,
  TraceLine,
  TraceTitle,
} from "@/components/ui/trace";
import { type Job, type JobDetail, type JobEvent, LIVE, send, stateLabel, stateTone, when } from "@/lib/jobs";
import { ACTION, JOB_EVENT } from "@/lib/kinds";
import { usePaged } from "@/lib/paging";
import type { Decision, Proposal } from "@/lib/thread";
import { photoUrl } from "@/lib/uploads";
import { SAY } from "@/lib/say";
import { cn } from "@/lib/utils";


export function JobSheet({
  job,
  proposals,
  onDecide,
  onOpenChange,
  onChanged,
}: {
  job: Job | null;
  proposals: Proposal[];
  onDecide: (id: string, d: Decision, edits?: Record<string, string>, reason?: string) => Promise<string | null>;
  onOpenChange: (open: boolean) => void;
  onChanged: () => void;
}) {
  return (
    <Sheet open={job !== null} onOpenChange={onOpenChange}>
      <BottomSheetContent width="lg" height="full">
        {job ? (
          <Body key={job.id} job={job} proposals={proposals} onDecide={onDecide} onChanged={onChanged} />
        ) : null}
      </BottomSheetContent>
    </Sheet>
  );
}

function Body({
  job,
  proposals,
  onDecide,
  onChanged,
}: {
  job: Job;
  proposals: Proposal[];
  onDecide: (id: string, d: Decision, edits?: Record<string, string>, reason?: string) => Promise<string | null>;
  onChanged: () => void;
}) {
  const [detail, setDetail] = useState<JobDetail | null>(null);
  // Whether the last read of the detail failed, and when the one on screen
  // was read: a failure is said as one, not as "아직 기록이 없습니다".
  const [failed, setFailed] = useState(false);
  const [readAt, setReadAt] = useState<Date | null>(null);
  const [text, setText] = useState("");
  // What the last thing done here came to - a failure in the failure's colour,
  // with the way to try again; it read the same as success.
  const [note, setNote] = useState<{ text: string; failed?: boolean; retry?: () => void } | null>(null);

  const load = useCallback(async () => {
    const res = await fetch(`/api/jobs/jobs/${job.id}?events=300`, { cache: "no-store" }).catch(() => null);
    if (res?.ok) {
      setDetail((await res.json()) as JobDetail);
      setFailed(false);
      setReadAt(new Date());
    } else {
      setFailed(true);
    }
  }, [job.id]);

  useEffect(() => {
    // load sets state only after its fetch resolves; the rule cannot see that.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
  }, [load]);

  // Every step lands here a few seconds after it happens while a run is live.
  const live = detail?.job.running ?? job.running;
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, live ? 2_500 : 15_000);
    return () => window.clearInterval(id);
  }, [load, live]);

  const j = detail?.job ?? job;
  const card = j.waitingCard ? proposals.find((p) => p.id === j.waitingCard && p.state === "pending") : undefined;
  // A job may also be waiting on a login card, or be asked whether it should
  // go before the job on the browser (job.first); show any pending card it raised.
  const cards = useMemo(() => {
    const own = proposals.filter(
      (p) =>
        p.state === "pending" &&
        (((p.action.kind === ACTION.jobAsk || p.action.kind === ACTION.jobFirst) &&
          (p.action.input as { jobId?: number }).jobId === j.id) ||
          (p.action.kind === ACTION.jobLogin && j.sites.includes(String((p.action.input as { site?: string }).site)))),
    );
    return card && !own.includes(card) ? [card, ...own] : own;
  }, [proposals, card, j.id, j.sites]);

  const act = async (path: string, body: unknown, done: string): Promise<string | null> => {
    const err = await send(`jobs/${j.id}/${path}`, body);
    setNote(err ? { text: err, failed: true, retry: () => void act(path, body, done) } : { text: done });
    await load();
    onChanged();
    return err;
  };

  const runs = useMemo(() => group(detail?.events ?? []), [detail]);
  // Ten a page: a run is a block of lines, a record a row with its photo.
  const records = useMemo(() => detail?.records ?? [], [detail]);
  const recordsPaged = usePaged(records, { size: 10 });
  const runsPaged = usePaged(runs, { size: 10 });
  const ended = !LIVE.includes(j.state);

  return (
    <>
      <BottomSheetHeader>
        <BottomSheetTitle className="text-title leading-snug">{j.title}</BottomSheetTitle>
        <BottomSheetDescription className={cn("flex items-center gap-1.5 text-aside", stateTone(j))}>
          {/* A run going now: a light passes over what it is doing. */}
          <span className={cn(j.running && "shimmer motion-reduce:shimmer-none")}>{stateLabel(j)}</span>
          {!j.running && j.state === "active" && j.nextAt && !j.blockedSites?.length ? (
            <span className="text-faint">· 다음 {when(j.nextAt)}</span>
          ) : null}
        </BottomSheetDescription>
      </BottomSheetHeader>

      <div className="scrollbar-hairline min-h-0 flex-1 overflow-y-auto px-4 pb-4 sm:px-5">
        {j.summary ? (
          <div className="flex items-start gap-1">
            <p className="selectable min-w-0 flex-1 text-ui leading-relaxed text-ink">{j.summary}</p>
            <CopyButton text={j.summary} label="요약 복사" className="-mt-2.5 -mr-2.5" />
          </div>
        ) : null}

        {cards.map((p) => (
          <ProposalCard
            key={p.id}
            proposal={p}
            onDecide={async (d, edits, reason) => {
              const err = await onDecide(p.id, d, edits, reason);
              if (!err) {
                await load();
                onChanged();
              }
              return err;
            }}
          />
        ))}

        {records.length ? (
          <section id={recordsPaged.anchor} className="mt-5 scroll-mt-4" aria-label="지켜보는 대상">
            <ul className="space-y-1.5">
              {recordsPaged.items.map((r) => (
                <li key={r.key} className="flex gap-3 rounded-xl bg-glass p-2.5">
                  {r.image && !j.photosGone ? <PhotoTiles names={[r.image]} label={r.title} className="size-14" /> : null}
                  <div className="min-w-0 flex-1">
                    <p className="flex items-baseline gap-2">
                      <span className="min-w-0 truncate text-ui text-ink">{r.title}</span>
                      {r.status ? <span className="ms-auto shrink-0 text-aside text-dim">{r.status}</span> : null}
                    </p>
                    {r.amount != null ? (
                      <p className="tnum text-title font-medium text-foreground">{r.amount.toLocaleString("ko-KR")}원</p>
                    ) : null}
                    <p className="tnum mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-meta text-faint">
                      {Object.entries(r.metrics ?? {}).map(([k, v]) => (
                        <span key={k}>
                          {k} <span className="text-dim">{v}</span>
                        </span>
                      ))}
                      {r.url ? (
                        <a
                          href={r.url}
                          target="_blank"
                          rel="noreferrer"
                          className="inline-flex items-center gap-1 text-dim hover:text-foreground focus-ring"
                        >
                          <ExternalLink aria-hidden className="size-3" />
                          열기
                        </a>
                      ) : null}
                    </p>
                    {r.note ? <p className="mt-0.5 text-meta text-faint">{r.note}</p> : null}
                  </div>
                </li>
              ))}
            </ul>
            <Pager {...recordsPaged} unit="개" />
          </section>
        ) : null}

        <Collapsible className="mt-5 rounded-lg bg-glass px-3 py-2.5">
          <CollapsibleTrigger className="group/goal flex w-full cursor-pointer items-center gap-1 text-left text-aside text-dim focus-ring">
            맡긴 일
            <ChevronRight aria-hidden className="size-3.5 text-faint transition-transform group-aria-expanded/goal:rotate-90 motion-reduce:transition-none" />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <p className="mt-2 text-ui leading-relaxed whitespace-pre-wrap text-ink">{j.goal}</p>
            {j.instructions ? (
              <p className="mt-2 text-aside leading-relaxed whitespace-pre-wrap text-dim">{j.instructions}</p>
            ) : null}
            {j.photos.length && !j.photosGone ? (
              <div className="scrollbar-none mt-2 flex gap-1.5 overflow-x-auto">
                <PhotoTiles names={j.photos} label="작업 사진" className="size-16" />
              </div>
            ) : null}
          </CollapsibleContent>
        </Collapsible>

        <section id={runsPaged.anchor} className="mt-6 scroll-mt-4" aria-label="진행 기록">
          <h3 className="mb-2 text-aside text-dim">진행</h3>
          {detail && failed ? <AsOf at={readAt} failed onRetry={() => void load()} className="mb-2" /> : null}
          {!detail && failed ? (
            <AsOf at={null} failedText="진행 기록을 불러오지 못했어요" failed onRetry={() => void load()} />
          ) : !detail ? (
            <p className="text-aside text-faint">{SAY.loading}</p>
          ) : runs.length === 0 ? (
            <p className="text-aside text-faint">아직 기록이 없어요.</p>
          ) : (
            <Trace className="gap-4">
              {/* The newest runs are on the first page: the one going now, and two open. */}
              {runsPaged.items.map((g, i) => (
                <Run
                  key={g.key}
                  group={g}
                  live={runsPaged.page === 1 && i === 0 && j.running}
                  open={runsPaged.page === 1 && i < 2}
                  gone={!!j.photosGone}
                />
              ))}
            </Trace>
          )}
          <Pager {...runsPaged} unit="번" />
        </section>
      </div>

      <div className="border-t border-edge-soft px-4 pt-3 pb-3 sm:px-5">
        {note ? (
          <p role="status" className={cn("mb-2 text-aside", note.failed ? "text-reject" : "text-dim")}>
            {note.failed ? "전하지 못했어요: " : ""}
            {note.text}
            {note.retry ? (
              <>
                {" · "}
                <button
                  type="button"
                  onClick={note.retry}
                  className="tap-y underline decoration-reject-fill underline-offset-[3px] hover:text-reject focus-ring"
                >
                  다시 시도
                </button>
              </>
            ) : null}
          </p>
        ) : null}
        {j.state !== "cancelled" ? (
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              const t = text.trim();
              if (!t) return;
              const err = await act("instruct", { text: t }, "작업에 전했어요. 곧 반영해요.");
              if (!err) setText("");
            }}
            className="flex items-end gap-2"
          >
            <Input
              surface="glass"
              aria-label="작업에 지시하기"
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={ended ? "다시 맡길 일 (예: 가격 내려서 다시 올려줘)" : "지시하기 (예: 가격 6만 원으로 내려줘)"}
              className="min-w-0 flex-1"
            />
            <Button type="submit" variant="soft" size="icon-touch" disabled={!text.trim()} aria-label="지시 보내기" className="mouse:size-9">
              <ArrowUp aria-hidden />
            </Button>
          </form>
        ) : null}
        <div className="mt-2 flex gap-2">
          {j.state === "active" || j.state === "paused" ? (
            <Button variant="soft" size="touch-sm" disabled={j.running} onClick={() => void act("run", {}, "지금 실행해요.")} className="flex-1">
              지금 실행
            </Button>
          ) : null}
          {j.state === "active" || j.state === "waiting" ? (
            <Button variant="soft" size="touch-sm" onClick={() => void act("pause", {}, "멈췄어요.")} className="flex-1">
              멈추기
            </Button>
          ) : j.state === "paused" || j.state === "failed" ? (
            <Button variant="soft" size="touch-sm" onClick={() => void act("resume", {}, "다시 시작해요.")} className="flex-1">
              다시 시작
            </Button>
          ) : null}
          {!ended || j.state === "failed" ? (
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="soft" size="touch-sm" className="flex-1">
                  그만두기
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{j.title} 작업을 그만둘까요?</AlertDialogTitle>
                  <AlertDialogDescription>
                    그만둔 작업은 다시 시작하거나 지시할 수 없어요. 같은 일은 대화에서 새로 맡겨야 해요.
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>두기</AlertDialogCancel>
                  <AlertDialogAction onClick={() => void act("cancel", {}, "그만뒀어요.")}>그만두기</AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          ) : null}
        </div>
      </div>
    </>
  );
}

type RunGroup = { key: string; runId?: number; at: string; events: JobEvent[] };

/**
 * A screen the run took (capture), where it took it: a tile that opens the
 * photo full screen, in the app (ui/lightbox.tsx) - as a link it took a
 * home-screen app out to Safari. Gone with the job's photos after it ended,
 * it says so instead of leaving a broken picture.
 */
function Shot({ name, gone, album }: { name: string; gone: boolean; album: string[] }) {
  const [open, setOpen] = useState(false);
  if (gone) return <span className="text-faint">찍은 화면 (지웠어요)</span>;
  return (
    <span className="flex flex-col gap-1">
      <span>화면을 찍었어요</span>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label="작업이 찍은 화면 크게 보기"
        className="block w-fit overflow-hidden rounded-lg bg-well focus-ring"
      >
        {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
        <img src={photoUrl(name)} alt="작업이 찍은 화면" loading="lazy" className="block max-h-48 w-auto max-w-[16rem] object-contain" />
      </button>
      {open ? (
        <Lightbox
          items={album.map((n) => ({ src: photoUrl(n), alt: "작업이 찍은 화면" }))}
          start={Math.max(0, album.indexOf(name))}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </span>
  );
}

/**
 * Photos as tiles that open full screen, one after another under the finger:
 * the job's own photos, what a record shows.
 */
function PhotoTiles({ names, label, className }: { names: string[]; label: string; className: string }) {
  const [open, setOpen] = useState<number | null>(null);
  return (
    <>
      {names.map((n, i) => (
        <button
          key={n}
          type="button"
          onClick={() => setOpen(i)}
          aria-label={`${label} 크게 보기`}
          className={cn("block shrink-0 overflow-hidden rounded-lg bg-well focus-ring", className)}
        >
          {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
          <img src={photoUrl(n)} alt="" loading="lazy" className="size-full object-cover" />
        </button>
      ))}
      {open !== null ? (
        <Lightbox items={names.map((n) => ({ src: photoUrl(n), alt: label }))} start={open} onClose={() => setOpen(null)} />
      ) : null}
    </>
  );
}

/** Events arrive newest first; group each run's lines together, keeping that order. */
function group(events: JobEvent[]): RunGroup[] {
  const out: RunGroup[] = [];
  for (const e of events) {
    // The run's own header already says it started.
    if (e.kind === JOB_EVENT.runStart) continue;
    const last = out[out.length - 1];
    const same = last && (e.runId ? last.runId === e.runId : !last.runId);
    if (same) {
      last.events.push(e);
      last.at = e.at;
    } else {
      out.push({ key: `${e.runId ?? "x"}-${e.id}`, runId: e.runId, at: e.at, events: [e] });
    }
  }
  return out;
}

const TONE: Record<string, string> = {
  step: "text-faint",
  note: "text-dim",
  progress: "text-ink",
  report: "text-foreground",
  card: "text-mine",
  answer: "text-mine",
  instruction: "text-mine",
  error: "text-reject",
  finish: "text-approve",
  schedule: "text-faint",
  shot: "text-dim",
};

/**
 * One run: when, and how many things it did, opening to what it did step by
 * step. The run going now has a light passing over its line (Trace).
 */
function Run({ group: g, live, open, gone }: { group: RunGroup; live: boolean; open: boolean; gone: boolean }) {
  const steps = g.events.filter((e) => e.kind === JOB_EVENT.step).length;
  // The screens the run took, oldest first: each opens among them.
  const shots = g.events.filter((e) => e.kind === JOB_EVENT.shot).map((e) => e.text).reverse();
  const time = when(g.at);
  return (
    <TraceCollapsible defaultOpen={open || live}>
      <TraceCollapsibleTrigger className="text-aside">
        <TraceLine status={live ? "running" : undefined} className="gap-1.5 text-aside text-faint">
          <TraceTitle className="tnum">
            {time} {g.runId ? (live ? "실행 중" : "실행") : "기록"}
          </TraceTitle>
          {steps ? <TraceDescription>· 동작 {steps}</TraceDescription> : null}
        </TraceLine>
      </TraceCollapsibleTrigger>
      <TraceCollapsibleContent className="pl-3">
        <ol className="space-y-1">
          {g.events.map((e) => (
            <li
              key={e.id}
              className={cn(
                "leading-relaxed whitespace-pre-wrap",
                e.kind === JOB_EVENT.step ? "text-meta" : "text-aside",
                TONE[e.kind] ?? "text-dim",
              )}
            >
              {e.kind === JOB_EVENT.shot ? <Shot name={e.text} gone={gone} album={shots} /> : <WithUploads text={e.text} />}
            </li>
          ))}
        </ol>
      </TraceCollapsibleContent>
    </TraceCollapsible>
  );
}
