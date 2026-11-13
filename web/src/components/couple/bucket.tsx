"use client";

import { useMemo, useState } from "react";
import { Check as CheckMark, ChevronDown, Plus, Search, X } from "lucide-react";
import { BlockTitle } from "@/components/couple/screen";
import { useUndo } from "@/components/shell/undo";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { writeCouple, type Block, type Bucket } from "@/lib/couple";
import { cn } from "@/lib/utils";

/**
 * A bucket list: what the two want to do, the ones still ahead first. The
 * circle is the one control a row has besides taking it off - a wish done is
 * marked in approving green and kept, not crossed out; taking one off waits
 * five seconds with 되돌리기 before it goes.
 */
export function BucketPage({ block, onChanged }: { block: Block; onChanged: () => Promise<void> }) {
  const [items, setItems] = useState<Bucket[]>(block.bucket ?? []);
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState<string | null>(null);
  const [showDone, setShowDone] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { offer } = useUndo();

  // What the server sent last stands, but a change made here stays until it is read back.
  const [base, setBase] = useState(block.bucket);
  if (base !== block.bucket) {
    setBase(block.bucket);
    setItems(block.bucket ?? []);
  }

  const done = items.filter((b) => b.done);
  const ahead = items.filter((b) => !b.done);
  const share = items.length ? Math.round((done.length / items.length) * 100) : 0;
  const found = useMemo(() => {
    const q = query?.trim();
    if (!q) return null;
    return items.filter((b) => b.title.includes(q));
  }, [items, query]);

  async function add(e: React.FormEvent) {
    e.preventDefault();
    const title = draft.trim();
    if (!title || busy) return;
    setBusy(true);
    setError(null);
    const sent = await writeCouple<Bucket>("POST", `/blocks/${block.id}/bucket`, { title });
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    setDraft("");
    setItems((list) => [...list, sent.data]);
    void onChanged();
  }

  async function mark(b: Bucket, next: boolean) {
    setError(null);
    setItems((list) => list.map((x) => (x.id === b.id ? { ...x, done: next } : x)));
    const sent = await writeCouple<Bucket>("PATCH", `/blocks/${block.id}/bucket/${b.id}`, { done: next });
    if (!sent.ok) {
      setItems((list) => list.map((x) => (x.id === b.id ? { ...x, done: !next } : x)));
      setError(sent.error);
      return;
    }
    void onChanged();
  }

  function remove(b: Bucket) {
    const at = items.findIndex((x) => x.id === b.id);
    setItems((list) => list.filter((x) => x.id !== b.id));
    offer(
      `${b.title} 지웠어요`,
      () =>
        setItems((list) => {
          const next = list.slice();
          next.splice(Math.min(at, next.length), 0, b);
          return next;
        }),
      () =>
        void writeCouple("DELETE", `/blocks/${block.id}/bucket/${b.id}`).then((sent) => {
          if (!sent.ok) {
            setItems((list) => [...list, b].sort((x, y) => x.order - y.order));
            setError(sent.error);
          } else void onChanged();
        }),
    );
  }

  return (
    <div>
      <BlockTitle title={block.title}>
        <Button
          variant="ghost"
          size="icon-touch"
          onClick={() => setQuery((q) => (q === null ? "" : null))}
          aria-label={query === null ? "버킷 찾기" : "찾기 닫기"}
          className="text-dim hover:text-foreground"
        >
          {query === null ? <Search aria-hidden className="size-5" /> : <X aria-hidden className="size-5" />}
        </Button>
      </BlockTitle>

      <div className="mt-4 flex items-baseline justify-between text-ui">
        <span className="text-dim">
          {done.length}/{items.length} 이뤘어요
        </span>
        <span className="text-heading font-semibold text-foreground tabular-nums">{share}%</span>
      </div>
      <Progress value={share} className="mt-2 h-1.5" indicatorClassName="bg-foreground/70" aria-label={`${share}% 이뤘어요`} />

      {query !== null ? (
        <Input
          autoFocus
          surface="glass"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="버킷 찾기"
          aria-label="버킷 찾기"
          className="mt-5"
        />
      ) : (
        <form onSubmit={(e) => void add(e)} className="mt-5 flex gap-2">
          <Input
            surface="glass"
            value={draft}
            maxLength={100}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="예: 🌊 바다 보러 가기"
            aria-label="새 버킷"
            className="flex-1"
          />
          <Button type="submit" variant="raised" size="icon-touch" disabled={!draft.trim() || busy} aria-label="버킷 추가">
            <Plus aria-hidden className="size-5" />
          </Button>
        </form>
      )}
      {error ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {error}
        </p>
      ) : null}

      {found ? (
        found.length === 0 ? (
          <p className="mt-5 text-ui text-faint">찾는 버킷이 없어요.</p>
        ) : (
          <List items={found} onMark={mark} onRemove={remove} className="mt-3" />
        )
      ) : (
        <>
          <h2 className="mt-6 text-ui text-dim">
            할 것 <span className="tabular-nums">{ahead.length}</span>
          </h2>
          {ahead.length ? (
            <List items={ahead} onMark={mark} onRemove={remove} className="mt-1" />
          ) : (
            <p className="mt-2 text-ui text-faint">{items.length ? "전부 이뤘어요. 다음 버킷을 적어 볼까요?" : "작은 것부터 적어도 좋아요."}</p>
          )}
          {done.length ? (
            <>
              <Button variant="quiet" size="touch-sm" onClick={() => setShowDone((v) => !v)} aria-expanded={showDone} className="mt-4 -ml-2 gap-1">
                이룬 것 <span className="tabular-nums">{done.length}</span>
                <ChevronDown aria-hidden className={cn("size-4 transition-transform", showDone && "rotate-180")} />
              </Button>
              {showDone ? <List items={done} onMark={mark} onRemove={remove} className="mt-1" /> : null}
            </>
          ) : null}
        </>
      )}
    </div>
  );
}

function List({
  items,
  onMark,
  onRemove,
  className,
}: {
  items: Bucket[];
  onMark: (b: Bucket, done: boolean) => void;
  onRemove: (b: Bucket) => void;
  className?: string;
}) {
  return (
    <ul className={cn("divide-y divide-edge-soft", className)}>
      {items.map((b) => (
        <li key={b.id} className="flex min-h-12 items-center gap-3">
          <button
            type="button"
            onClick={() => onMark(b, !b.done)}
            aria-label={b.done ? `${b.title} 이룬 것 취소` : `${b.title} 이뤘어요로 표시`}
            aria-pressed={b.done}
            className="tap -m-2.5 flex shrink-0 items-center justify-center rounded-full p-2.5 focus-ring"
          >
            <span
              aria-hidden
              className={cn(
                "flex size-[22px] items-center justify-center rounded-full border-[1.5px] transition-colors",
                b.done ? "border-approve-fill bg-approve-wash text-approve" : "border-dim hover:border-foreground",
              )}
            >
              {b.done ? <CheckMark className="size-3.5" strokeWidth={3} /> : null}
            </span>
          </button>
          <span className={cn("min-w-0 flex-1 text-body break-keep", b.done ? "text-dim" : "text-foreground")}>{b.title}</span>
          <Button variant="discard" size="icon-touch" onClick={() => onRemove(b)} aria-label={`${b.title} 지우기`} className="-mr-2 shrink-0">
            <X aria-hidden className="size-4" />
          </Button>
        </li>
      ))}
    </ul>
  );
}
