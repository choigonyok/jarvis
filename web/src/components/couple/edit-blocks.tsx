"use client";

import { useState } from "react";
import { ArrowDown, ArrowUp, Archive, ArchiveRestore } from "lucide-react";
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
import { useArchived } from "@/components/couple/use-space";
import { BLOCK_NAME, writeCouple, type Block } from "@/lib/couple";
import { dayOf, monthDay } from "@/lib/when";

/**
 * The home's blocks, kept: their order, putting one away, and bringing one
 * back. The order is the two's - the partner's app shows the same. A block put
 * away is the app's 보관, its items kept; brought back it goes last. The
 * calendar stays (this console reads it).
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
  const archived = useArchived(true);
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
    await Promise.all([onChanged(), archived.reload()]);
  }

  async function bringBack(b: Block) {
    setBusy(true);
    setError(null);
    const sent = await writeCouple("POST", `/blocks/${b.id}/restore`);
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    setOrder((list) => [...list, { ...b, order: Math.max(-1, ...list.map((x) => x.order)) + 1 }]);
    await Promise.all([onChanged(), archived.reload()]);
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
      {archived.data?.length ? (
        <section className="mt-6" aria-labelledby="archived-blocks">
          <h3 id="archived-blocks" className="text-ui text-dim">
            보관함 <span className="tabular-nums">{archived.data.length}</span>
          </h3>
          <ul className="mt-1 divide-y divide-edge-soft">
            {archived.data.map((b) => (
              <li key={b.id} className="flex min-h-14 items-center gap-2 py-1">
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-body text-dim">{b.title}</span>
                  <span className="block text-meta text-faint">
                    {BLOCK_NAME[b.type] ?? b.type}
                    {b.archived ? ` · ${monthDay(dayOf(new Date(b.archived)))} 보관` : ""}
                  </span>
                </span>
                <Button variant="soft" size="touch-sm" disabled={busy} onClick={() => void bringBack(b)} className="shrink-0 gap-1.5">
                  <ArchiveRestore aria-hidden className="size-4" />
                  꺼내기
                </Button>
              </li>
            ))}
          </ul>
        </section>
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
            <AlertDialogDescription>홈에서 사라지고 상대의 uniple 앱에서도 보이지 않아요. 안의 내용은 그대로 남고, 이 화면의 보관함에서 다시 꺼낼 수 있어요.</AlertDialogDescription>
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
