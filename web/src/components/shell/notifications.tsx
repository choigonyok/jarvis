"use client";

import { useEffect, useRef, useState } from "react";
import { Bell } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * The notification inbox. Separate from the approval ledger (기록): that is
 * what was decided, this will be what happened - a job's news, a budget
 * crossing, an allocation drifting.
 *
 * A placeholder for now: the items are samples, so the surface can be judged
 * on the phone before the service behind it exists.
 */
type Note = { id: string; title: string; body: string; at: string; unread: boolean };

const SAMPLES: Note[] = [
  { id: "s1", title: "작업 · 에어팟 프로 2 판매", body: "채팅이 2건 새로 왔습니다.", at: "방금", unread: true },
  { id: "s2", title: "가계부", body: "이번 달 식비가 예산의 80%를 넘었습니다.", at: "1시간 전", unread: true },
  { id: "s3", title: "자산", body: "미국 주식 비중이 목표보다 5%p 높습니다.", at: "어제", unread: false },
];

export function Notifications() {
  const [open, setOpen] = useState(false);
  const [notes, setNotes] = useState(SAMPLES);
  const wrap = useRef<HTMLDivElement>(null);
  const unread = notes.filter((n) => n.unread).length;

  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open]);

  return (
    <div ref={wrap} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-label={unread ? `알림 ${unread}개 안 읽음` : "알림"}
        className="relative flex size-11 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50 sm:size-8"
      >
        <Bell aria-hidden className="size-[18px] sm:size-4" />
        {unread ? (
          <span aria-hidden className="absolute top-2.5 right-2.5 size-[7px] rounded-full bg-reject ring-2 ring-background sm:top-1 sm:right-1" />
        ) : null}
      </button>

      {open ? (
        <div className="absolute top-full right-0 z-50 mt-1 w-[min(20rem,calc(100vw-2rem))] overflow-hidden rounded-xl border border-edge bg-background/95 shadow-lg backdrop-blur-md">
          <div className="flex items-baseline justify-between border-b border-edge-soft px-3.5 py-2.5">
            <p className="text-[13px] text-foreground">알림</p>
            {unread ? (
              <button
                type="button"
                onClick={() => setNotes((ns) => ns.map((n) => ({ ...n, unread: false })))}
                className="rounded text-[11.5px] text-faint outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                모두 읽음
              </button>
            ) : null}
          </div>
          <ul>
            {notes.map((n) => (
              <li key={n.id}>
                <button
                  type="button"
                  onClick={() => setNotes((ns) => ns.map((x) => (x.id === n.id ? { ...x, unread: false } : x)))}
                  className="flex w-full gap-2.5 px-3.5 py-2.5 text-left outline-none hover:bg-glass focus-visible:bg-glass"
                >
                  <span aria-hidden className={cn("mt-1.5 size-[6px] shrink-0 rounded-full", n.unread ? "bg-reject" : "bg-transparent")} />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-baseline justify-between gap-2">
                      <span className={cn("truncate text-[12.5px]", n.unread ? "text-foreground" : "text-dim")}>{n.title}</span>
                      <span className="shrink-0 text-[11px] text-faint">{n.at}</span>
                    </span>
                    <span className="mt-0.5 block text-[12.5px] leading-relaxed text-dim">{n.body}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
          <p className="border-t border-edge-soft px-3.5 py-2 text-[11px] text-faint">샘플 알림입니다. 실제 알림은 준비 중입니다.</p>
        </div>
      ) : null}
    </div>
  );
}
