"use client";

import { nestSlot } from "@/components/nest/host";
import { Header, PageTop } from "@/components/shell/header";
import { ScreenMenu } from "@/components/shell/screen-menu";
import { StandingBar } from "@/components/shell/standing-bar";
import { TabBar } from "@/components/shell/tab-bar";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";

/**
 * 둥지: the room the two keep together - decorated, cleaned, its plants and
 * fish looked after, a diary written by both, neighbours' rooms visited, and
 * a voice call while both are in. It is its own service (dungji/); this tab
 * is the header and the space the frame stands over (components/nest/host.tsx,
 * mounted beside every page, so a call keeps going while another tab is
 * looked at).
 *
 * The console's tab bar stays: 둥지's own places (방, 일기, 방명록, 이웃) are a
 * row of words at the top of the frame, as a place's screens are words in the
 * console's header, and the frame ends above the floating bar (pb-tabbar).
 */
export function Nest() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop fade={false}>
        <Header connection={connection} pending={waiting.length} actions={<ScreenMenu pending={waiting.length} />} />
        <StandingBar pending={waiting} />
      </PageTop>

      {/* Clear of the floating tab bar, and of the notch on a phone turned on its side. */}
      <div className="inset-x-safe pb-tabbar flex min-h-0 flex-1 flex-col">
        <div ref={nestSlot} className="min-h-0 flex-1" />
      </div>

      <TabBar pending={waiting.length} />
    </div>
  );
}
