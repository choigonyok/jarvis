"use client";

import { useState } from "react";
import { ArrowDown, ArrowUp, Archive } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { BLOCK_NAME, writeCouple, type Block } from "@/lib/couple";

/**
 * The home's blocks, kept: their order, and putting one away. The order is
 * the two's - the partner's app shows the same. A block put away is the app's
 * 보관: the app can bring it back. The calendar stays (this console reads it).
 */
export function EditBlocks({
  open,
  onOpenChange,
  blocks,
  onChanged,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  blocks: Block[];
  onChanged: () => unknown;
}) {
  return (
    <Panel open={open} onOpenChange={onOpenChange} title="블록 편집" description="순서를 바꾸면 상대의 uniple 앱 홈도 같은 순서가 돼요." height="tall">
      {open ? <Editor blocks={blocks} onClose={() => onOpenChange(false)} onChanged={onChanged} /> : null}
    </Panel>
  );
}

function Editor({ blocks, onClose, onChanged }: { blocks: Block[]; onClose: () => void; onChanged: () => unknown }) {
  const [order, setOrder] = useState(() => [...blocks].sort((a, b) => a.order - b.order));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [away, setAway] = useState<Block | null>(null);
  const moved = order.some((b, i) => b.id !== [...blocks].sort((x, y) => x.order - y.order)[i]?.id);

  function move(i: number, by: -1 | 1) {
    setOrder((list) => {
      const j = i + by;
      if (j < 0 || j >= list.length) return list;
      const next = list.slice();
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  }

  async function save() {
    setBusy(true);
    setError(null);
    const sent = await writeCouple("POST", "/order", { ids: order.map((b) => b.id) });
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    await onChanged();
    onClose();
  }

  async function putAway(b: Block) {
    setAway(null);
    setBusy(true);
    setError(null);
    const sent = await writeCouple("DELETE", `/blocks/${b.id}`);
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    setOrder((list) => list.filter((x) => x.id !== b.id));
    await onChanged();
  }

  return (
    <div>
      <ul className="divide-y divide-edge-soft">
        {order.map((b, i) => (
          <li key={b.id} className="flex min-h-14 items-center gap-2 py-1">
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body text-foreground">{b.title}</span>
              <span className="block text-meta text-faint">{BLOCK_NAME[b.type] ?? b.type}</span>
            </span>
            <Button variant="ghost" size="icon-touch" disabled={i === 0 || busy} onClick={() => move(i, -1)} aria-label={`${b.title} 위로`} className="text-dim hover:text-foreground">
              <ArrowUp aria-hidden className="size-4" />
            </Button>
            <Button
              variant="ghost"
              size="icon-touch"
              disabled={i === order.length - 1 || busy}
              onClick={() => move(i, 1)}
              aria-label={`${b.title} 아래로`}
              className="text-dim hover:text-foreground"
            >
              <ArrowDown aria-hidden className="size-4" />
            </Button>
            {b.type !== "calendar" ? (
              <Button variant="discard" size="icon-touch" disabled={busy} onClick={() => setAway(b)} aria-label={`${b.title} 보관`}>
                <Archive aria-hidden className="size-4" />
              </Button>
            ) : (
              <span className="size-11 mouse:size-8" aria-hidden />
            )}
          </li>
        ))}
      </ul>
      {error ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {error}
        </p>
      ) : null}
      <div className="mt-4 flex gap-2">
        <Button variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
          닫기
        </Button>
        <Button variant="raised" size="touch" disabled={!moved || busy} onClick={() => void save()} className="flex-1">
          {busy ? "저장하는 중…" : "순서 저장"}
        </Button>
      </div>
      <AlertDialog open={away !== null} onOpenChange={(o) => !o && setAway(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{away?.title} 블록을 보관할까요?</AlertDialogTitle>
            <AlertDialogDescription>홈에서 사라지고 상대의 uniple 앱에서도 보이지 않아요. 보관한 블록은 uniple 앱에서 다시 꺼낼 수 있어요.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>두기</AlertDialogCancel>
            <AlertDialogAction onClick={() => away && void putAway(away)}>보관</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
