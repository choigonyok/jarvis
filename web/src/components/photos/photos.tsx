"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronLeft, Download, Play, Search, X } from "lucide-react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import {
  type Album,
  type Asset,
  type Bucket,
  clip,
  flatten,
  get,
  monthLabel,
  originalUrl,
  smartSearch,
  thumbUrl,
  videoUrl,
} from "@/lib/photos";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

type View = { kind: "library" } | { kind: "albums" } | { kind: "album"; album: Album } | { kind: "search"; query: string };

/**
 * Photos and videos backed up to Immich on the Mac mini. The phone's Immich
 * app does the backing up; this tab is for looking: the library by month,
 * albums, and search by what is in the picture.
 */
export function Photos() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const [view, setView] = useState<View>({ kind: "library" });
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<{ list: Asset[]; index: number } | null>(null);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[64rem] px-3 pt-3 pb-10 sm:px-6 sm:pt-5">
          <div className="mb-3 flex flex-wrap items-center gap-2">
            {view.kind === "album" ? (
              <button
                type="button"
                onClick={() => setView({ kind: "albums" })}
                className="tap -ms-1 flex min-h-9 items-center gap-1 rounded-lg px-1.5 text-[13px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <ChevronLeft aria-hidden className="size-4" />
                {view.album.albumName}
              </button>
            ) : (
              <div role="tablist" aria-label="보기" className="inline-flex gap-0.5 rounded-lg border border-edge-soft bg-glass p-0.5">
                {(
                  [
                    ["library", "보관함"],
                    ["albums", "앨범"],
                  ] as const
                ).map(([kind, label]) => (
                  <button
                    key={kind}
                    type="button"
                    role="tab"
                    aria-selected={view.kind === kind}
                    onClick={() => {
                      setQuery("");
                      setView({ kind });
                    }}
                    className={cn(
                      "min-h-8 rounded-md px-3 text-[12.5px] transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
                      view.kind === kind ? "bg-glass-raised text-foreground" : "text-faint hover:text-dim",
                    )}
                  >
                    {label}
                  </button>
                ))}
              </div>
            )}
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const q = query.trim();
                if (q) setView({ kind: "search", query: q });
              }}
              className="relative ms-auto min-w-0 flex-1 sm:max-w-72"
            >
              <label className="sr-only" htmlFor="photo-search">
                사진에 있는 것으로 찾기
              </label>
              <Search aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-faint" />
              <input
                id="photo-search"
                type="search"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  if (!e.target.value && view.kind === "search") setView({ kind: "library" });
                }}
                placeholder="바다, 강아지, 생일 케이크…"
                className="h-9 w-full rounded-lg bg-glass ps-8 pe-2 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-[13px]"
              />
            </form>
          </div>

          {view.kind === "library" ? <Timeline onOpen={(list, index) => setOpen({ list, index })} /> : null}
          {view.kind === "album" ? (
            <Timeline key={view.album.id} albumId={view.album.id} onOpen={(list, index) => setOpen({ list, index })} />
          ) : null}
          {view.kind === "albums" ? <Albums onPick={(album) => setView({ kind: "album", album })} /> : null}
          {view.kind === "search" ? (
            <Results key={view.query} query={view.query} onOpen={(list, index) => setOpen({ list, index })} />
          ) : null}
        </div>
      </main>

      {open ? <Viewer list={open.list} start={open.index} onClose={() => setOpen(null)} /> : null}
      <TabBar pending={waiting.length} />
    </div>
  );
}

/** The library (or one album) by month, each month fetched as it scrolls near. */
function Timeline({ albumId, onOpen }: { albumId?: string; onOpen: (list: Asset[], index: number) => void }) {
  const [buckets, setBuckets] = useState<Bucket[] | null>(null);
  const [months, setMonths] = useState<Record<string, Asset[]>>({});
  const [error, setError] = useState<string | null>(null);
  const q = albumId ? `&albumId=${albumId}` : "";

  useEffect(() => {
    get<Bucket[]>(`timeline/buckets${albumId ? `?albumId=${albumId}` : ""}`)
      .then((b) => setBuckets(b))
      .catch(() => setError("사진 서버에 연결하지 못했습니다."));
  }, [albumId]);

  const load = useCallback(
    async (bucket: string) => {
      if (months[bucket]) return;
      try {
        const cols = await get<Parameters<typeof flatten>[0]>(`timeline/bucket?timeBucket=${encodeURIComponent(bucket)}${q}`);
        setMonths((m) => ({ ...m, [bucket]: flatten(cols) }));
      } catch {
        // Left as placeholders; scrolling past again retries.
      }
    },
    [months, q],
  );

  if (error) return <p className="py-16 text-center text-[13px] text-dim">{error}</p>;
  if (!buckets) return null;
  if (buckets.length === 0) {
    return (
      <div className="py-16 text-center">
        <p className="text-[14px] text-foreground/90">{albumId ? "앨범이 비어 있습니다." : "아직 백업된 사진이 없습니다."}</p>
        {albumId ? null : (
          <p className="mx-auto mt-2 max-w-[26rem] text-[12.5px] leading-relaxed text-dim">
            아이폰에 Immich 앱을 설치하고 맥미니로 백업을 켜면, 올라간 사진과 영상이 여기에 날짜별로 쌓입니다.
          </p>
        )}
      </div>
    );
  }

  // Everything loaded so far, in order: the viewer pages through it all.
  const loaded = buckets.flatMap((b) => months[b.timeBucket] ?? []);

  return (
    <div className="space-y-6">
      {buckets.map((b) => (
        <Month
          key={b.timeBucket}
          bucket={b}
          assets={months[b.timeBucket]}
          onNear={() => void load(b.timeBucket)}
          onOpen={(a) => onOpen(loaded, loaded.findIndex((x) => x.id === a.id))}
        />
      ))}
    </div>
  );
}

function Month({
  bucket,
  assets,
  onNear,
  onOpen,
}: {
  bucket: Bucket;
  assets?: Asset[];
  onNear: () => void;
  onOpen: (a: Asset) => void;
}) {
  const ref = useRef<HTMLElement>(null);
  useEffect(() => {
    if (assets) return;
    const el = ref.current;
    if (!el) return;
    const io = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && onNear(), {
      rootMargin: "800px 0px",
    });
    io.observe(el);
    return () => io.disconnect();
  }, [assets, onNear]);

  return (
    <section ref={ref} aria-label={monthLabel(bucket.timeBucket)}>
      <h2 className="mb-2 flex items-baseline gap-2 px-1 text-[13.5px] text-foreground/90">
        {monthLabel(bucket.timeBucket)}
        <span className="tnum text-[11.5px] text-faint">{bucket.count}</span>
      </h2>
      <Grid count={bucket.count} assets={assets} onOpen={onOpen} />
    </section>
  );
}

function Grid({ count, assets, onOpen }: { count: number; assets?: Asset[]; onOpen: (a: Asset) => void }) {
  return (
    <ul className="grid grid-cols-3 gap-[2px] sm:grid-cols-4 lg:grid-cols-6">
      {assets
        ? assets.map((a) => (
            <li key={a.id}>
              <button
                type="button"
                onClick={() => onOpen(a)}
                aria-label={a.isImage ? "사진 보기" : "영상 보기"}
                className="relative block aspect-square w-full overflow-hidden bg-well outline-none focus-visible:ring-3 focus-visible:ring-ring/60"
              >
                {/* eslint-disable-next-line @next/next/no-img-element -- Immich thumbnails through the proxy */}
                <img src={thumbUrl(a.id)} alt="" loading="lazy" className="size-full object-cover" />
                {!a.isImage ? (
                  <span className="tnum absolute right-1 bottom-1 flex items-center gap-0.5 rounded bg-black/55 px-1 py-px text-[10.5px] text-white">
                    <Play aria-hidden className="size-2.5 fill-current" />
                    {clip(a.duration)}
                  </span>
                ) : null}
              </button>
            </li>
          ))
        : Array.from({ length: Math.min(count, 60) }, (_, i) => (
            <li key={i} aria-hidden className="aspect-square bg-well" />
          ))}
    </ul>
  );
}

function Albums({ onPick }: { onPick: (a: Album) => void }) {
  const [albums, setAlbums] = useState<Album[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    get<Album[]>("albums")
      .then(setAlbums)
      .catch(() => setError("앨범을 불러오지 못했습니다."));
  }, []);
  if (error) return <p className="py-16 text-center text-[13px] text-dim">{error}</p>;
  if (!albums) return null;
  if (albums.length === 0) {
    return <p className="py-16 text-center text-[13px] text-dim">앨범이 없습니다. Immich 앱이나 웹에서 앨범을 만들면 여기에 나옵니다.</p>;
  }
  return (
    <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
      {albums.map((a) => (
        <li key={a.id}>
          <button type="button" onClick={() => onPick(a)} className="block w-full text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
            <span className="block aspect-square overflow-hidden rounded-xl bg-well">
              {a.albumThumbnailAssetId ? (
                // eslint-disable-next-line @next/next/no-img-element -- Immich thumbnails through the proxy
                <img src={thumbUrl(a.albumThumbnailAssetId)} alt="" loading="lazy" className="size-full object-cover" />
              ) : null}
            </span>
            <span className="mt-1.5 block truncate text-[13px] text-foreground/90">{a.albumName}</span>
            <span className="tnum block text-[11.5px] text-faint">{a.assetCount}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function Results({ query, onOpen }: { query: string; onOpen: (list: Asset[], index: number) => void }) {
  const [assets, setAssets] = useState<Asset[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    smartSearch(query)
      .then(setAssets)
      .catch(() => setError("찾지 못했습니다. 사진 분석이 끝나지 않았을 수 있습니다."));
  }, [query]);
  if (error) return <p className="py-16 text-center text-[13px] text-dim">{error}</p>;
  if (!assets) return <p className="py-16 text-center text-[13px] text-faint">찾는 중…</p>;
  if (assets.length === 0) return <p className="py-16 text-center text-[13px] text-dim">&ldquo;{query}&rdquo;에 맞는 사진이 없습니다.</p>;
  return (
    <section aria-label={`${query} 검색 결과`}>
      <p className="mb-2 px-1 text-[12.5px] text-dim">
        &ldquo;{query}&rdquo; <span className="tnum text-faint">{assets.length}</span>
      </p>
      <Grid count={assets.length} assets={assets} onOpen={(a) => onOpen(assets, assets.indexOf(a))} />
    </section>
  );
}

/** One photo or video, full screen. Swipe or arrow keys to move, Esc to close. */
function Viewer({ list, start, onClose }: { list: Asset[]; start: number; onClose: () => void }) {
  const [index, setIndex] = useState(Math.max(0, start));
  const a = list[index];
  const drag = useRef<{ x: number; y: number } | null>(null);
  const go = useCallback((d: number) => setIndex((i) => Math.min(list.length - 1, Math.max(0, i + d))), [list.length]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "ArrowLeft") go(-1);
      if (e.key === "ArrowRight") go(1);
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [go, onClose]);

  if (!a) return null;
  const when = new Date(a.takenAt).toLocaleString("ko-KR", {
    year: "numeric",
    month: "long",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });

  return (
    <div
      role="dialog"
      aria-modal
      aria-label="사진 보기"
      className="screen-h fixed inset-x-0 top-0 z-[70] flex flex-col bg-black"
      onPointerDown={(e) => {
        drag.current = { x: e.clientX, y: e.clientY };
      }}
      onPointerUp={(e) => {
        const d = drag.current;
        drag.current = null;
        if (!d) return;
        const dx = e.clientX - d.x;
        const dy = e.clientY - d.y;
        if (Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy)) go(dx < 0 ? 1 : -1);
        else if (dy > 120 && Math.abs(dy) > Math.abs(dx)) onClose();
      }}
    >
      <div className="pt-safe flex shrink-0 items-center gap-2 px-2 py-2 text-white">
        <button type="button" onClick={onClose} aria-label="닫기" className="flex size-11 items-center justify-center rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-white/50">
          <X aria-hidden className="size-5" />
        </button>
        <p className="tnum min-w-0 flex-1 truncate text-[12.5px] text-white/75">
          {when}
          <span className="ms-2 text-white/45">
            {index + 1} / {list.length}
          </span>
        </p>
        <a href={originalUrl(a.id)} download aria-label="원본 받기" className="flex size-11 items-center justify-center rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-white/50">
          <Download aria-hidden className="size-5" />
        </a>
      </div>
      <div className="pb-safe flex min-h-0 flex-1 items-center justify-center">
        {a.isImage ? (
          // eslint-disable-next-line @next/next/no-img-element -- Immich preview through the proxy
          <img key={a.id} src={thumbUrl(a.id, "preview")} alt="" className="max-h-full max-w-full object-contain select-none" draggable={false} />
        ) : (
          <video key={a.id} src={videoUrl(a.id)} poster={thumbUrl(a.id, "preview")} controls autoPlay playsInline className="max-h-full max-w-full" />
        )}
      </div>
    </div>
  );
}
