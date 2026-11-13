"use client";

import { useState } from "react";
import { ArrowUpDown, Plus, X } from "lucide-react";
import { ReorderSheet } from "@/components/couple/reorder";
import { BlockTitle } from "@/components/couple/screen";
import { useUndo } from "@/components/shell/undo";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Toggle } from "@/components/ui/toggle";
import { useKept } from "@/lib/keep";
import { writeCouple, type Block, type Free, type Space } from "@/lib/couple";
import { ago } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * A free block: short notes either of us leaves, in the order the two keep
 * them, each with who left it. A note's words open to be rewritten on a tap.
 */
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
  // Who left each and when: shown or not, as this phone last chose.
  const [showWho, setShowWho] = useKept(`couple:free:${block.id}:who`, true);
  const [showWhen, setShowWhen] = useKept(`couple:free:${block.id}:when`, true);
  const [ordering, setOrdering] = useState(false);
  const [editing, setEditing] = useState<{ id: string; text: string } | null>(null);

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

  async function rewrite(f: Free, content: string) {
    setEditing(null);
    const text = content.trim();
    if (!text || text === f.content) return;
    setError(null);
    setItems((list) => list.map((x) => (x.id === f.id ? { ...x, content: text } : x)));
    const sent = await writeCouple<Free>("PATCH", `/blocks/${block.id}/free/${f.id}`, { content: text });
    if (!sent.ok) {
      setItems((list) => list.map((x) => (x.id === f.id ? { ...x, content: f.content } : x)));
      setError(sent.error);
      return;
    }
    void onChanged();
  }

  async function reorder(ids: string[]): Promise<string | null> {
    const sent = await writeCouple("POST", `/blocks/${block.id}/free/order`, { ids });
    if (!sent.ok) return sent.error;
    const at = new Map(ids.map((id, i) => [id, i]));
    setItems((list) => list.map((f) => ({ ...f, order: at.get(f.id) ?? f.order })).sort((a, b) => a.order - b.order));
    void onChanged();
    return null;
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
      <BlockTitle title={block.title} block={block} onRenamed={onChanged} />
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <p className="mr-auto text-ui text-dim">
          <span className="tabular-nums">{items.length}</span>개
        </p>
        {items.length > 1 ? (
          <Button variant="ghost" size="icon-touch" onClick={() => setOrdering(true)} aria-label="기록 순서 바꾸기" className="text-dim hover:text-foreground">
            <ArrowUpDown aria-hidden className="size-4" />
          </Button>
        ) : null}
        <Toggle pressed={showWho} onPressedChange={setShowWho} variant="pill" size="chip" aria-label="쓴 사람 보이기">
          쓴 사람
        </Toggle>
        <Toggle pressed={showWhen} onPressedChange={setShowWhen} variant="pill" size="chip" aria-label="시간 보이기">
          시간
        </Toggle>
      </div>
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
            <li key={f.id} data-focus={`free:${f.id}`} className="flex items-start gap-3 py-3">
              <span aria-hidden className={cn("mt-[8px] size-[7px] shrink-0 rounded-full", !showWho ? "bg-dim" : f.by === "me" ? "bg-mine" : "bg-partner")} />
              <div className="min-w-0 flex-1">
                {editing?.id === f.id ? (
                  <div className="space-y-2">
                    <Textarea
                      autoFocus
                      surface="glass"
                      rows={3}
                      maxLength={500}
                      value={editing.text}
                      aria-label="기록 고치기"
                      onChange={(e) => setEditing({ id: f.id, text: e.target.value })}
                      onKeyDown={(e) => {
                        if (e.key === "Escape") setEditing(null);
                      }}
                    />
                    <div className="flex gap-2">
                      <Button variant="soft" size="touch-sm" onClick={() => setEditing(null)}>
                        두기
                      </Button>
                      <Button variant="raised" size="touch-sm" disabled={!editing.text.trim()} onClick={() => void rewrite(f, editing.text)}>
                        저장
                      </Button>
                    </div>
                  </div>
                ) : (
                  <button
                    type="button"
                    onClick={() => setEditing({ id: f.id, text: f.content })}
                    aria-label={`${f.content} 고치기`}
                    className="-mx-1 block w-[calc(100%+0.5rem)] rounded-md px-1 text-left text-body break-keep whitespace-pre-line text-foreground focus-ring"
                  >
                    {f.content}
                  </button>
                )}
                {showWho || showWhen ? (
                  <p className="mt-0.5 text-meta text-faint">
                    {[showWho ? (f.by === "me" ? space.me.name || "나" : space.partner?.name || "상대") : null, showWhen ? ago(f.createdAt) : null]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                ) : null}
              </div>
              <Button variant="discard" size="icon-touch" onClick={() => remove(f)} aria-label="기록 지우기" className="-mr-2 shrink-0">
                <X aria-hidden className="size-4" />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <ReorderSheet open={ordering} onOpenChange={setOrdering} title="기록 순서" items={items} label={(f) => f.content} onSave={reorder} />
    </div>
  );
}
