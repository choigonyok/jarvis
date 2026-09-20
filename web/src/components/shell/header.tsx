"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type { Connection } from "@/lib/thread";
import { cn } from "@/lib/utils";

const connectionCopy: Record<Connection, { label: string; dot: string }> = {
  connecting: { label: "연결 중", dot: "bg-faint" },
  open: { label: "연결됨", dot: "bg-online" },
  closed: { label: "연결 끊김", dot: "bg-faint" },
};

const tabs = [
  { href: "/", label: "대화" },
  { href: "/calendar", label: "캘린더" },
];

/**
 * One header for both surfaces. The two places share a pending count on
 * purpose: a card raised in the chat and one sitting in the calendar are the
 * same queue, and the operator should never have to check two of them.
 */
export function Header({
  connection,
  pending,
}: {
  connection: Connection;
  pending: number;
}) {
  const pathname = usePathname();
  const status = connectionCopy[connection];

  return (
    <header className="shrink-0 border-b border-edge-soft bg-background/70 backdrop-blur-xl">
      <div className="mx-auto flex h-14 w-full max-w-[46rem] items-center justify-between px-5 sm:px-8">
        <div className="flex items-baseline gap-4">
          <h1 className="text-[15px] font-semibold tracking-tight text-foreground">
            Jarvis
          </h1>
          <nav className="flex items-baseline gap-3" aria-label="화면">
            {tabs.map((tab) => {
              const active = pathname === tab.href;
              return (
                <Link
                  key={tab.href}
                  href={tab.href}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "rounded-sm text-[13px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                    active ? "text-foreground" : "text-faint hover:text-dim",
                  )}
                >
                  {tab.label}
                </Link>
              );
            })}
          </nav>
        </div>

        <div className="flex items-center gap-3">
          <span className="flex items-center gap-1.5 text-[11.5px] text-faint">
            <span
              aria-hidden
              className={cn(
                "size-[5px] rounded-full",
                status.dot,
                connection === "connecting" && "anim-breathe",
              )}
            />
            {status.label}
          </span>
          <p className="text-[11.5px] text-faint" aria-live="polite">
            {pending > 0 ? `결재 대기 ${pending}건` : "결재 대기 없음"}
          </p>
        </div>
      </div>
    </header>
  );
}
