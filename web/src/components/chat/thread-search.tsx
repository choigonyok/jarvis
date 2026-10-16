"use client";

import { useEffect, useRef, useState, type RefObject } from "react";
import { ChevronDown, ChevronUp, Search, X } from "lucide-react";
import { cn } from "@/lib/utils";

/** Text that is chrome, not something said: times, buttons, the quote copies. */
const SKIP = "button, svg, [data-search-skip]";

type Highlights = Map<string, unknown> & { set(name: string, h: unknown): void; delete(name: string): boolean };

function highlights(): Highlights | null {
  const css = globalThis.CSS as unknown as { highlights?: Highlights } | undefined;
  return css?.highlights && typeof (globalThis as { Highlight?: unknown }).Highlight === "function" ? css.highlights : null;
}

function mark(name: string, ranges: Range[]) {
  const hl = highlights();
  if (!hl) return;
  if (ranges.length === 0) {
    hl.delete(name);
    return;
  }
  const H = (globalThis as unknown as { Highlight: new (...r: Range[]) => unknown }).Highlight;
  hl.set(name, new H(...ranges));
}

/** Every place `query` appears in what the thread shows, newest first. */
function find(root: HTMLElement, query: string): Range[] {
  const q = query.toLocaleLowerCase("ko-KR");
  const out: Range[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (node) =>
      node.parentElement?.closest(SKIP) ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT,
  });
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = (node.nodeValue ?? "").toLocaleLowerCase("ko-KR");
    for (let at = text.indexOf(q); at !== -1; at = text.indexOf(q, at + q.length)) {
      const r = document.createRange();
      r.setStart(node, at);
      r.setEnd(node, at + q.length);
      out.push(r);
    }
  }
  return out.reverse();
}

/**
 * Searching the conversation, the way a messenger does it: type, and the
 * newest place it was said comes into view with every other place marked; the
 * arrows walk back through older ones. What is searched is what the thread
 * shows - the markdown as rendered, the cards' words - so a hit is always
 * something you can see when you land on it.
 *
 * Marks are the CSS Custom Highlight API: nothing is wrapped or re-rendered,
 * so the thread keeps its own DOM. Where the API is missing the arrows still
 * walk, and the line the hit is on is brought to the middle.
 */
export function useThreadSearch(root: RefObject<HTMLElement | null>) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<Range[]>([]);
  const [at, setAt] = useState(0);
  const first = useRef(true);
  // Bumped when the thread's DOM changes under an open search - a new turn, a
  // card decided - so the marks are found again rather than left pointing at
  // text that is gone.
  const [version, setVersion] = useState(0);
  useEffect(() => {
    const el = root.current;
    if (!open || !el) return;
    let t = 0;
    const mo = new MutationObserver(() => {
      window.clearTimeout(t);
      t = window.setTimeout(() => setVersion((v) => v + 1), 200);
    });
    mo.observe(el, { childList: true, subtree: true, characterData: true });
    return () => {
      mo.disconnect();
      window.clearTimeout(t);
    };
  }, [open, root]);

  const searching = open && query.trim() !== "";
  const live = searching ? hits : NONE;

  // Look again as the query settles and as the thread changes under it.
  useEffect(() => {
    const el = root.current;
    const q = query.trim();
    if (!open || !el || !q) {
      mark("chat-search", []);
      mark("chat-search-current", []);
      return;
    }
    const t = window.setTimeout(() => {
      const found = find(el, q);
      setHits(found);
      setAt((i) => (first.current ? 0 : Math.min(i, Math.max(0, found.length - 1))));
      mark("chat-search", found);
    }, 140);
    return () => window.clearTimeout(t);
  }, [open, query, version, root]);

  // Bring the current one to the middle of the thread.
  useEffect(() => {
    const el = root.current;
    const hit = live[at];
    mark("chat-search-current", hit ? [hit] : []);
    if (!el || !hit) return;
    const box = hit.getBoundingClientRect();
    const frame = el.getBoundingClientRect();
    if (box.height === 0) return;
    const smooth = !first.current && !window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    first.current = false;
    el.scrollTo({ top: el.scrollTop + box.top - frame.top - el.clientHeight / 2, behavior: smooth ? "smooth" : "auto" });
  }, [live, at, root]);

  useEffect(
    () => () => {
      mark("chat-search", []);
      mark("chat-search-current", []);
    },
    [],
  );

  return {
    open,
    query,
    total: live.length,
    at: live.length ? at : 0,
    setQuery: (q: string) => {
      first.current = true;
      setQuery(q);
    },
    show: () => {
      first.current = true;
      setOpen(true);
    },
    close: () => {
      setOpen(false);
      setQuery("");
      setHits(NONE);
      setAt(0);
    },
    /** Older: further up the thread. */
    older: () => setAt((i) => (live.length ? Math.min(live.length - 1, i + 1) : 0)),
    newer: () => setAt((i) => Math.max(0, i - 1)),
  };
}

const NONE: Range[] = [];

export type ThreadSearch = ReturnType<typeof useThreadSearch>;

/** The search button in the header. */
export function SearchToggle({ search }: { search: ThreadSearch }) {
  return (
    <button
      type="button"
      onClick={() => (search.open ? search.close() : search.show())}
      aria-label={search.open ? "검색 닫기" : "대화 검색"}
      aria-pressed={search.open}
      className={cn(
        "flex size-11 items-center justify-center rounded-lg transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 sm:size-8",
        search.open ? "text-foreground" : "text-faint hover:text-dim",
      )}
    >
      <Search aria-hidden className="size-[18px] sm:size-4" />
    </button>
  );
}

/** The row under the header while searching. */
export function SearchBar({ search }: { search: ThreadSearch }) {
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (search.open) input.current?.focus();
  }, [search.open]);
  if (!search.open) return null;

  const q = search.query.trim();
  const count = !q ? "" : search.total === 0 ? "없음" : `${search.at + 1}/${search.total}`;
  return (
    <div className="inset-x-safe shrink-0 border-b border-edge-soft bg-background" role="search">
      <div className="mx-auto flex h-14 w-full max-w-[52rem] items-center gap-1 px-4 sm:px-8">
        <label className="flex h-10 min-w-0 flex-1 items-center gap-2 rounded-full bg-well px-3.5">
          <Search aria-hidden className="size-4 shrink-0 text-faint" />
          <input
            ref={input}
            type="search"
            enterKeyHint="search"
            value={search.query}
            onChange={(e) => search.setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing) return;
              if (e.key === "Escape") search.close();
              if (e.key === "Enter") {
                e.preventDefault();
                if (e.shiftKey) search.newer();
                else search.older();
              }
            }}
            placeholder="대화에서 찾기"
            aria-label="대화에서 찾을 말"
            className="min-w-0 flex-1 bg-transparent text-[14.5px] text-foreground outline-none placeholder:text-faint [&::-webkit-search-cancel-button]:hidden"
          />
          <span className="tnum shrink-0 text-[12px] text-faint" aria-live="polite">
            {count}
          </span>
        </label>
        <button
          type="button"
          onClick={search.older}
          disabled={search.at >= search.total - 1}
          aria-label="이전 결과 (더 오래된)"
          className="flex size-10 shrink-0 items-center justify-center rounded-full text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30"
        >
          <ChevronUp aria-hidden className="size-[18px]" />
        </button>
        <button
          type="button"
          onClick={search.newer}
          disabled={search.at <= 0}
          aria-label="다음 결과 (더 최근)"
          className="flex size-10 shrink-0 items-center justify-center rounded-full text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30"
        >
          <ChevronDown aria-hidden className="size-[18px]" />
        </button>
        <button
          type="button"
          onClick={search.close}
          aria-label="검색 닫기"
          className="-mr-2 flex size-10 shrink-0 items-center justify-center rounded-full text-faint outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <X aria-hidden className="size-[18px]" />
        </button>
      </div>
    </div>
  );
}
