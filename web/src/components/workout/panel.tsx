"use client";

import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";

/** A bottom sheet on a phone; a centred one on a wide screen - the same shape the ledger's sheets use. */
export function Panel({
  open,
  onOpenChange,
  title,
  description,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        // A sheet that throws up the keyboard before anything has been read
        // covers the list it opened to show.
        onOpenAutoFocus={(e) => e.preventDefault()}
        className="inset-x-safe pb-safe max-h-[88dvh] gap-0 border-edge-soft bg-background sm:mx-auto sm:max-w-lg sm:rounded-t-2xl sm:border-x"
      >
        <SheetHeader className="px-4 pt-4 pb-2 sm:px-5">
          <SheetTitle className="pr-8 text-[15px] font-medium">{title}</SheetTitle>
          {description ? (
            <SheetDescription className="text-[12px] text-faint">{description}</SheetDescription>
          ) : null}
        </SheetHeader>
        <div className="scrollbar-hairline min-h-0 overflow-y-auto px-4 pb-4 sm:px-5">{children}</div>
      </SheetContent>
    </Sheet>
  );
}

export const quietButton =
  "tap rounded-lg px-3 text-[13px] text-dim transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50";

/** The one bright control in a sheet is the one that commits - same as the ledger's. */
export const saveButton =
  "tap min-h-11 flex-1 rounded-lg bg-glass-raised px-4 text-[14px] font-medium text-foreground transition-colors outline-none hover:bg-foreground/15 focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50";
