"use client";

import { useState } from "react";
import { Plus, X } from "lucide-react";
import { BlockTitle } from "@/components/couple/screen";
import { useUndo } from "@/components/shell/undo";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { writeCouple, type Block, type Free, type Space } from "@/lib/couple";
import { ago } from "@/lib/when";
import { cn } from "@/lib/utils";

/** A free block: short notes either of us leaves, oldest first, each with who left it. */
export function FreePage({ block, space, onChanged }: { block: Block; space: Space; onChanged: () => Promise<void> }) {
  const [items, setItems] = useState<Free[]>(block.free ?? []);
  const [base, setBase] = useState(block.free);
  if (base !== block.free) {
    setBase(block.free);
    setItems(block.free ?? []);
  }
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { offer } = useUndo();

  async function add(e: React.FormEvent) {
    e.preventDefault();
    const content = draft.trim();
    if (!content || busy) return;
    setBusy(true);
    setError(null);
    const sent = await writeCouple<Free>("POST", `/blocks/${block.id}/free`, { content });
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    setDraft("");
    setItems((list) => [...list, sent.data]);
    void onChanged();
  }

  function remove(f: Free) {
    setItems((list) => list.filter((x) => x.id !== f.id));
    offer(
      "기록을 지웠어요",
      () => setItems((list) => [...list, f].sort((a, b) => a.order - b.order)),
      () =>
        void writeCouple("DELETE", `/blocks/${block.id}/free/${f.id}`).then((sent) => {
          if (!sent.ok) {
            setItems((list) => [...list, f].sort((a, b) => a.order - b.order));
            setError(sent.error);
          } else void onChanged();
        }),
    );
  }

  return (
    <div>
      <BlockTitle title={block.title} />
      <p className="mt-2 text-ui text-dim">
        <span className="tabular-nums">{items.length}</span>개
      </p>
      <form onSubmit={(e) => void add(e)} className="mt-4 flex gap-2">
        <Input surface="glass" value={draft} maxLength={500} onChange={(e) => setDraft(e.target.value)} placeholder="우리 둘만의 기록" aria-label="새 기록" className="flex-1" />
        <Button type="submit" variant="raised" size="icon-touch" disabled={!draft.trim() || busy} aria-label="기록 추가">
          <Plus aria-hidden className="size-5" />
        </Button>
      </form>
      {error ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {error}
        </p>
      ) : null}
      {items.length === 0 ? (
        <p className="mt-5 text-ui text-faint">첫 기록을 기다리고 있어요.</p>
      ) : (
        <ul className="mt-3 divide-y divide-edge-soft">
          {items.map((f) => (
            <li key={f.id} className="flex items-start gap-3 py-3">
              <span aria-hidden className={cn("mt-[8px] size-[7px] shrink-0 rounded-full", f.by === "me" ? "bg-mine" : "bg-partner")} />
              <div className="min-w-0 flex-1">
                <p className="text-body break-keep whitespace-pre-line text-foreground">{f.content}</p>
                <p className="mt-0.5 text-meta text-faint">
                  {f.by === "me" ? space.me.name || "나" : space.partner?.name || "상대"} · {ago(f.createdAt)}
                </p>
              </div>
              <Button variant="discard" size="icon-touch" onClick={() => remove(f)} aria-label="기록 지우기" className="-mr-2 shrink-0">
                <X aria-hidden className="size-4" />
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
