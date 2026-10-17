"use client";

import { useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";
import { BASIS, FINDINGS, GROUPS, PRIORITY, STRENGTHS, type Finding, type Priority } from "./findings";

/**
 * The code review of 2026-10-17, read the way the 상태 tab reads: one
 * sentence first (how many things want doing now), then the same row for
 * everything - a mark for its priority, a name, the size of the fix on the
 * right, one line saying what is wrong; opening a row shows what was seen
 * and what to do.
 *
 * The tally under the headline is also the filter, as the calendar's legend
 * and the ledger's categories are: the place you look to learn what the
 * marks mean is the place you tap to see only those.
 *
 * A temporary tab: the findings are data (findings.ts), not something
 * fetched, and the whole thing is this directory plus one line in the header.
 */
export function Report() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const [open, setOpen] = useState<string | null>(null);
  const [only, setOnly] = useState<Priority | null>(null);

  const count = (p: Priority) => FINDINGS.filter((f) => f.priority === p).length;
  const now = count("now");
  const shown = only ? FINDINGS.filter((f) => f.priority === only) : FINDINGS;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[46rem] px-4 pt-6 pb-10 sm:px-8 sm:pt-10">
          <p className="text-[26px] leading-[1.25] font-medium tracking-[-0.015em] text-foreground sm:text-[30px]">
            {now > 0 ? `지금 고칠 것이 ${now}개 있어요` : "지금 고칠 것은 없어요"}
          </p>
          <p className="mt-2 text-[13.5px] text-dim text-pretty">
            {BASIS.date}에 main 전체를 읽고 돌려 본 결과예요. 보안 권고 하나와 테스트가 비어 있는 곳 둘이 먼저이고,
            나머지는 자랄수록 무거워지는 것들이에요.
          </p>

          {/* The tally is the filter: tap a word to see only those rows, again to see all. */}
          <div className="mt-5 flex flex-wrap gap-x-5 gap-y-2" role="group" aria-label="우선순위로 거르기">
            {(Object.keys(PRIORITY) as Priority[]).map((p) => {
              const active = only === p;
              const dimmed = only !== null && !active;
              return (
                <button
                  key={p}
                  type="button"
                  aria-pressed={active}
                  title={PRIORITY[p].meaning}
                  onClick={() => setOnly(active ? null : p)}
                  className={cn(
                    "tap -my-2 flex items-center gap-2 rounded-md px-1 text-[13.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-0 sm:min-w-0 sm:py-1",
                    dimmed ? "text-faint" : "text-foreground",
                  )}
                >
                  <Mark priority={p} />
                  <span>
                    {PRIORITY[p].label} <span className="tnum">{count(p)}</span>
                  </span>
                </button>
              );
            })}
          </div>
          <p className="mt-2 text-[11.5px] text-faint">
            {only ? PRIORITY[only].meaning : "지금 · 다음 · 나중은 손댈 순서예요. 누르면 그것만 보여요."}
          </p>

          {GROUPS.map((group) => {
            const rows = shown.filter((f) => f.group === group);
            if (rows.length === 0) return null;
            return (
              <Group key={group} name={group} rows={rows}>
                <ul className="divide-y divide-edge-soft">
                  {rows.map((f) => (
                    <Row
                      key={f.id}
                      f={f}
                      open={open === f.id}
                      onToggle={() => setOpen(open === f.id ? null : f.id)}
                    />
                  ))}
                </ul>
              </Group>
            );
          })}

          {only === null ? (
            <>
              <section className="mt-9 sm:mt-11" aria-labelledby="group-strengths">
                <div className="mb-1 flex items-baseline justify-between gap-3">
                  <h2 id="group-strengths" className="text-[12px] text-faint">
                    잘 돼 있는 것
                  </h2>
                  <span className="text-[11.5px] text-faint">손대지 않아도 돼요</span>
                </div>
                <ul className="divide-y divide-edge-soft">
                  {STRENGTHS.map((s) => (
                    <li key={s.name} className="flex items-start gap-3 py-3">
                      <span className="mt-[7px] flex size-[7px] shrink-0 items-center justify-center">
                        <span className="size-[6px] rounded-full bg-online" />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="text-[14.5px] text-foreground/90">{s.name}</span>
                        <span className="mt-0.5 block text-[13px] leading-[1.55] text-faint text-pretty">{s.summary}</span>
                      </span>
                    </li>
                  ))}
                </ul>
              </section>

              <section className="mt-9 sm:mt-11" aria-labelledby="group-basis">
                <h2 id="group-basis" className="mb-1 text-[12px] text-faint">
                  이 보고서
                </h2>
                <ul className="space-y-1.5 py-3 text-[12.5px] leading-[1.6] text-dim">
                  {BASIS.facts.map((f) => (
                    <li key={f}>
                      <Inline text={f} />
                    </li>
                  ))}
                </ul>
                <p className="text-[11.5px] text-faint text-pretty">
                  임시 탭이에요. 다 읽고 나면 <Code>web/src/components/report</Code> 와 헤더의 탭 한 줄을 지우면 돼요.
                </p>
              </section>
            </>
          ) : null}
        </div>
      </main>

      <TabBar pending={waiting.length} />
    </div>
  );
}

/** A group: its name, and on the right how its rows divide by priority. */
function Group({ name, rows, children }: { name: string; rows: Finding[]; children: ReactNode }) {
  const n = (p: Priority) => rows.filter((r) => r.priority === p).length;
  const tally = (Object.keys(PRIORITY) as Priority[])
    .map((p) => (n(p) ? `${PRIORITY[p].label} ${n(p)}` : ""))
    .filter(Boolean)
    .join(" · ");
  const id = `group-${name}`;
  return (
    <section className="mt-9 sm:mt-11" aria-labelledby={id}>
      <div className="mb-1 flex items-baseline justify-between gap-3">
        <h2 id={id} className="text-[12px] text-faint">
          {name}
        </h2>
        <span className="tnum text-[11.5px] text-faint">{tally}</span>
      </div>
      {children}
    </section>
  );
}

function Row({ f, open, onToggle }: { f: Finding; open: boolean; onToggle: () => void }) {
  const pressing = f.priority === "now";
  return (
    <li id={`finding-${f.id}`} className="scroll-mt-24">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex min-h-12 w-full items-start gap-3 py-3 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <Mark priority={f.priority} className="mt-[7px]" />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline justify-between gap-3">
            <span className={cn("text-[14.5px] text-pretty", pressing ? "text-foreground" : "text-foreground/90")}>{f.name}</span>
            <span className="shrink-0 text-[11.5px] text-faint">
              {/* The size of the fix. Its label rides along on a wide screen. */}
              <span className="hidden sm:inline">고치는 품 </span>
              {f.effort}
            </span>
          </span>
          <span className={cn("mt-0.5 block text-[13px] leading-[1.55] text-pretty", pressing ? "text-dim" : "text-faint")}>
            {f.summary}
          </span>
        </span>
        <ChevronDown aria-hidden className={cn("mt-1 size-4 shrink-0 text-faint transition-transform", open && "rotate-180")} />
      </button>

      {open ? (
        <div className="anim-settle mb-4 ms-[19px] space-y-3 border-s border-edge ps-4 text-[12.5px] leading-[1.6]">
          <div>
            <p className="text-faint">본 것</p>
            <ul className="mt-0.5 space-y-1 text-dim">
              {f.evidence.map((e) => (
                <li key={e} className="text-pretty">
                  <Inline text={e} />
                </li>
              ))}
            </ul>
          </div>
          <div>
            <p className="text-faint">조치</p>
            <p className="mt-0.5 text-pretty text-dim">
              <Inline text={f.fix} />
            </p>
          </div>
          <p className="text-faint">
            {PRIORITY[f.priority].label} · {PRIORITY[f.priority].meaning}
          </p>
        </div>
      ) : null}
    </li>
  );
}

const markLabel: Record<Priority, string> = { now: "지금", next: "다음", later: "나중" };

/**
 * The product's own marks, so a priority reads the way a state does on the
 * 상태 tab: the solid signal dot for what wants doing now, the breathing
 * hollow ring for what waits on you next, a faint dash for later.
 */
function Mark({ priority, className }: { priority: Priority; className?: string }) {
  return (
    <span role="img" aria-label={markLabel[priority]} className={cn("flex size-[7px] shrink-0 items-center justify-center", className)}>
      {priority === "now" ? (
        <span className="size-[7px] rounded-full bg-reject" />
      ) : priority === "next" ? (
        <span className="anim-breathe size-[7px] rounded-full border border-dim" />
      ) : (
        <span className="h-px w-[7px] bg-faint" />
      )}
    </span>
  );
}

/** A file or a command, in the one place mono is allowed: the literal thing itself. */
function Code({ children }: { children: ReactNode }) {
  return <code className="rounded-sm bg-well px-1 py-px font-mono text-[11.5px] text-foreground/85">{children}</code>;
}

/** Text with `code` spans. */
function Inline({ text }: { text: string }) {
  const parts = text.split("`");
  return (
    <>
      {parts.map((part, i) =>
        i % 2 === 1 ? <Code key={i}>{part}</Code> : <span key={i}>{part}</span>,
      )}
    </>
  );
}
