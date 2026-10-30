"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Pager } from "@/components/ui/pager";
import { usePaged } from "@/lib/paging";
import { detail, outcome, outcomeTone, type Reminder, waiting } from "@/lib/reminders";
import { onResume } from "@/lib/resume";
import { cn } from "@/lib/utils";

/**
 * The reminders, read beside the jobs: the waiting ones (one is set in
 * conversation - "8시에 알려줘", "집에 가면 알려줘" - and changed or called
 * off there too), and the last month's. A reminder changes rarely: it is
 * read on opening, coming back, and once a minute.
 */
export function useReminders() {
  const [reminders, setReminders] = useState<Reminder[] | null>(null);
  const load = useCallback(async () => {
    try {
      const res = await fetch("/api/mind/reminders?days=30", { cache: "no-store" });
      if (!res.ok) return;
      const body = (await res.json()) as { reminders?: Reminder[] };
      setReminders(body.reminders ?? []);
    } catch {
      // Kept as it was: the list is a convenience here, the jobs are not.
    }
  }, []);
  useEffect(() => {
    // load sets state only after its fetch resolves; the rule cannot see that.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load();
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, 60_000);
    const off = onResume(load);
    return () => {
      window.clearInterval(id);
      off();
    };
  }, [load]);
  return reminders;
}

/** What is waiting to be said, soonest first. */
export function WaitingReminders({ reminders }: { reminders: Reminder[] }) {
  const list = useMemo(() => reminders.filter(waiting), [reminders]);
  const paged = usePaged(list, { focusKey: (r) => `reminder:${r.id}` });
  if (list.length === 0) return null;
  return (
    <section className="mt-8 scroll-mt-4" aria-label="맡아 둔 리마인드" id={paged.anchor} data-focus="reminders">
      <h2 className="mb-1 px-1 text-[12.5px] text-dim">맡아 둔 리마인드</h2>
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
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="tap flex w-full items-center justify-between rounded-lg px-2 text-[13px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        지난 리마인드
        <span className="tnum text-[12px] text-faint">{list.length}</span>
      </button>
      {open ? (
        <div id={paged.anchor} className="scroll-mt-4">
          <ul className="mt-2 space-y-1">
            {paged.items.map((r) => (
              <li key={r.id}>
                <ReminderRow reminder={r} />
              </li>
            ))}
          </ul>
          <Pager {...paged} className="px-2" />
          <p className="px-2 pt-2 text-[11.5px] leading-relaxed text-faint">지난 30일 것만 보입니다.</p>
        </div>
      ) : null}
    </section>
  );
}

/**
 * One reminder, in the job row's grain: what is said, what came of it on the
 * right, and the moment - or when it was said and why not - beneath.
 */
function ReminderRow({ reminder: r }: { reminder: Reminder }) {
  const live = waiting(r);
  const result = outcome(r);
  return (
    <div className="px-2 py-3">
      <div className="flex items-baseline gap-2">
        <p className={cn("line-clamp-2 min-w-0 text-[14.5px] break-words", live ? "text-foreground/90" : "text-dim")}>
          {r.text}
        </p>
        {result ? <span className={cn("ms-auto shrink-0 text-[12px]", outcomeTone(r))}>{result}</span> : null}
      </div>
      {live && r.body ? (
        <p className="mt-1 line-clamp-2 text-[12.5px] whitespace-pre-line break-words text-dim">{r.body}</p>
      ) : null}
      <p className={cn("tnum mt-1 text-[11.5px] break-words", live ? "text-dim" : "text-faint")}>{detail(r)}</p>
    </div>
  );
}
