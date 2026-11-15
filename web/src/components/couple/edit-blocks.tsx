"use client";

import { useEffect, useState } from "react";
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
import { ADDABLE, BLOCK_NAME, writeCouple, type Block } from "@/lib/couple";
import { cn } from "@/lib/utils";
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
  // An order changed and not saved: a pull down, Back or the ✕ asks before
  // it goes (ui/sheet.tsx). It had gone without a word.
  const [moved, setMoved] = useState(false);
  return (
    <Panel
      open={open}
      onOpenChange={onOpenChange}
      guard={moved}
      title="블록 편집"
      description="순서를 바꾸면 상대의 uniple 앱 홈도 같은 순서가 돼요."
      height="tall"
    >
      {open ? <Editor blocks={blocks} onClose={() => onOpenChange(false)} onChanged={onChanged} onMoved={setMoved} /> : null}
    </Panel>
  );
}

function Editor({
  blocks,
  onClose,
  onChanged,
  onMoved,
}: {
  blocks: Block[];
  onClose: () => void;
  onChanged: () => unknown;
  onMoved: (moved: boolean) => void;
}) {
  const [order, setOrder] = useState(() => [...blocks].sort((a, b) => a.order - b.order));
  const [busy, setBusy] = useState(false);
  // What did not go, said beside what was pressed: under a block's row for
  // its 보관 or 꺼내기, over the buttons for the order. One line between the
  // lists had been a screen away from the 꺼내기 pressed.
  const [error, setError] = useState<{ on: string; text: string } | null>(null);
  const [away, setAway] = useState<Block | null>(null);
  const archived = useArchived(true);
  // Moved, of the blocks both lists hold: one just put away or brought back
  // is not a move while the home is read again.
  const here = new Set(order.map((b) => b.id));
  const was = [...blocks].sort((x, y) => x.order - y.order).filter((b) => here.has(b.id));
  const known = new Set(was.map((b) => b.id));
  const moved = order.filter((b) => known.has(b.id)).some((b, i) => b.id !== was[i].id);
  useEffect(() => {
    onMoved(moved);
    return () => onMoved(false);
  }, [moved, onMoved]);

  // A kind the home holds as many of as it may: brought back only once one
  // is put away, as 블록 추가 says before it is pressed.
  function full(b: Block): number | null {
    const cap = ADDABLE.find((a) => a.type === b.type)?.cap;
    return cap !== undefined && order.filter((x) => x.type === b.type).length >= cap ? cap : null;
  }

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
    if (!sent.ok) return setError({ on: "order", text: sent.error });
    await onChanged();
    onClose();
  }

  async function putAway(b: Block) {
    setAway(null);
    setBusy(true);
    setError(null);
    const sent = await writeCouple("DELETE", `/blocks/${b.id}`);
    setBusy(false);
    if (!sent.ok) return setError({ on: b.id, text: sent.error });
    setOrder((list) => list.filter((x) => x.id !== b.id));
    await Promise.all([onChanged(), archived.reload()]);
  }

  async function bringBack(b: Block) {
    setBusy(true);
    setError(null);
    const sent = await writeCouple("POST", `/blocks/${b.id}/restore`);
    setBusy(false);
    if (!sent.ok) return setError({ on: b.id, text: sent.error });
    setOrder((list) => [...list, { ...b, order: Math.max(-1, ...list.map((x) => x.order)) + 1 }]);
    await Promise.all([onChanged(), archived.reload()]);
  }

  return (
    <div>
      <ul className="divide-y divide-edge-soft">
        {order.map((b, i) => (
          <li key={b.id} className="py-1">
            <div className="flex min-h-12 items-center gap-2">
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
            </div>
            {error?.on === b.id ? (
              <p role="alert" className="pb-1 text-aside text-reject">
                {error.text}
              </p>
            ) : null}
          </li>
        ))}
      </ul>
      {archived.data?.length ? (
        <section className="mt-6" aria-labelledby="archived-blocks">
          <h3 id="archived-blocks" className="text-ui text-dim">
            보관함 <span className="tabular-nums">{archived.data.length}</span>
          </h3>
          <ul className="mt-1 divide-y divide-edge-soft">
            {archived.data.map((b) => {
              const cap = full(b);
              return (
                <li key={b.id} className="py-1">
                  <div className="flex min-h-12 items-center gap-2">
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-body text-dim">{b.title}</span>
                      <span className="block text-meta text-faint">
                        {BLOCK_NAME[b.type] ?? b.type}
                        {b.archived ? ` · ${monthDay(dayOf(new Date(b.archived)))} 보관` : ""}
                      </span>
                    </span>
                    <Button variant="soft" size="touch-sm" disabled={busy || cap !== null} onClick={() => void bringBack(b)} className="shrink-0 gap-1.5">
                      <ArchiveRestore aria-hidden className="size-4" />
                      꺼내기
                    </Button>
                  </div>
                  {cap !== null ? (
                    <p className="pb-1 text-meta text-dim">
                      {cap === 1
                        ? "하나만 둘 수 있어요. 홈에 있는 것을 보관하면 꺼낼 수 있어요."
                        : `${cap}개까지 둘 수 있어요. 하나를 보관하면 꺼낼 수 있어요.`}
                    </p>
                  ) : null}
                  {error?.on === b.id ? (
                    <p role="alert" className="pb-1 text-aside text-reject">
                      {error.text}
                    </p>
                  ) : null}
                </li>
              );
            })}
          </ul>
        </section>
      ) : null}
      {error?.on === "order" ? (
        <p role="alert" className="mt-4 text-aside text-reject">
          {error.text}
        </p>
      ) : null}
      <div className={cn("flex gap-2", error?.on === "order" ? "mt-2" : "mt-4")}>
        {/* What it does when the order was changed, said on it: it had sat
            beside 순서 저장 and dropped the order without a word. */}
        <Button variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
          {moved ? "바꾼 순서 버리기" : "닫기"}
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
