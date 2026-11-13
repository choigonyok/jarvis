"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Plus, Search, X } from "lucide-react";
import { RecordSheet, type RecordWords } from "@/components/couple/date-map";
import { PhotoTiles, PickPhotos, SaveAction, ViewerAction, useUploads } from "@/components/couple/photos";
import { BlockTitle, useViewOnly } from "@/components/couple/screen";
import { CONTINENTS, WorldMap, useWorldMap, worldFaces, type Country } from "@/components/couple/world-map";
import { Failed } from "@/components/shell/failed";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Lightbox } from "@/components/ui/lightbox";
import { photoSrc, writeCouple, type Block, type DateRecord, type Visit } from "@/lib/couple";
import { onOpenParams } from "@/lib/focus";
import { SAY } from "@/lib/say";
import { dayRange, monthDay } from "@/lib/when";
import { cn } from "@/lib/utils";

const TRIP_WORDS: RecordWords = { noun: "여행 기록", title: "도시", placeholder: "예: 교토" };

/**
 * The world map: every country the two have been to, its photo filling its
 * shape. A country is found on the map or by name; picked, it shows its
 * face, every trip there and their photos.
 */
export function WorldMapPage({ block, onChanged }: { block: Block; onChanged: () => Promise<void> }) {
  const viewOnly = useViewOnly();
  const { map, failed } = useWorldMap();
  const [picked, setPicked] = useState<string | null>(null);
  const [finding, setFinding] = useState(false);
  const [editing, setEditing] = useState<DateRecord | "new" | null>(null);
  const [viewing, setViewing] = useState<{ list: Visit[]; at: number } | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const panel = useRef<HTMLElement>(null);
  const uploads = useUploads(block.id);

  const visits = useMemo(() => block.visits ?? [], [block.visits]);
  const faces = useMemo(() => worldFaces(visits), [visits]);

  // News about a country opens it (?region=, news.tsx).
  useEffect(() => {
    if (!map) return;
    const open = (params: URLSearchParams) => {
      const code = params.get("region");
      if (code && map.countries.some((c) => c.code === code)) setPicked(code);
    };
    open(new URLSearchParams(window.location.search));
    return onOpenParams(open);
  }, [map]);

  if (failed) return <Failed>지도를 불러오지 못했어요. 새로고침해 보세요.</Failed>;
  if (!map) return <p className="pt-10 text-center text-ui text-faint">{SAY.loading}</p>;

  const country = picked ? map.countries.find((c) => c.code === picked) : undefined;
  const face = visits.find((v) => v.region === picked && v.rep && v.asset) ?? null;
  const trips = (block.records ?? []).filter((r) => r.region === picked);
  const tripPhotos = visits.filter((v) => v.region === picked && !v.rep && v.asset);

  function pick(code: string) {
    setPicked((p) => (p === code ? null : code));
    setFinding(false);
    setNote(null);
    requestAnimationFrame(() => panel.current?.scrollIntoView({ behavior: "smooth", block: "nearest" }));
  }

  async function act(run: () => Promise<{ ok: boolean; error?: string }>) {
    setNote(null);
    const sent = await run();
    if (!sent.ok) setNote(sent.error ?? SAY.couldNotSave);
    await onChanged();
  }

  async function putFace(files: File[]) {
    const [asset] = await uploads.upload(files.slice(0, 1));
    if (!asset || !picked) return;
    await act(() => writeCouple("POST", `/blocks/${block.id}/visits`, { region: picked, asset, face: true }));
  }

  return (
    <div>
      <BlockTitle title={block.title} block={block} onRenamed={onChanged}>
        <Button
          variant="ghost"
          size="icon-touch"
          onClick={() => setFinding((v) => !v)}
          aria-label={finding ? "나라 찾기 닫기" : "나라 찾기"}
          className="text-dim hover:text-foreground"
        >
          {finding ? <X aria-hidden className="size-5" /> : <Search aria-hidden className="size-5" />}
        </Button>
      </BlockTitle>
      <p className="mt-2 text-ui text-dim">
        방문 나라 <span className="text-heading font-semibold text-foreground tabular-nums">{faces.size}</span>곳
      </p>

      {finding ? <FindCountry countries={map.countries} visited={faces} onPick={pick} /> : null}

      <div className="mt-3 overflow-hidden rounded-2xl bg-glass p-2 sm:p-3">
        <WorldMap map={map} faces={faces} selected={picked} onSelect={pick} size={picked ? "display" : "thumb"} className="h-auto w-full" />
      </div>
      <p className="mt-1.5 text-tiny text-faint">경계: Natural Earth</p>
      {note && !country ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {note}
        </p>
      ) : null}

      {country ? (
        <section ref={panel} className="mt-5 scroll-mt-24 rounded-2xl bg-glass p-4 sm:p-5" aria-label={country.name}>
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 className="text-heading font-semibold tracking-tight text-foreground">{country.name}</h2>
              <p className="mt-0.5 text-aside text-dim">
                {CONTINENTS.find((c) => c.key === country.continent)?.name ?? "기타"} · {faces.has(country.code) ? "방문 완료" : "아직이에요"}
              </p>
            </div>
            <Button variant="ghost" size="icon-touch" onClick={() => setPicked(null)} aria-label="닫기" className="-mt-1 -mr-2 text-faint hover:text-foreground">
              <X aria-hidden className="size-5" />
            </Button>
          </div>

          <div className="mt-3">
            {face ? (
              <button
                type="button"
                onClick={() => setViewing({ list: [face], at: 0 })}
                className="block w-full overflow-hidden rounded-xl bg-glass-raised focus-ring"
                aria-label={`${country.name} 대표 사진 크게 보기`}
              >
                {/* eslint-disable-next-line @next/next/no-img-element -- through the console's own proxy */}
                <img src={photoSrc(face.asset, "display")} alt="" className="aspect-[4/3] w-full object-cover" />
              </button>
            ) : (
              <div className="flex aspect-[4/3] w-full items-center justify-center rounded-xl border border-dashed border-edge text-center">
                <p className="px-6 text-ui text-faint">지도에 채울 대표 사진이 없어요</p>
              </div>
            )}
            <div className={cn("mt-2 flex flex-wrap gap-2", viewOnly && "hidden")}>
              <PickPhotos multiple={false} busy={uploads.busy} label={face ? "대표 사진 바꾸기" : "대표 사진 추가"} onPick={(f) => void putFace(f)} />
              {face ? (
                <Button variant="discard" size="touch-sm" onClick={() => void act(() => writeCouple("DELETE", `/blocks/${block.id}/visits/${face.id}`))}>
                  대표 사진 지우기
                </Button>
              ) : null}
            </div>
          </div>

          {trips.length ? (
            <ul className="mt-3 divide-y divide-edge-soft">
              {trips.map((r) => {
                const shots = tripPhotos.filter((v) => v.record === r.id);
                return (
                  <li key={r.id}>
                    <button
                      type="button"
                      onClick={() => !viewOnly && setEditing(r)}
                      className="-mx-2 flex w-[calc(100%+1rem)] items-start gap-3 rounded-lg px-2 py-3 text-left transition-colors hover:bg-glass-raised focus-ring"
                    >
                      {shots[0] ? (
                        // eslint-disable-next-line @next/next/no-img-element -- through the console's own proxy
                        <img src={photoSrc(shots[0].asset)} alt="" loading="lazy" className="size-14 shrink-0 rounded-lg object-cover" />
                      ) : null}
                      <span className="min-w-0 flex-1">
                        <span className="block text-body break-keep text-foreground">{r.title || "도시 없는 여행"}</span>
                        <span className="block text-aside text-faint">
                          {[r.from ? (r.to ? dayRange(r.from, r.to) : monthDay(r.from)) : null, shots.length ? `사진 ${shots.length}장` : null]
                            .filter(Boolean)
                            .join(" · ") || "날짜 없음"}
                        </span>
                        {r.note ? <span className="mt-1 line-clamp-2 block text-ui break-keep text-dim">{r.note}</span> : null}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p className="mt-3 text-ui text-faint">{country.name}의 첫 추억을 남겨 보세요.</p>
          )}
          {tripPhotos.length ? (
            <PhotoTiles
              className="mt-2"
              size="sm"
              assets={tripPhotos.map((v) => ({ key: v.id, asset: v.asset }))}
              onOpen={(i) => setViewing({ list: tripPhotos, at: i })}
            />
          ) : null}

          {note || uploads.error ? (
            <p role="alert" className="mt-2 text-aside text-reject">
              {note ?? uploads.error}
            </p>
          ) : null}
          {viewOnly ? null : (
            <Button variant="raised" size="touch" onClick={() => setEditing("new")} className="mt-3 w-full gap-1.5">
              <Plus aria-hidden className="size-4" />
              여행 기록 추가
            </Button>
          )}
        </section>
      ) : (
        <p className="mt-4 text-ui text-faint">나라를 누르거나 찾아서 사진과 여행 기록을 남겨요.</p>
      )}

      <RecordSheet
        block={block}
        region={country?.code ?? null}
        regionName={country?.name ?? ""}
        editing={editing}
        photos={editing && editing !== "new" ? tripPhotos.filter((v) => v.record === editing.id) : []}
        words={TRIP_WORDS}
        onClose={() => setEditing(null)}
        onSaved={async () => {
          setEditing(null);
          await onChanged();
        }}
      />
      {viewing ? (
        <Lightbox
          items={viewing.list.map((v) => ({ src: photoSrc(v.asset, "display"), title: country?.name }))}
          start={viewing.at}
          onClose={() => setViewing(null)}
          actions={(i) => {
            const v = viewing.list[i];
            if (!v) return null;
            return (
              <>
                <SaveAction asset={v.asset} name={country?.name} />
                {v.rep || viewOnly ? null : (
                  <ViewerAction
                    onClick={() => {
                      setViewing(null);
                      if (picked) void act(() => writeCouple("POST", `/blocks/${block.id}/visits`, { region: picked, asset: v.asset, face: true }));
                    }}
                  >
                    대표로
                  </ViewerAction>
                )}
                <ViewerAction
                  danger
                  disabled={viewOnly}
                  onClick={() => {
                    setViewing(null);
                    void act(() => writeCouple("DELETE", `/blocks/${block.id}/visits/${v.id}`));
                  }}
                >
                  지우기
                </ViewerAction>
              </>
            );
          }}
        />
      ) : null}
    </div>
  );
}

/** Countries by name, a continent at a time, the visited marked. */
function FindCountry({ countries, visited, onPick }: { countries: Country[]; visited: ReadonlyMap<string, unknown>; onPick: (code: string) => void }) {
  const [query, setQuery] = useState("");
  const [continent, setContinent] = useState<Country["continent"] | "all">("all");
  const found = useMemo(() => {
    const q = query.replace(/\s+/g, "");
    return countries
      .filter((c) => c.continent !== "etc")
      .filter((c) => continent === "all" || c.continent === continent)
      .filter((c) => !q || c.name.replace(/\s+/g, "").includes(q) || c.code === q.toUpperCase())
      .sort((a, b) => a.name.localeCompare(b.name, "ko"));
  }, [countries, query, continent]);
  return (
    <div className="mt-3 rounded-2xl bg-glass p-3">
      <Input autoFocus surface="glass" value={query} onChange={(e) => setQuery(e.target.value)} placeholder="나라 찾기" aria-label="나라 찾기" />
      <div className="scrollbar-none -mx-1 mt-2 flex gap-1.5 overflow-x-auto px-1" role="group" aria-label="대륙">
        {[{ key: "all" as const, name: "전체" }, ...CONTINENTS].map((c) => (
          <button
            key={c.key}
            type="button"
            aria-pressed={continent === c.key}
            onClick={() => setContinent(c.key)}
            className={cn(
              "tap-y h-9 shrink-0 rounded-full border px-3 text-aside transition-colors focus-ring",
              continent === c.key ? "border-edge bg-glass-raised text-foreground" : "border-edge-soft text-faint hover:text-dim",
            )}
          >
            {c.name}
          </button>
        ))}
      </div>
      {found.length ? (
        <ul className="mt-2 max-h-72 divide-y divide-edge-soft overflow-y-auto overscroll-contain">
          {found.map((c) => (
            <li key={c.code}>
              <button
                type="button"
                onClick={() => onPick(c.code)}
                className="flex min-h-11 w-full items-center gap-2 px-1 text-left text-body text-foreground focus-ring"
              >
                <span className="min-w-0 flex-1 truncate">{c.name}</span>
                {visited.has(c.code) ? <span className="shrink-0 text-meta text-dim">방문</span> : null}
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-3 px-1 text-ui text-faint">찾는 나라가 없어요.</p>
      )}
    </div>
  );
}
