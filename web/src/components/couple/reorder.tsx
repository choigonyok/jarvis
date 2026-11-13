"use client";

import { useState } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";

/**
 * A list's order, changed a step at a time and saved once: the same up and
 * down the home's 블록 편집 uses. The order is the two's - the partner's app
 * shows it the same way.
 */
export function ReorderSheet<T extends { id: string }>({
  open,
  onOpenChange,
  title,
  items,
  label,
  onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  items: T[];
  label: (item: T) => string;
  /** Resolves to why the order did not go, or null. */
  onSave: (ids: string[]) => Promise<string | null>;
}) {
  return (
    <Panel open={open} onOpenChange={onOpenChange} title={title} description="순서를 바꾸면 상대의 uniple 앱에도 같은 순서로 보여요." height="tall">
      {open ? <Order items={items} label={label} onSave={onSave} onClose={() => onOpenChange(false)} /> : null}
    </Panel>
  );
}

function Order<T extends { id: string }>({
  items,
  label,
  onSave,
  onClose,
}: {
  items: T[];
  label: (item: T) => string;
  onSave: (ids: string[]) => Promise<string | null>;
  onClose: () => void;
}) {
  const [order, setOrder] = useState(items);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const moved = order.some((it, i) => it.id !== items[i]?.id);

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
    const failed = await onSave(order.map((it) => it.id));
    setBusy(false);
    if (failed) return setError(failed);
    onClose();
  }

  return (
    <div>
      <ol className="divide-y divide-edge-soft">
        {order.map((it, i) => (
          <li key={it.id} className="flex min-h-12 items-center gap-2 py-1">
            <span className="w-6 shrink-0 text-right text-meta text-faint tabular-nums">{i + 1}</span>
            <span className="min-w-0 flex-1 truncate text-body text-foreground">{label(it)}</span>
            <Button
              variant="ghost"
              size="icon-touch"
              disabled={i === 0 || busy}
              onClick={() => move(i, -1)}
              aria-label={`${label(it)} 위로`}
              className="text-dim hover:text-foreground"
            >
              <ArrowUp aria-hidden className="size-4" />
            </Button>
            <Button
              variant="ghost"
              size="icon-touch"
              disabled={i === order.length - 1 || busy}
              onClick={() => move(i, 1)}
              aria-label={`${label(it)} 아래로`}
              className="text-dim hover:text-foreground"
            >
              <ArrowDown aria-hidden className="size-4" />
            </Button>
          </li>
        ))}
      </ol>
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
    </div>
  );
}

/**
 * A new order for some of a list's items, the rest kept where they were: the
 * reordered ones fill the places the same ones held.
 */
export function placeInto(all: string[], some: string[]): string[] {
  const moving = new Set(some);
  const next = some.slice();
  return all.map((id) => (moving.has(id) ? (next.shift() ?? id) : id));
}
