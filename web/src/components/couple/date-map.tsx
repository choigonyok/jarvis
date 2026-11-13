"use client";

import { useId, useMemo, useRef, useState } from "react";
import { Pin, Plus, X } from "lucide-react";
import { KoreaMap, percent, useKoreaMap, type Face } from "@/components/couple/korea-map";
import { PhotoTiles, PickPhotos, ViewerAction, useUploads } from "@/components/couple/photos";
import { BlockTitle } from "@/components/couple/screen";
import { Failed } from "@/components/shell/failed";
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
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Lightbox } from "@/components/ui/lightbox";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Textarea } from "@/components/ui/textarea";
import { photoSrc, today, writeCouple, type Block, type DateRecord, type Visit } from "@/lib/couple";
import { SAY } from "@/lib/say";
import { dayRange, monthDay } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * The date map: every 시/군/구 the two have been to, its photo filling its
 * shape. A 시/도 brings its regions near with their names; a region picked
 * shows its face, every photo of it and what we did there. Photos go up from
 * the phone: a region's face, or a record's photos.
 */
export function DateMapPage({ block, onChanged }: { block: Block; onChanged: () => Promise<void> }) {
  const { map, failed } = useKoreaMap();
  const pinned = typeof block.config?.card_region === "string" ? (block.config.card_region as string) : "all";
  const [view, setView] = useState(pinned);
  const [picked, setPicked] = useState<string | null>(null);
  const [editing, setEditing] = useState<DateRecord | "new" | null>(null);
  const [viewing, setViewing] = useState<{ list: Visit[]; at: number } | null>(null);
  const [pinning, setPinning] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const panel = useRef<HTMLElement>(null);
  const uploads = useUploads(block.id);

  const visits = useMemo(() => block.visits ?? [], [block.visits]);
  const faces = useMemo(() => {
    const out = new Map<string, Face>();
    for (const v of visits) {
      if (!v.asset) continue;
      if (!out.has(v.region) || v.rep) out.set(v.region, { asset: v.asset, rotation: v.rotation });
    }
    return out;
  }, [visits]);
  const written = useMemo(() => new Set((block.records ?? []).map((r) => r.region).filter((r) => !faces.has(r))), [block.records, faces]);
  const been = (code: string) => faces.has(code) || written.has(code);

  if (failed) return <Failed>지도를 불러오지 못했어요. 새로고침해 보세요.</Failed>;
  if (!map) return <p className="pt-10 text-center text-ui text-faint">{SAY.loading}</p>;

  const regions = view === "all" ? map.regions : map.regions.filter((r) => r.sido === view);
  const visited = regions.filter((r) => been(r.code)).length;
  const region = picked ? map.regions.find((r) => r.code === picked) : undefined;
  const here = visits.filter((v) => v.region === picked && v.asset);
  const face = here.find((v) => v.rep) ?? null;
  const records = (block.records ?? []).filter((r) => r.region === picked);

  function pick(code: string) {
    setPicked((p) => (p === code ? null : code));
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
    await act(() =>
      face
        ? writeCouple("PATCH", `/blocks/${block.id}/visits/${face.id}`, { asset })
        : writeCouple("POST", `/blocks/${block.id}/visits`, { region: picked, asset, face: true }),
    );
  }

  async function pin() {
    setPinning(true);
    await act(() => writeCouple("PATCH", `/blocks/${block.id}`, { cardRegion: view }));
    setPinning(false);
  }

  const provinceName = (code: string) => map.provinces.find((p) => p.code === code)?.name ?? "";

  return (
    <div>
      <BlockTitle title={block.title} block={block} onRenamed={onChanged} />
      <div className="mt-4 flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <div className="flex items-center gap-1">
          <NativeSelect
            aria-label="지도 범위"
            value={view}
            onChange={(e) => {
              setView(e.target.value);
              setPicked(null);
            }}
          >
            <NativeSelectOption value="all">전국</NativeSelectOption>
            {map.provinces.map((p) => (
              <NativeSelectOption key={p.code} value={p.code}>
                {p.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <Button
            variant="ghost"
            size="icon-touch"
            disabled={pinning || pinned === view}
            onClick={() => void pin()}
            aria-label={pinned === view ? "이 지도가 홈 카드에 있어요" : "이 지도를 홈 카드에 두기"}
            title={pinned === view ? "홈 카드에 있는 지도" : "홈 카드에 두기"}
            className={cn("text-faint hover:text-foreground", pinned === view && "text-foreground disabled:opacity-100")}
          >
            <Pin aria-hidden className={cn("size-4", pinned === view && "fill-current")} />
          </Button>
        </div>
        <p className="text-ui text-dim">
          가 본 곳{" "}
          <span className="text-heading font-semibold text-foreground tabular-nums">
            {view === "all" ? percent(visited, regions.length) : `${visited}/${regions.length}`}
          </span>
        </p>
      </div>

      <div className="mt-3 overflow-hidden rounded-2xl bg-glass p-2 sm:p-4">
        <KoreaMap
          map={map}
          view={view}
          faces={faces}
          marked={written}
          selected={picked}
          onSelect={pick}
          labels={view !== "all"}
          size={view === "all" ? "thumb" : "display"}
          className="mx-auto h-auto max-h-[70dvh] w-full"
        />
      </div>
      <p className="mt-1.5 text-tiny text-faint">경계: 통계청 SGIS · vuski/admdongkor (CC BY 4.0)</p>
      {note && !region ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {note}
        </p>
      ) : null}

      {region ? (
        <section ref={panel} className="mt-5 scroll-mt-24 rounded-2xl bg-glass p-4 sm:p-5" aria-label={region.name}>
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 className="text-heading font-semibold tracking-tight text-foreground">{region.name}</h2>
              <p className="mt-0.5 text-aside text-dim">
                {provinceName(region.sido)} · {been(region.code) ? "가 봤어요" : "아직이에요"}
              </p>
            </div>
            <Button variant="ghost" size="icon-touch" onClick={() => setPicked(null)} aria-label="닫기" className="-mt-1 -mr-2 text-faint hover:text-foreground">
              <X aria-hidden className="size-5" />
            </Button>
          </div>

          {/* The region's face: the photo its shape is filled with. */}
          <div className="mt-3">
            {face ? (
              <button
                type="button"
                onClick={() => setViewing({ list: [face], at: 0 })}
                className="block w-full overflow-hidden rounded-xl bg-glass-raised focus-ring"
                aria-label={`${region.name} 대표 사진 크게 보기`}
              >
                {/* eslint-disable-next-line @next/next/no-img-element -- through the console's own proxy */}
                <img src={photoSrc(face.asset, "display")} alt="" className="aspect-[4/3] w-full object-cover" />
              </button>
            ) : (
              <div className="flex aspect-[4/3] w-full flex-col items-center justify-center gap-1 rounded-xl border border-dashed border-edge text-center">
                <p className="px-6 text-ui text-faint">지도에 채울 대표 사진이 없어요</p>
              </div>
            )}
            <div className="mt-2 flex flex-wrap gap-2">
              <PickPhotos multiple={false} busy={uploads.busy} label={face ? "대표 사진 바꾸기" : "대표 사진 추가"} onPick={(f) => void putFace(f)} />
              {face ? (
                <Button
                  variant="discard"
                  size="touch-sm"
                  onClick={() => void act(() => writeCouple("DELETE", `/blocks/${block.id}/visits/${face.id}`))}
                >
                  대표 사진 지우기
                </Button>
              ) : null}
            </div>
          </div>

          {here.filter((v) => v !== face).length ? (
            <div className="mt-4">
              <h3 className="text-aside text-dim">이 지역의 사진</h3>
              <PhotoTiles
                className="mt-1"
                assets={here.filter((v) => v !== face).map((v) => ({ key: v.id, asset: v.asset }))}
                onOpen={(i) => setViewing({ list: here.filter((v) => v !== face), at: i })}
              />
            </div>
          ) : null}

          {records.length ? (
            <ul className="mt-3 divide-y divide-edge-soft">
              {records.map((r) => {
                const shots = visits.filter((v) => v.record === r.id && v.asset);
                return (
                  <li key={r.id}>
                    <button
                      type="button"
                      onClick={() => setEditing(r)}
                      className="-mx-2 flex w-[calc(100%+1rem)] items-start gap-3 rounded-lg px-2 py-3 text-left transition-colors hover:bg-glass-raised focus-ring"
                    >
                      {shots[0] ? (
                        // eslint-disable-next-line @next/next/no-img-element -- through the console's own proxy
                        <img src={photoSrc(shots[0].asset)} alt="" loading="lazy" className="size-14 shrink-0 rounded-lg object-cover" />
                      ) : null}
                      <span className="min-w-0 flex-1">
                        <span className="block text-body break-keep text-foreground">{r.title || "제목 없는 날"}</span>
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
            <p className="mt-3 text-ui text-faint">{region.name}의 첫 추억을 남겨 보세요.</p>
          )}

          {note || uploads.error ? (
            <p role="alert" className="mt-2 text-aside text-reject">
              {note ?? uploads.error}
            </p>
          ) : null}

          <Button variant="raised" size="touch" onClick={() => setEditing("new")} className="mt-3 w-full gap-1.5">
            <Plus aria-hidden className="size-4" />
            데이트 기록 추가
          </Button>
        </section>
      ) : (
        <p className="mt-4 text-ui text-faint">{view === "all" ? "지역을 누르면 그곳의 사진과 기록이 열려요. 위에서 시/도를 고르면 가까이 봐요." : "지역을 눌러 사진과 기록을 보거나 남겨요."}</p>
      )}

      <RecordSheet
        block={block}
        region={region?.code ?? null}
        regionName={region?.name ?? ""}
        editing={editing}
        photos={editing && editing !== "new" ? visits.filter((v) => v.record === editing.id && v.asset) : []}
        onClose={() => setEditing(null)}
        onSaved={async () => {
          setEditing(null);
          await onChanged();
        }}
      />
      {viewing ? (
        <Lightbox
          items={viewing.list.map((v) => ({ src: photoSrc(v.asset, "display"), title: region?.name }))}
          start={viewing.at}
          onClose={() => setViewing(null)}
          actions={(i) => {
            const v = viewing.list[i];
            if (!v) return null;
            return (
              <>
                {v !== face && picked ? (
                  <ViewerAction
                    onClick={() => {
                      setViewing(null);
                      void act(() =>
                        face
                          ? writeCouple("PATCH", `/blocks/${block.id}/visits/${face.id}`, { asset: v.asset })
                          : writeCouple("POST", `/blocks/${block.id}/visits`, { region: picked, asset: v.asset, face: true }),
                      );
                    }}
                  >
                    대표로
                  </ViewerAction>
                ) : null}
                <ViewerAction
                  danger
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

function RecordSheet({
  block,
  region,
  regionName,
  editing,
  photos,
  onClose,
  onSaved,
}: {
  block: Block;
  region: string | null;
  regionName: string;
  editing: DateRecord | "new" | null;
  photos: Visit[];
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  return (
    <Panel open={editing !== null} onOpenChange={(open) => !open && onClose()} title={editing === "new" ? `${regionName} 데이트 기록` : "데이트 기록 고치기"}>
      {editing && region ? (
        <RecordForm
          key={editing === "new" ? "new" : editing.id}
          block={block}
          region={region}
          record={editing === "new" ? null : editing}
          photos={photos}
          onClose={onClose}
          onSaved={onSaved}
        />
      ) : null}
    </Panel>
  );
}

/**
 * A date's title, days, photos and note. Photos picked go up at once and are
 * put on the record when it is saved; photos taken off go when it is saved.
 */
function RecordForm({
  block,
  region,
  record,
  photos,
  onClose,
  onSaved,
}: {
  block: Block;
  region: string;
  record: DateRecord | null;
  photos: Visit[];
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const id = useId();
  const [title, setTitle] = useState(record?.title ?? "");
  const [from, setFrom] = useState(record?.from ?? today());
  const [to, setTo] = useState(record?.to ?? "");
  const [note, setNote] = useState(record?.note ?? "");
  const [added, setAdded] = useState<string[]>([]);
  const [dropped, setDropped] = useState<ReadonlySet<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [asking, setAsking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const uploads = useUploads(block.id);
  const ready = (!to || !from || to >= from) && !uploads.busy;
  const kept = photos.filter((v) => !dropped.has(v.id));

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!ready || busy) return;
    setBusy(true);
    setError(null);
    const body = { region, title: title.trim(), from, to: to || from, note: note.trim() };
    const sent = record
      ? await writeCouple<DateRecord>("PATCH", `/blocks/${block.id}/records/${record.id}`, body)
      : await writeCouple<DateRecord>("POST", `/blocks/${block.id}/records`, body);
    if (!sent.ok) {
      setBusy(false);
      return setError(sent.error);
    }
    const recordId = record?.id ?? sent.data.id;
    for (const asset of added) {
      const put = await writeCouple("POST", `/blocks/${block.id}/visits`, { record: recordId, asset });
      if (!put.ok) {
        setBusy(false);
        return setError(`기록은 저장했지만 사진을 붙이지 못했어요: ${put.error}`);
      }
    }
    for (const visit of dropped) {
      const gone = await writeCouple("DELETE", `/blocks/${block.id}/visits/${visit}`);
      if (!gone.ok) {
        setBusy(false);
        return setError(`기록은 저장했지만 사진을 빼지 못했어요: ${gone.error}`);
      }
    }
    setBusy(false);
    await onSaved();
  }

  async function remove() {
    if (!record || busy) return;
    setAsking(false);
    setBusy(true);
    setError(null);
    const sent = await writeCouple("DELETE", `/blocks/${block.id}/records/${record.id}`);
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    await onSaved();
  }

  return (
    <form onSubmit={(e) => void save(e)} className="space-y-4">
      <Field>
        <FieldLabel htmlFor={`${id}-title`}>제목</FieldLabel>
        <Input id={`${id}-title`} surface="glass" value={title} maxLength={60} placeholder="예: 바다 보러 간 날" onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <div className="grid grid-cols-2 gap-2">
        <Field>
          <FieldLabel htmlFor={`${id}-from`}>시작일</FieldLabel>
          <Input id={`${id}-from`} surface="glass" type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="min-w-0" />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-to`}>종료일</FieldLabel>
          <Input id={`${id}-to`} surface="glass" type="date" min={from} value={to} onChange={(e) => setTo(e.target.value)} className="min-w-0" />
        </Field>
      </div>
      <Field>
        <FieldLabel>사진</FieldLabel>
        <PhotoTiles
          size="sm"
          assets={[...kept.map((v) => ({ key: v.id, asset: v.asset })), ...added.map((a) => ({ key: a, asset: a }))]}
          onRemove={(i) => {
            if (i < kept.length) setDropped((s) => new Set(s).add(kept[i].id));
            else setAdded((list) => list.filter((_, j) => j !== i - kept.length));
          }}
          trailing={
            <PickPhotos
              label="사진"
              busy={uploads.busy}
              onPick={(files) => void uploads.upload(files).then((assets) => setAdded((list) => [...list, ...assets]))}
            />
          }
        />
        {uploads.error ? <p className="text-aside text-reject">{uploads.error}</p> : null}
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-note`}>메모</FieldLabel>
        <Textarea id={`${id}-note`} surface="glass" rows={3} maxLength={1000} value={note} onChange={(e) => setNote(e.target.value)} className="min-h-[5.5rem]" />
      </Field>
      {error ? (
        <p role="alert" className="text-aside text-reject">
          {error}
        </p>
      ) : null}
      <div className="flex gap-2 pt-1">
        {record ? (
          <Button type="button" variant="discard" size="touch-sm" onClick={() => setAsking(true)} disabled={busy}>
            지우기
          </Button>
        ) : (
          <Button type="button" variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
            두기
          </Button>
        )}
        <Button type="submit" variant="raised" size="touch" disabled={!ready || busy} className="flex-1">
          {busy ? "저장하는 중…" : "저장"}
        </Button>
      </div>
      <AlertDialog open={asking} onOpenChange={setAsking}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>이 기록을 지울까요?</AlertDialogTitle>
            <AlertDialogDescription>
              {photos.length ? `사진 ${photos.length}장도 함께 지워져요. ` : ""}상대의 uniple 앱에서도 사라지고, 되돌릴 수 없어요.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>두기</AlertDialogCancel>
            <AlertDialogAction onClick={() => void remove()}>지우기</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </form>
  );
}
