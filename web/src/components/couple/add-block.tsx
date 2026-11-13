"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Panel } from "@/components/ui/bottom-sheet";
import { ADDABLE, BLOCK_NAME, writeCouple, type Block, type Space } from "@/lib/couple";
import { cn } from "@/lib/utils";

/**
 * A new block on the home, of the kinds this console opens and the space has
 * room for (two bucket lists, two habits, two free blocks). The rest - photos,
 * a diary, the expense book - are added in the app, where their forms are.
 */
export function AddBlock({
  open,
  onOpenChange,
  space,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  space: Space;
  onAdded: () => unknown;
}) {
  const router = useRouter();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function add(type: string) {
    setBusy(type);
    setError(null);
    const sent = await writeCouple<Block>("POST", "/blocks", { type });
    setBusy(null);
    if (!sent.ok) return setError(sent.error);
    onOpenChange(false);
    await onAdded();
    if (sent.data.id) router.push(`/couple/${sent.data.id}`);
  }

  return (
    <Panel open={open} onOpenChange={onOpenChange} title="블록 추가" description="여기서 더할 수 있는 블록이에요. 사진·일기·가계부 블록은 uniple 앱에서 더해요.">
      <ul className="divide-y divide-edge-soft">
        {ADDABLE.map((a) => {
          const have = space.blocks.filter((b) => b.type === a.type).length;
          const full = have >= a.cap;
          return (
            <li key={a.type}>
              <button
                type="button"
                disabled={full || busy !== null}
                onClick={() => void add(a.type)}
                className={cn(
                  "-mx-2 flex w-[calc(100%+1rem)] items-center gap-3 rounded-lg px-2 py-3 text-left transition-colors focus-ring",
                  full ? "opacity-50" : "hover:bg-glass",
                )}
              >
                <span className="min-w-0 flex-1">
                  <span className="block text-body text-foreground">{BLOCK_NAME[a.type]}</span>
                  <span className="block text-aside text-faint">{full ? `${a.cap}개까지 둘 수 있어요` : a.line}</span>
                </span>
                <span className="shrink-0 text-aside text-dim tabular-nums">{busy === a.type ? "더하는 중…" : `${have}/${a.cap}`}</span>
              </button>
            </li>
          );
        })}
      </ul>
      {error ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {error}
        </p>
      ) : null}
    </Panel>
  );
}
