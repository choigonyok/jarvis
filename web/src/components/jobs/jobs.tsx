"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Header, TabBar } from "@/components/shell/header";
import { Retry } from "@/components/shell/retry";
import { StandingBar } from "@/components/shell/standing-bar";
import { ChecklistCard } from "@/components/chat/checklist";
import { JobSheet } from "@/components/jobs/job-sheet";
import { type BlockedSite, type Job, LIVE, stateLabel, stateTone, when } from "@/lib/jobs";
import { onResume } from "@/lib/resume";
import { ago } from "@/lib/spending";
import { isPending, type Checklist } from "@/lib/thread";
import { photoUrl } from "@/lib/uploads";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

/**
 * The jobs handed over in conversation and how far each has got. A job runs
 * in the background on its own schedule; this is where a person watches it
 * work (live, step by step, while it runs), answers what it asks, and steers
 * it with a new instruction.
 */
/** A list with something left on it. */
const unfinished = (l: Checklist) => l.status === "active" && l.items.some((i) => !i.done);

// The lists opened here, for as long as the app is: leaving the tab and
// coming back finds them as they were left.
const unfolded = new Set<string>();

export function Jobs() {
  const { proposals, connection, decide, checklists, tick, dropItem, closeList, loadOpenLists } = useThread();
  const waiting = proposals.filter(isPending);
  // The lists with something left when they were read - kept on screen as
  // they are ticked off here (a list finished now says so until next time).
  const [listIds, setListIds] = useState<string[]>([]);
  // Folded by default: a few lists to a screen, each opened when wanted.
  const [opened, setOpened] = useState<ReadonlySet<string>>(() => new Set(unfolded));
  const flip = useCallback((id: string) => {
    setOpened((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      unfolded.clear();
      for (const x of next) unfolded.add(x);
      return next;
    });
  }, []);

  const [jobs, setJobs] = useState<Job[] | null>(null);
  const [blocked, setBlocked] = useState<BlockedSite[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [openId, setOpenId] = useState<number | null>(null);
  const [showEnded, setShowEnded] = useState(false);

  const load = useCallback(async () => {
    void loadOpenLists().then((open) => {
      if (open) setListIds((cur) => [...new Set([...cur, ...open.filter(unfinished).map((l) => l.id)])]);
    });
    try {
      const res = await fetch("/api/jobs/jobs", { cache: "no-store" });
      if (!res.ok) throw new Error(String(res.status));
      const body = (await res.json()) as { jobs: Job[]; blockedSites: BlockedSite[] };
      setJobs(body.jobs);
      setBlocked(body.blockedSites);
      setError(null);
    } catch {
      setError("작업을 불러오지 못했습니다. 잠시 뒤 다시 시도하세요.");
    }
  }, [loadOpenLists]);

  useEffect(() => {
    // load sets state only after its fetch resolves; the rule cannot see that.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
  }, [load]);
  // Back to the app, or a pull to refresh: read again at once.
  useEffect(() => onResume(load), [load]);

  // While something runs, progress should move on screen without a pull.
  const running = jobs?.some((j) => j.running) ?? false;
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, running ? 3_000 : 20_000);
    return () => window.clearInterval(id);
  }, [load, running]);

  // What is open and not done - and a list made meanwhile (it reaches this
  // screen on the stream) joins them.
  const lists = [
    ...new Set([...listIds, ...Object.values(checklists).filter(unfinished).map((l) => l.id)]),
  ]
    .map((id) => checklists[id])
    .filter((l): l is Checklist => Boolean(l) && l.status === "active");
  const live = (jobs ?? []).filter((j) => LIVE.includes(j.state));
  const ended = (jobs ?? []).filter((j) => !LIVE.includes(j.state));
  const open = jobs?.find((j) => j.id === openId) ?? null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-4 pb-12 sm:px-8 sm:pt-7 sm:pb-8">
          {error && !jobs ? (
            <div className="py-16 text-center">
              <p className="text-[13px] text-dim">{error}</p>
              <Retry onRetry={load} />
            </div>
          ) : null}

          {blocked.length > 0 ? (
            <section role="alert" className="mb-5 rounded-xl border border-mine/25 bg-mine/[0.06] px-4 py-3.5">
              <p className="text-[13.5px] text-foreground/90">
                {blocked.map((b) => b.site).join(", ")} 에 다시 로그인해야 합니다.
              </p>
              <p className="mt-1 text-[12px] leading-relaxed text-dim">
                이 사이트를 쓰는 작업은 쉬고 있습니다. 화면 탭에서 로그인한 뒤 대화에 올라온 로그인 카드를 승인하면
                이어서 합니다.
              </p>
              <Link
                href="/screen"
                className="tap mt-3 inline-flex min-h-10 items-center rounded-lg border border-edge px-3 text-[13px] text-foreground outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50 mouse:min-h-8"
              >
                화면 탭 열기
              </Link>
            </section>
          ) : null}

          {lists.length > 0 ? (
            <section className="mb-6" aria-label="안 끝난 체크리스트" data-focus="checklists">
              <h2 className="mb-2 px-1 text-[12.5px] text-dim">안 끝난 체크리스트</h2>
              <ul className="space-y-3">
                {lists.map((l) => (
                  <li key={l.id}>
                    <ChecklistCard
                      list={l}
                      onTick={(item, done) => tick(l.id, item, done)}
                      onDrop={(item) => dropItem(l.id, item)}
                      onClose={() => closeList(l.id)}
                      fold={{ open: opened.has(l.id), onToggle: () => flip(l.id) }}
                    />
                  </li>
                ))}
              </ul>
            </section>
          ) : null}

          {jobs ? (
            live.length > 0 ? (
              <ul className="space-y-1" data-focus="waiting">
                {live.map((j) => (
                  <li key={j.id} data-focus={`job:${j.id}`}>
                    <Row job={j} onOpen={() => setOpenId(j.id)} />
                  </li>
                ))}
              </ul>
            ) : (
              <div className="py-14 text-center">
                <p className="text-[14px] text-foreground/90">맡긴 작업이 없습니다.</p>
                <p className="mx-auto mt-2 max-w-[24rem] text-[12.5px] leading-relaxed text-dim">
                  대화에서 시간이 걸리는 일을 맡기면 여기서 진행을 볼 수 있습니다. 예를 들어 &ldquo;이거 중고나라에
                  올려줘&rdquo;, &ldquo;이 상품 가격 내려가면 알려줘&rdquo;, &ldquo;택배 도착하면 알려줘&rdquo;.
                </p>
                <Link
                  href="/"
                  className="tap mt-4 inline-flex min-h-10 items-center rounded-lg border border-edge px-4 text-[13px] text-foreground outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  대화로 가기
                </Link>
              </div>
            )
          ) : null}

          {ended.length > 0 ? (
            <section className="mt-8 border-t border-edge-soft pt-4">
              <button
                type="button"
                onClick={() => setShowEnded((v) => !v)}
                aria-expanded={showEnded}
                className="tap flex w-full items-center justify-between rounded-lg px-2 text-[13px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                끝난 작업
                <span className="tnum text-[12px] text-faint">{ended.length}</span>
              </button>
              {showEnded ? (
                <ul className="mt-2 space-y-1">
                  {ended.map((j) => (
                    <li key={j.id}>
                      <Row job={j} onOpen={() => setOpenId(j.id)} />
                    </li>
                  ))}
                </ul>
              ) : null}
            </section>
          ) : null}
        </div>
      </main>

      <JobSheet
        job={open}
        proposals={proposals}
        onDecide={decide}
        onOpenChange={(v) => !v && setOpenId(null)}
        onChanged={load}
      />

      <TabBar pending={waiting.length} />
    </div>
  );
}

function Row({ job: j, onOpen }: { job: Job; onOpen: () => void }) {
  const ended = !LIVE.includes(j.state);
  const cover = !j.photosGone ? j.photos[0] : undefined;
  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex w-full items-start gap-3.5 rounded-xl px-2 py-3 text-left transition-colors outline-none hover:bg-glass focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {cover ? (
        <span className="relative mt-0.5 block size-14 shrink-0 overflow-hidden rounded-lg border border-edge-soft bg-well">
          {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
          <img
            src={photoUrl(cover)}
            alt=""
            loading="lazy"
            onError={(e) => (e.currentTarget.style.visibility = "hidden")}
            className={cn("size-full object-cover", ended && "opacity-50 grayscale")}
          />
        </span>
      ) : null}
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className={cn("min-w-0 truncate text-[14.5px]", ended ? "text-dim" : "text-foreground/90")}>
            {j.title}
          </span>
          <span className={cn("ms-auto flex shrink-0 items-center gap-1.5 text-[12px]", stateTone(j))}>
            {j.running ? <span aria-hidden className="anim-breathe size-[6px] rounded-full bg-approve" /> : null}
            {stateLabel(j)}
          </span>
        </span>
        {j.running && j.currentStep ? (
          <span className="mt-1 block truncate text-[12.5px] text-dim">{j.currentStep}</span>
        ) : j.summary ? (
          <span className="mt-1 block truncate text-[12.5px] text-dim">{j.summary}</span>
        ) : null}
        <span className="tnum mt-1 block truncate text-[11.5px] text-faint">
          {j.running
            ? "지금 실행 중"
            : j.state === "active" && j.nextAt && !j.blockedSites?.length
              ? `다음 ${when(j.nextAt)}${j.nextReason ? ` · ${j.nextReason}` : ""}`
              : j.state === "waiting"
                ? "카드에 답하면 이어서 합니다"
                : j.finishedAt
                  ? `${ago(j.finishedAt)} 끝남`
                  : j.lastRunAt
                    ? `${ago(j.lastRunAt)} 실행`
                    : ""}
        </span>
      </span>
    </button>
  );
}
