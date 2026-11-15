"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { DetailSheet } from "@/components/ui/detail-sheet";
import { Pager } from "@/components/ui/pager";
import { usePaged } from "@/lib/paging";
import { detail, outcome, outcomeTone, type Reminder, waiting } from "@/lib/reminders";
import { HttpError, useResource } from "@/lib/use-resource";
import { cn } from "@/lib/utils";

/**
 * The reminders, read beside the jobs: the waiting ones (one is set in
 * conversation - "8시에 알려줘", "집에 가면 알려줘" - and changed or called
 * off there too), and the last month's. A reminder changes rarely: it is
 * read on opening, coming back, and once a minute.
 */
export function useReminders() {
  const { data } = useResource(
    "reminders",
    async (signal) => {
      const res = await fetch("/api/mind/reminders?days=30", { cache: "no-store", signal });
      if (!res.ok) throw new HttpError(res.status);
      const body = (await res.json()) as { reminders?: Reminder[] };
      return body.reminders ?? [];
    },
    { interval: 60_000 },
  );
  // A read that fails keeps the list as it was, and says nothing: the list
  // is a convenience here, the jobs are not.
  return data ?? null;
}

/** What is waiting to be said, soonest first. */
export function WaitingReminders({ reminders }: { reminders: Reminder[] }) {
  const list = useMemo(() => reminders.filter(waiting), [reminders]);
  const paged = usePaged(list, { focusKey: (r) => `reminder:${r.id}` });
  if (list.length === 0) return null;
  return (
    <section className="mt-8 scroll-mt-4" aria-label="맡아 둔 리마인드" id={paged.anchor} data-focus="reminders">
      <h2 className="mb-1 px-1 text-aside text-dim">맡아 둔 리마인드</h2>
      <ul className="space-y-1">
        {paged.items.map((r) => (
          <li key={r.id} data-focus={`reminder:${r.id}`}>
            <ReminderRow reminder={r} />
          </li>
        ))}
      </ul>
      <Pager {...paged} className="px-2" />
    </section>
  );
}

/** The last month's, folded like the jobs that ended. */
export function PastReminders({ reminders }: { reminders: Reminder[] }) {
  const list = useMemo(() => reminders.filter((r) => !waiting(r)), [reminders]);
  const paged = usePaged(list);
  const [open, setOpen] = useState(false);
  if (list.length === 0) return null;
  return (
    <section className="mt-8 border-t border-edge-soft pt-4">
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
        aria-expanded={open}
        className="tap flex w-full items-center justify-between rounded-lg px-2 text-ui text-dim hover:text-foreground focus-ring"
      >
        지난 리마인드
        <span className="tnum text-aside text-faint">{list.length}</span>
      </button>
      <DetailSheet title="지난 리마인드" open={open} onOpenChange={setOpen}>
        <div id={paged.anchor} className="scroll-mt-4">
          <ul className="space-y-1">
            {paged.items.map((r) => (
              <li key={r.id}>
                <ReminderRow reminder={r} />
              </li>
            ))}
          </ul>
          <Pager {...paged} className="px-2" />
          <p className="px-2 pt-2 text-meta leading-relaxed text-faint">지난 30일 것만 보여요.</p>
        </div>
      </DetailSheet>
    </section>
  );
}

/**
 * One reminder, in the job row's grain: what is said, what came of it on the
 * right, and the moment - or when it was said and why not - beneath. Two
 * lines of it are the row; the row opens the whole of it, and of one still
 * waiting, where it is changed or called off - the conversation.
 */
function ReminderRow({ reminder: r }: { reminder: Reminder }) {
  const live = waiting(r);
  const result = outcome(r);
  const [open, setOpen] = useState(false);
  return (
    <div className="flex items-start gap-1">
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
        aria-expanded={open}
        className="min-w-0 flex-1 rounded-lg py-3 pl-2 text-left transition-colors hover:bg-glass focus-ring"
      >
        <span className="flex items-baseline gap-2">
          <span className={cn("line-clamp-2 min-w-0 text-body break-words", live ? "text-ink" : "text-dim")}>
            {r.text}
          </span>
          {result ? <span className={cn("ms-auto shrink-0 text-aside", outcomeTone(r))}>{result}</span> : null}
        </span>
        {live && r.body ? (
          <span className="mt-1 line-clamp-2 block text-aside whitespace-pre-line break-words text-dim">{r.body}</span>
        ) : null}
        <span className={cn("tnum mt-1 block text-meta break-words", live ? "text-dim" : "text-faint")}>{detail(r)}</span>
      </button>
      <CopyButton text={[r.text, r.body].filter(Boolean).join("\n")} label={`${r.text} 복사`} className="mt-1.5" />
      <DetailSheet title="리마인드" open={open} onOpenChange={setOpen}>
        <p className="selectable text-body leading-relaxed whitespace-pre-line break-words text-ink">{r.text}</p>
        {r.body ? (
          <p className="selectable mt-2 text-ui leading-relaxed whitespace-pre-line break-words text-dim">{r.body}</p>
        ) : null}
        <p className="tnum mt-3 text-aside break-words text-dim">
          {detail(r)}
          {result ? (
            <>
              {" · "}
              <span className={outcomeTone(r)}>{result}</span>
            </>
          ) : null}
        </p>
        {live ? (
          <div className="mt-6">
            <p className="text-aside leading-relaxed text-faint">바꾸거나 취소하려면 대화에서 말해 주세요.</p>
            <Button variant="soft" size="touch-sm" asChild className="mt-2">
              <Link href="/">대화로 가기</Link>
            </Button>
          </div>
        ) : null}
      </DetailSheet>
    </div>
  );
}
