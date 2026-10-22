"use client";

import { editorSlot } from "@/components/edit/photocraft";
import { Header, ScreenMenu, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";

/**
 * 편집: PhotoCraft, an image editor that runs in the browser - layers, masks,
 * type, PSD files. A picture is opened from this device, and Save or Export
 * downloads the result; nothing is kept on the server.
 *
 * The tab is only the frame: the header, and the space under it that the
 * editor stands over (components/edit/photocraft.tsx - mounted beside every
 * page, so a picture half edited survives a look at another tab).
 *
 * On a phone the floating tab bar is put away, as it is while a set is being
 * lifted: the editor's own controls run along the bottom (the layer buttons,
 * the status line), and a tap meant for one of them must not land on a tab.
 * The other screens are a menu in the header away.
 */
export function Edit() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header
        connection={connection}
        pending={waiting.length}
        actions={<ScreenMenu pending={waiting.length} />}
      />
      <StandingBar pending={waiting} href="/record" />

      {/* Clear of the home indicator, and of the notch on a phone turned on its side. */}
      <div className="inset-x-safe pb-safe flex min-h-0 flex-1 flex-col">
        <div ref={editorSlot} className="min-h-0 flex-1" />
      </div>

      <TabBar pending={waiting.length} hidden />
    </div>
  );
}
