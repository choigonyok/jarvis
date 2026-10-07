import type { CalendarEvent } from "@/lib/thread";
import { cn } from "@/lib/utils";

export type Owner = NonNullable<CalendarEvent["owner"]>;

/** Legend order: mine, ours, theirs - "함께" sits between the two it joins. */
export const OWNERS: Owner[] = ["me", "shared", "partner"];

export const OWNER_LABEL: Record<Owner, string> = {
  me: "나",
  shared: "함께",
  partner: "상대",
};

/** An entry with no owner predates the shared calendar; it is the operator's. */
export const ownerOf = (e: Pick<CalendarEvent, "owner">): Owner => e.owner ?? "me";

const fill: Record<Owner, string> = {
  me: "bg-mine",
  shared: "owner-shared",
  partner: "bg-partner",
};

const fillVertical: Record<Owner, string> = {
  me: "bg-mine",
  shared: "owner-shared-v",
  partner: "bg-partner",
};

export const ownerText: Record<Owner, string> = {
  me: "text-mine",
  shared: "text-foreground/80",
  partner: "text-partner",
};

/** The dot: under a date in the grid, and in the legend. */
export function OwnerDot({ owner, className }: { owner: Owner; className?: string }) {
  return (
    <span aria-hidden className={cn("size-[7px] shrink-0 rounded-full", fill[owner], className)} />
  );
}

/** The edge bar down the left of a row in the day's detail. */
export function OwnerBar({ owner, className }: { owner: Owner; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn("w-[3px] shrink-0 self-stretch rounded-full", fillVertical[owner], className)}
    />
  );
}
