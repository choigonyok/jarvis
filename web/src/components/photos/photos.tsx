"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Brush, ChevronLeft, Play, Search } from "lucide-react";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import { Lightbox } from "@/components/ui/lightbox";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Header, TabBar } from "@/components/shell/header";
import { Retry } from "@/components/shell/retry";
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
import { onResume } from "@/lib/resume";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { dateTime } from "@/lib/when";

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
      <Header
        connection={connection}
        pending={waiting.length}
        actions={
          <Link
            href="/edit"
            aria-label="사진 편집"
            title="사진 편집"
            className="flex size-11 items-center justify-center rounded-lg text-faint transition-colors outline-none hover:text-dim focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <Brush aria-hidden className="size-[18px]" strokeWidth={1.8} />
          </Link>
        }
      />
      <StandingBar pending={waiting} />

      <main className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[64rem] px-3 pt-3 pb-10 sm:px-6 sm:pt-5">
          {/* 보관함 and 앨범 are tabs; a search is neither - its results show with no tab lit. */}
          <Tabs
            value={view.kind === "library" ? "library" : view.kind === "search" ? "" : "albums"}
            onValueChange={(v) => {
              setQuery("");
              setView({ kind: v as "library" | "albums" });
            }}
            className="contents"
          >
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
              <TabsList aria-label="보기">
                <TabsTrigger value="library">보관함</TabsTrigger>
                <TabsTrigger value="albums">앨범</TabsTrigger>
              </TabsList>
            )}
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const q = query.trim();
                if (q) setView({ kind: "search", query: q });
              }}
              className="ms-auto min-w-0 flex-1 sm:max-w-72"
            >
              <InputGroup className="h-9 border-transparent mouse:h-9">
                <InputGroupAddon className="pl-2.5">
                  <Search aria-hidden className="size-3.5" />
                </InputGroupAddon>
                <InputGroupInput
                  id="photo-search"
                  type="search"
                  aria-label="사진에 있는 것으로 찾기"
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value);
                    if (!e.target.value && view.kind === "search") setView({ kind: "library" });
                  }}
                  placeholder="바다, 강아지, 생일 케이크…"
                  className="ps-1.5 pe-2"
                />
              </InputGroup>
            </form>
          </div>

          <TabsContent value="library">
            <Timeline onOpen={(list, index) => setOpen({ list, index })} />
          </TabsContent>
          <TabsContent value="albums">
            {view.kind === "album" ? (
              <Timeline key={view.album.id} albumId={view.album.id} onOpen={(list, index) => setOpen({ list, index })} />
            ) : (
              <Albums onPick={(album) => setView({ kind: "album", album })} />
            )}
          </TabsContent>
          {view.kind === "search" ? (
            <Results key={view.query} query={view.query} onOpen={(list, index) => setOpen({ list, index })} />
          ) : null}
          </Tabs>
        </div>
      </main>

      {/* Full screen in the app's own viewer (ui/lightbox.tsx): two fingers
          zoom, a finger moves on or pulls down to close. */}
      {open ? (
        <Lightbox
          items={open.list.map((a) => ({
            kind: a.isImage ? "image" : "video",
            src: a.isImage ? thumbUrl(a.id, "preview") : videoUrl(a.id),
            poster: a.isImage ? undefined : thumbUrl(a.id, "preview"),
            title: dateTime(a.takenAt),
            download: originalUrl(a.id),
          }))}
          start={open.index}
          onClose={() => setOpen(null)}
        />
      ) : null}
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

  const readBuckets = useCallback(
    () =>
      get<Bucket[]>(`timeline/buckets${albumId ? `?albumId=${albumId}` : ""}`)
        .then((b) => {
          setBuckets(b);
          setError(null);
        })
        .catch(() => setError("사진 서버에 연결하지 못했어요.")),
    [albumId],
  );
  useEffect(() => {
    void readBuckets();
  }, [readBuckets]);
  // Back to the app, or a pull to refresh: months backed up meanwhile.
  useEffect(() => onResume(readBuckets), [readBuckets]);

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

  if (error) {
    return (
      <div className="py-16 text-center">
        <p className="text-[13px] text-dim">{error}</p>
        <Retry onRetry={readBuckets} />
      </div>
    );
  }
  if (!buckets) return null;
  if (buckets.length === 0) {
    return (
      <div className="py-16 text-center">
        <p className="text-[14px] text-foreground/90">{albumId ? "앨범이 비어 있어요." : "아직 백업된 사진이 없어요."}</p>
        {albumId ? null : (
          <p className="mx-auto mt-2 max-w-[26rem] text-[12.5px] leading-relaxed text-dim">
            아이폰에 Immich 앱을 설치하고 맥미니로 백업을 켜면, 올라간 사진과 영상이 여기에 날짜별로 쌓여요.
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
  const readAlbums = useCallback(
    () =>
      get<Album[]>("albums")
        .then((a) => {
          setAlbums(a);
          setError(null);
        })
        .catch(() => setError("앨범을 불러오지 못했어요.")),
    [],
  );
  useEffect(() => {
    void readAlbums();
  }, [readAlbums]);
  if (error) {
    return (
      <div className="py-16 text-center">
        <p className="text-[13px] text-dim">{error}</p>
        <Retry onRetry={readAlbums} />
      </div>
    );
  }
  if (!albums) return null;
  if (albums.length === 0) {
    return <p className="py-16 text-center text-[13px] text-dim">앨범이 없어요. Immich 앱이나 웹에서 앨범을 만들면 여기에 나와요.</p>;
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
      .catch(() => setError("찾지 못했어요. 사진 분석이 끝나지 않았을 수 있어요."));
  }, [query]);
  if (error) return <p className="py-16 text-center text-[13px] text-dim">{error}</p>;
  if (!assets) return <p className="py-16 text-center text-[13px] text-faint">찾는 중…</p>;
  if (assets.length === 0) return <p className="py-16 text-center text-[13px] text-dim">&ldquo;{query}&rdquo;에 맞는 사진이 없어요.</p>;
  return (
    <section aria-label={`${query} 검색 결과`}>
      <p className="mb-2 px-1 text-[12.5px] text-dim">
        &ldquo;{query}&rdquo; <span className="tnum text-faint">{assets.length}</span>
      </p>
      <Grid count={assets.length} assets={assets} onOpen={(a) => onOpen(assets, assets.indexOf(a))} />
    </section>
  );
}
