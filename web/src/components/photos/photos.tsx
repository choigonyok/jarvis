"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import Link from "next/link";
import { Brush, ChevronLeft, Play, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import { Lightbox } from "@/components/ui/lightbox";
import { BackCloses } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Header, PageTop } from "@/components/shell/header";
import { TabBar } from "@/components/shell/tab-bar";
import { StandingBar } from "@/components/shell/standing-bar";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Failed } from "@/components/shell/failed";
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
import { SAY } from "@/lib/say";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { dateTime } from "@/lib/when";
import { cn } from "@/lib/utils";

type Place = { kind: "library" } | { kind: "albums" } | { kind: "album"; album: Album };
/**
 * A search's results come from the whole library, and go back to the place it
 * was made from. n tells one sending apart from the next: the same words sent
 * again - after it failed, say - are read again.
 */
type View = Place | { kind: "search"; query: string; from: Place; n: number };

/**
 * Photos and videos backed up to Immich on the Mac mini. The phone's Immich
 * app does the backing up; this tab is for looking: the library by month,
 * albums, and search by what is in the picture.
 *
 * An album and a search are a step in, as a sheet is: going back - an edge
 * swipe - steps out to where they were opened from, not out of the tab. The
 * library and the album list stay as they were read under the other one, and
 * each finds its place again on coming back.
 */
export function Photos() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const [view, setView] = useState<View>({ kind: "library" });
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<{ list: Asset[]; index: number } | null>(null);
  const place: Place = view.kind === "search" ? view.from : view;
  // The album open, under a search made from it too: its months stay read.
  const album = place.kind === "album" ? place.album : null;
  const deep = view.kind !== "library" && view.kind !== "albums";
  // One history entry while a step in, taken by going back: a step out that
  // is still in (a search made in an album, back to the album) is given a
  // new one. Stepping out by a button lets the entry go.
  const [entry, setEntry] = useState(0);
  function stepOut() {
    if (view.kind === "search") {
      setQuery("");
      setView(view.from);
    } else if (view.kind === "album") setView({ kind: "albums" });
    setEntry((n) => n + 1);
  }

  // Where each view was scrolled to: they share one scroller, which a shorter
  // view pulls back to its top. An album's and a search's are let go with
  // them - opened again, they are read again from the top.
  const main = useRef<HTMLElement>(null);
  const scrolled = useRef(new Map<string, number>());
  const spot = view.kind === "search" ? `search ${view.n}` : view.kind === "album" ? `album ${view.album.id}` : view.kind;
  const shown = useRef(spot);
  const kept = album ? `album ${album.id}` : "";
  useLayoutEffect(() => {
    shown.current = spot;
    for (const k of [...scrolled.current.keys()]) if (k !== spot && k !== kept && k !== "library" && k !== "albums") scrolled.current.delete(k);
    if (main.current) main.current.scrollTop = scrolled.current.get(spot) ?? 0;
  }, [spot, kept]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      {deep ? <BackCloses key={entry} close={stepOut} /> : null}
      <PageTop>
        <Header
          connection={connection}
          pending={waiting.length}
          actions={
            <Link
              href="/edit"
              aria-label="사진 편집"
              title="사진 편집"
              className="flex size-11 items-center justify-center rounded-lg text-faint transition-colors hover:text-dim focus-ring"
            >
              <Brush aria-hidden className="size-[18px]" strokeWidth={1.8} />
            </Link>
          }
        />
        <StandingBar pending={waiting} />
      </PageTop>

      <main
        ref={main}
        onScroll={(e) => scrolled.current.set(shown.current, e.currentTarget.scrollTop)}
        className="scrollbar-hairline inset-x-safe relative min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar"
      >
        <div className="mx-auto w-full max-w-page-wider px-3 pt-3 pb-10 sm:px-6 sm:pt-5">
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
                className="tap -ms-1 flex items-center gap-1 rounded-lg px-1.5 text-ui text-dim hover:text-foreground focus-ring mouse:min-h-9"
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
                if (q) setView({ kind: "search", query: q, from: place, n: Date.now() });
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
                    if (!e.target.value && view.kind === "search") setView(view.from);
                  }}
                  placeholder="바다, 강아지, 생일 케이크…"
                  className="ps-1.5 pe-2"
                />
              </InputGroup>
            </form>
          </div>

          {/* Both kept, the one not shown hidden: what was read of the
              library - and of the album list, under an album - is there to
              come back to, not read again from the top. */}
          <TabsContent value="library" forceMount className="data-[state=inactive]:hidden">
            <Timeline onOpen={(list, index) => setOpen({ list, index })} />
          </TabsContent>
          <TabsContent value="albums" forceMount className="data-[state=inactive]:hidden">
            <div hidden={!!album}>
              <Albums onPick={(a) => setView({ kind: "album", album: a })} />
            </div>
            {album ? <Timeline key={album.id} albumId={album.id} onOpen={(list, index) => setOpen({ list, index })} /> : null}
          </TabsContent>
          {view.kind === "search" ? (
            <Results key={view.n} query={view.query} onOpen={(list, index) => setOpen({ list, index })} />
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
  // Months whose read failed, said where their photos go: left as grey
  // squares, they looked like months still coming.
  const [missed, setMissed] = useState<ReadonlySet<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const q = albumId ? `&albumId=${albumId}` : "";
  // Months on their way, asked for once: every draw had made each month's
  // watcher anew, and each new one asked again for a month already coming.
  const asking = useRef(new Set<string>());
  // What is on hand, for the counts read on coming back to be held against.
  const onHand = useRef(months);
  useEffect(() => {
    onHand.current = months;
  }, [months]);

  /** Reads a month now: the first time, again, or once more after it failed. */
  const load = useCallback(
    async (bucket: string) => {
      if (asking.current.has(bucket)) return;
      asking.current.add(bucket);
      try {
        const cols = await get<Parameters<typeof flatten>[0]>(`timeline/bucket?timeBucket=${encodeURIComponent(bucket)}${q}`);
        setMonths((m) => ({ ...m, [bucket]: flatten(cols) }));
        setMissed((s) => {
          if (!s.has(bucket)) return s;
          const next = new Set(s);
          next.delete(bucket);
          return next;
        });
      } catch {
        setMissed((s) => new Set(s).add(bucket));
      } finally {
        asking.current.delete(bucket);
      }
    },
    [q],
  );

  const readBuckets = useCallback(
    () =>
      get<Bucket[]>(`timeline/buckets${albumId ? `?albumId=${albumId}` : ""}`)
        .then((b) => {
          setBuckets(b);
          setError(null);
          // A month read before whose count has moved - a photo just backed
          // up - is read again, showing what it had until then: only the
          // number beside its name had changed.
          for (const x of b) {
            const had = onHand.current[x.timeBucket];
            if (had && had.length !== x.count) void load(x.timeBucket);
          }
        })
        .catch(() => setError("사진 서버에 연결하지 못했어요.")),
    [albumId, load],
  );
  useEffect(() => {
    void readBuckets();
  }, [readBuckets]);
  // Back to the app, or a pull to refresh: months backed up meanwhile.
  useEffect(() => onResume(readBuckets), [readBuckets]);

  if (error) {
    return (
      <Failed onRetry={readBuckets} className="py-10">
        {error}
      </Failed>
    );
  }
  // The first read: a month's name and its squares where they will be, not
  // the space under the tabs empty until everything lands at once.
  if (!buckets) {
    return (
      <>
        <p role="status" className="sr-only">
          {SAY.loading}
        </p>
        <div aria-hidden>
          <div className="mx-1 mt-1 mb-3 h-3.5 w-24 rounded bg-well" />
          <ul className="grid grid-cols-3 gap-[2px] sm:grid-cols-4 lg:grid-cols-6">
            {Array.from({ length: 18 }, (_, i) => (
              <li key={i} className="aspect-square bg-well" />
            ))}
          </ul>
        </div>
      </>
    );
  }
  if (buckets.length === 0) {
    return (
      <Empty className="py-16">
        <EmptyHeader>
          <EmptyTitle>{albumId ? "앨범이 비어 있어요" : "아직 백업된 사진이 없어요"}</EmptyTitle>
          {albumId ? null : (
            <EmptyDescription>
              아이폰에 Immich 앱을 설치하고 맥미니로 백업을 켜면, 올라간 사진과 영상이 여기에 날짜별로 쌓여요.
            </EmptyDescription>
          )}
        </EmptyHeader>
      </Empty>
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
          failed={missed.has(b.timeBucket)}
          onNear={load}
          onOpen={(a) => onOpen(loaded, loaded.findIndex((x) => x.id === a.id))}
        />
      ))}
    </div>
  );
}

function Month({
  bucket,
  assets,
  failed,
  onNear,
  onOpen,
}: {
  bucket: Bucket;
  assets?: Asset[];
  /** Its last read failed. */
  failed?: boolean;
  /** Reads the month: as it scrolls near, and on 다시 시도. The same function every draw, so its watcher is made once. */
  onNear: (bucket: string) => unknown;
  onOpen: (a: Asset) => void;
}) {
  const ref = useRef<HTMLElement>(null);
  const key = bucket.timeBucket;
  useEffect(() => {
    if (assets) return;
    const el = ref.current;
    if (!el) return;
    const io = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && onNear(key), {
      rootMargin: "800px 0px",
    });
    io.observe(el);
    return () => io.disconnect();
  }, [assets, onNear, key]);

  return (
    <section ref={ref} aria-label={monthLabel(key)}>
      <h2 className="mb-2 flex items-baseline gap-2 px-1 text-ui text-ink">
        {monthLabel(key)}
        <span className="tnum text-meta text-faint">{bucket.count}</span>
      </h2>
      {failed && !assets ? (
        <Failed onRetry={() => onNear(key)} className="px-1">
          이 달의 사진을 불러오지 못했어요.
        </Failed>
      ) : (
        <Grid count={bucket.count} assets={assets} onOpen={onOpen} />
      )}
    </section>
  );
}

/**
 * How tall count squares stand in cols columns 2px apart, as a padding of the
 * row's width: rows × the square, which is the width less the gaps over cols,
 * plus the gaps between the rows.
 */
function tallness(count: number, cols: number): string {
  const rows = Math.ceil(count / cols);
  return `calc(${(100 * rows) / cols}% + ${2 * (rows / cols - 1)}px)`;
}

function Grid({ count, assets, onOpen }: { count: number; assets?: Asset[]; onOpen: (a: Asset) => void }) {
  return (
    <ul className="grid grid-cols-3 gap-[2px] sm:grid-cols-4 lg:grid-cols-6">
      {assets
        ? assets.map((a) => (
            <li key={a.id}>
              {/* Named by when it was taken - hundreds of 사진 보기 told nothing apart -
                  and its ring above the tiles next to it, which covered it at a 2px gap. */}
              <button
                type="button"
                onClick={() => onOpen(a)}
                aria-label={`${dateTime(a.takenAt)} ${a.isImage ? "사진" : "영상"} 보기`}
                className="relative block aspect-square w-full overflow-hidden bg-well focus-ring focus-visible:z-10"
              >
                {/* eslint-disable-next-line @next/next/no-img-element -- Immich thumbnails through the proxy */}
                <img src={thumbUrl(a.id)} alt="" loading="lazy" className="size-full object-cover" />
                {!a.isImage ? (
                  <span className="tnum absolute right-1 bottom-1 flex items-center gap-0.5 rounded bg-black/55 px-1 py-px text-tiny text-white">
                    <Play aria-hidden className="size-2.5 fill-current" />
                    {clip(a.duration)}
                  </span>
                ) : null}
              </button>
            </li>
          ))
        : // The month's whole height before it comes, one block for each
          // width's columns: sixty squares and no more, a month of three
          // hundred grew five times over as it came in, and the place being
          // looked at went down the screen with it.
          [3, 4, 6].map((cols) => (
            <li
              key={cols}
              aria-hidden
              className={cn("col-span-full bg-well", cols === 3 ? "sm:hidden" : cols === 4 ? "hidden sm:block lg:hidden" : "hidden lg:block")}
              style={{ paddingBottom: tallness(count, cols) }}
            />
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
      <Failed onRetry={readAlbums} className="py-10">
        {error}
      </Failed>
    );
  }
  if (!albums) {
    return (
      <>
        <p role="status" className="sr-only">
          {SAY.loading}
        </p>
        <ul aria-hidden className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
          {Array.from({ length: 4 }, (_, i) => (
            <li key={i}>
              <span className="block aspect-square rounded-xl bg-well" />
              <span className="mt-2 block h-3.5 w-20 rounded bg-well" />
            </li>
          ))}
        </ul>
      </>
    );
  }
  if (albums.length === 0) {
    return (
      <Empty className="py-16">
        <EmptyHeader>
          <EmptyTitle>앨범이 없어요</EmptyTitle>
          <EmptyDescription>Immich 앱이나 웹에서 앨범을 만들면 여기에 나와요.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  return (
    <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
      {albums.map((a) => (
        <li key={a.id}>
          <button type="button" onClick={() => onPick(a)} className="block w-full text-left focus-ring">
            <span className="block aspect-square overflow-hidden rounded-xl bg-well">
              {a.albumThumbnailAssetId ? (
                // eslint-disable-next-line @next/next/no-img-element -- Immich thumbnails through the proxy
                <img src={thumbUrl(a.albumThumbnailAssetId)} alt="" loading="lazy" className="size-full object-cover" />
              ) : null}
            </span>
            <span className="mt-1.5 block truncate text-ui text-ink">{a.albumName}</span>
            <span className="tnum block text-meta text-faint">{a.assetCount}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

/** Why a search's read failed: no line at all, or the photo server not answering. */
const searchFailure = (err: unknown) => (err instanceof TypeError ? SAY.offline : "사진 서버에 연결하지 못했어요.");

function Results({ query, onOpen }: { query: string; onOpen: (list: Asset[], index: number) => void }) {
  const [found, setFound] = useState<{ assets: Asset[]; next: number | null } | null>(null);
  const [error, setError] = useState<string | null>(null);
  // 더 찾기 under way, and why its last read failed - said where it was pressed.
  const [reading, setReading] = useState(false);
  const [moreFailed, setMoreFailed] = useState<string | null>(null);
  const search = useCallback(
    () =>
      smartSearch(query).then(
        (page) => {
          setFound(page);
          setError(null);
        },
        (err: unknown) => setError(searchFailure(err)),
      ),
    [query],
  );
  useEffect(() => {
    void search();
  }, [search]);

  /** The next page, after what is shown. */
  async function findMore() {
    if (!found?.next || reading) return;
    setReading(true);
    setMoreFailed(null);
    try {
      const page = await smartSearch(query, found.next);
      setFound((f) => {
        if (!f) return f;
        const had = new Set(f.assets.map((a) => a.id));
        return { assets: [...f.assets, ...page.assets.filter((a) => !had.has(a.id))], next: page.next };
      });
    } catch (err) {
      setMoreFailed(searchFailure(err));
    } finally {
      setReading(false);
    }
  }

  if (error) {
    return (
      <Failed onRetry={search} className="py-10">
        {error}
      </Failed>
    );
  }
  if (!found) return <p className="py-16 text-center text-ui text-faint">찾는 중…</p>;
  const { assets, next } = found;
  if (assets.length === 0) {
    return (
      <Empty className="py-16">
        <EmptyHeader>
          <EmptyTitle>&ldquo;{query}&rdquo;에 맞는 사진이 없어요</EmptyTitle>
          <EmptyDescription>다른 말로 찾아보세요. 사진 분석이 끝나지 않은 사진은 아직 찾지 못해요.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }
  return (
    <section aria-label={`${query} 검색 결과`}>
      {/* + where there are more than this, as the conversation's search counts. */}
      <p className="mb-2 px-1 text-aside text-dim">
        &ldquo;{query}&rdquo;{" "}
        <span className="tnum text-faint">
          {assets.length}
          {next ? "+" : ""}
        </span>
      </p>
      <Grid count={assets.length} assets={assets} onOpen={(a) => onOpen(assets, assets.indexOf(a))} />
      {next ? (
        <>
          {moreFailed ? (
            <p role="alert" className="mt-3 px-1 text-aside text-reject">
              {moreFailed}
            </p>
          ) : null}
          <Button variant="quiet" size="touch-sm" disabled={reading} onClick={() => void findMore()} className="mt-2 w-full">
            {reading ? "찾는 중…" : moreFailed ? "다시 찾기" : "더 찾기"}
          </Button>
        </>
      ) : null}
    </section>
  );
}
