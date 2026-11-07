"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { Ellipsis, LayoutGrid } from "lucide-react";
import { BottomSheetContent, BottomSheetHeader, BottomSheetTitle } from "@/components/ui/bottom-sheet";
import { Sheet, SheetTrigger } from "@/components/ui/sheet";
import { SCREEN_ICON } from "@/components/shell/places";
import { useRole } from "@/components/shell/role";
import { hubsFor, pendingHome, type Tab } from "@/lib/hubs";
import { cn } from "@/lib/utils";

/** A sheet's list of screens, one row each: 더보기's, and the menu below. */
export function ScreenList({
  tabs,
  pending,
  onPick,
}: {
  tabs: Tab[];
  pending: number;
  onPick: (tab: Tab) => void;
}) {
  const pathname = usePathname();
  return (
    <div className="px-2 pb-4">
      {tabs.map((tab) => {
        const Icon = SCREEN_ICON[tab.href] ?? Ellipsis;
        const active = pathname === tab.href;
        const marked = pending > 0 && tab.href === pendingHome;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            prefetch
            onClick={(e) => {
              e.preventDefault();
              onPick(tab);
            }}
            aria-current={active ? "page" : undefined}
            aria-label={marked ? `${tab.label}, 결재 대기 ${pending}건` : undefined}
            className={cn(
              "flex min-h-12 items-center gap-3 rounded-xl px-3 text-body transition-colors focus-ring",
              active ? "bg-glass-raised text-foreground" : "text-dim",
            )}
          >
            <Icon aria-hidden className="size-[18px]" strokeWidth={1.8} />
            {tab.label}
            {marked ? <span aria-hidden className="anim-breathe size-[6px] rounded-full bg-foreground/80" /> : null}
          </Link>
        );
      })}
    </div>
  );
}

/**
 * The way to another screen while the tab bar is put away - a running workout
 * puts it away, so the set being lifted has the bottom of the screen and a
 * tap meant for 완료 cannot land on a tab. Two taps instead of one, which is
 * the point. A phone's only: a wide screen has the links in the header.
 */
export function ScreenMenu({ pending }: { pending: number }) {
  const pathname = usePathname();
  const router = useRouter();
  const role = useRole();
  const [open, setOpen] = useState(false);
  const tabs = hubsFor(role).flatMap((hub) => hub.screens);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger
        aria-label={pending > 0 ? `다른 화면, 결재 대기 ${pending}건` : "다른 화면"}
        className="relative flex size-11 items-center justify-center rounded-lg text-faint transition-colors hover:text-dim focus-ring sm:hidden"
      >
        <LayoutGrid aria-hidden className="size-[18px]" strokeWidth={1.8} />
        {pending > 0 ? (
          <span aria-hidden className="anim-breathe absolute top-2.5 right-2 size-[6px] rounded-full bg-foreground/80" />
        ) : null}
      </SheetTrigger>
      <BottomSheetContent width="full" keepFocus={false} aria-describedby={undefined} className="gap-4">
        <BottomSheetHeader className="pb-1 sm:px-4">
          <BottomSheetTitle className="pr-0 text-body">다른 화면</BottomSheetTitle>
        </BottomSheetHeader>
        <ScreenList
          tabs={tabs}
          pending={pending}
          onPick={(tab) => {
            setOpen(false);
            if (tab.href !== pathname) router.push(tab.href);
          }}
        />
      </BottomSheetContent>
    </Sheet>
  );
}
