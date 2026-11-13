"use client";

import { useId, useMemo, useRef, useState } from "react";
import { Plus, X } from "lucide-react";
import { KoreaMap, percent, useKoreaMap, type Face } from "@/components/couple/korea-map";
import { BlockTitle } from "@/components/couple/screen";
import { Failed } from "@/components/shell/failed";
import { Panel } from "@/components/ui/bottom-sheet";
import { Button } from "@/components/ui/button";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Lightbox, type LightboxItem } from "@/components/ui/lightbox";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Textarea } from "@/components/ui/textarea";
import { photoSrc, today, writeCouple, type Block, type DateRecord } from "@/lib/couple";
import { SAY } from "@/lib/say";
import { dayRange, monthDay } from "@/lib/when";
import { cn } from "@/lib/utils";

/**
 * The date map: every 시/군/구 the two have been to, its photo filling its
 * shape. A 시/도 brings its regions near with their names; a region picked
 * shows what we did there, and a date can be written down against it. Photos
 * are added in the app (uploading is the app's), a date written here marks
 * the region in ink until one is.
 */
export function DateMapPage({ block, onChanged }: { block: Block; onChanged: () => Promise<void> }) {
  const { map, failed } = useKoreaMap();
  const [view, setView] = useState("all");
  const [picked, setPicked] = useState<string | null>(null);
  const [editing, setEditing] = useState<DateRecord | "new" | null>(null);
  const [viewing, setViewing] = useState<number | null>(null);
  const panel = useRef<HTMLElement>(null);

  const faces = useMemo(() => {
    const out = new Map<string, Face>();
    for (const v of block.visits ?? []) {
      if (!v.asset) continue;
      if (!out.has(v.region) || v.rep) out.set(v.region, { asset: v.asset, rotation: v.rotation });
    }
    return out;
  }, [block.visits]);
  const written = useMemo(() => new Set((block.records ?? []).map((r) => r.region).filter((r) => !faces.has(r))), [block.records, faces]);
  const been = (code: string) => faces.has(code) || written.has(code);

  if (failed) return <Failed>지도를 불러오지 못했어요. 새로고침해 보세요.</Failed>;
  if (!map) return <p className="pt-10 text-center text-ui text-faint">{SAY.loading}</p>;

  const regions = view === "all" ? map.regions : map.regions.filter((r) => r.sido === view);
  const visited = regions.filter((r) => been(r.code)).length;
  const region = picked ? map.regions.find((r) => r.code === picked) : undefined;
  const photos = (block.visits ?? []).filter((v) => v.region === picked && v.asset);
  const records = (block.records ?? []).filter((r) => r.region === picked);
  const shown: LightboxItem[] = photos.map((v) => ({ src: photoSrc(v.asset, "display"), alt: region?.name, title: region?.name }));

  function pick(code: string) {
    setPicked((p) => (p === code ? null : code));
    // The region's dates are under the map: on a phone, brought into view.
    requestAnimationFrame(() => panel.current?.scrollIntoView({ behavior: "smooth", block: "nearest" }));
  }

  return (
    <div>
      <BlockTitle title={block.title} />
      <div className="mt-4 flex items-center justify-between gap-3">
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

      {region ? (
        <section ref={panel} className="mt-5 scroll-mt-24 rounded-2xl bg-glass p-4 sm:p-5" aria-label={region.name}>
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 className="text-heading font-semibold tracking-tight text-foreground">{region.name}</h2>
              <p className="mt-0.5 text-aside text-dim">
                {map.provinces.find((p) => p.code === region.sido)?.name} · {been(region.code) ? "가 봤어요" : "아직이에요"}
              </p>
            </div>
            <Button variant="ghost" size="icon-touch" onClick={() => setPicked(null)} aria-label="닫기" className="-mt-1 -mr-2 text-faint hover:text-foreground">
              <X aria-hidden className="size-5" />
            </Button>
          </div>

          {photos.length ? (
            <ul className="scrollbar-none -mx-1 mt-3 flex gap-2 overflow-x-auto px-1">
              {photos.map((v, i) => (
                <li key={v.id} className="shrink-0">
                  <button type="button" onClick={() => setViewing(i)} className="block overflow-hidden rounded-xl focus-ring" aria-label={`${region.name} 사진 ${i + 1}`}>
                    {/* eslint-disable-next-line @next/next/no-img-element */}
                    <img src={photoSrc(v.asset)} alt="" loading="lazy" className="size-24 object-cover sm:size-28" />
                  </button>
                </li>
              ))}
            </ul>
          ) : null}

          {records.length ? (
            <ul className="mt-3 divide-y divide-edge-soft">
              {records.map((r) => {
                const n = (block.visits ?? []).filter((v) => v.record === r.id && v.asset).length;
                return (
                  <li key={r.id}>
                    <button
                      type="button"
                      onClick={() => setEditing(r)}
                      className="-mx-2 flex w-[calc(100%+1rem)] items-start gap-3 rounded-lg px-2 py-3 text-left transition-colors hover:bg-glass-raised focus-ring"
                    >
                      <span className="min-w-0 flex-1">
                        <span className="block text-body break-keep text-foreground">{r.title || "제목 없는 날"}</span>
                        <span className="block text-aside text-faint">
                          {[r.from ? (r.to ? dayRange(r.from, r.to) : monthDay(r.from)) : null, n ? `사진 ${n}장` : null].filter(Boolean).join(" · ") || "날짜 없음"}
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

          <Button variant="raised" size="touch" onClick={() => setEditing("new")} className="mt-3 w-full gap-1.5">
            <Plus aria-hidden className="size-4" />
            데이트 기록 추가
          </Button>
        </section>
      ) : (
        <p className="mt-4 text-ui text-faint">{view === "all" ? "지역을 누르면 그곳의 기록이 열려요. 위에서 시/도를 고르면 가까이 봐요." : "지역을 눌러 기록을 보거나 남겨요."}</p>
      )}

      <RecordSheet
        block={block}
        region={region?.code ?? null}
        regionName={region?.name ?? ""}
        editing={editing}
        photos={editing && editing !== "new" ? (block.visits ?? []).filter((v) => v.record === editing.id).length : 0}
        onClose={() => setEditing(null)}
        onSaved={async () => {
          setEditing(null);
          await onChanged();
        }}
      />
      {viewing !== null && shown.length ? <Lightbox items={shown} start={viewing} onClose={() => setViewing(null)} /> : null}
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
  photos: number;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  return (
    <Panel
      open={editing !== null}
      onOpenChange={(open) => !open && onClose()}
      title={editing === "new" ? `${regionName} 데이트 기록` : "데이트 기록 고치기"}
      description={editing === "new" ? "사진은 uniple 앱에서 올려요. 기록만 남겨도 지도에 표시돼요." : undefined}
    >
      {editing && region ? (
        <RecordForm key={editing === "new" ? "new" : editing.id} block={block} region={region} record={editing === "new" ? null : editing} photos={photos} onClose={onClose} onSaved={onSaved} />
      ) : null}
    </Panel>
  );
}

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
  photos: number;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const id = useId();
  const [title, setTitle] = useState(record?.title ?? "");
  const [from, setFrom] = useState(record?.from ?? today());
  const [to, setTo] = useState(record?.to ?? "");
  const [note, setNote] = useState(record?.note ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ready = !to || !from || to >= from;

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!ready || busy) return;
    setBusy(true);
    setError(null);
    const body = { region, title: title.trim(), from, to: to || from, note: note.trim() };
    const sent = record
      ? await writeCouple("PATCH", `/blocks/${block.id}/records/${record.id}`, body)
      : await writeCouple("POST", `/blocks/${block.id}/records`, body);
    setBusy(false);
    if (!sent.ok) return setError(sent.error);
    await onSaved();
  }
  async function remove() {
    if (!record || busy) return;
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
          <Input id={`${id}-from`} surface="glass" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-to`}>종료일</FieldLabel>
          <Input id={`${id}-to`} surface="glass" type="date" min={from} value={to} onChange={(e) => setTo(e.target.value)} />
        </Field>
      </div>
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
          <Button
            type="button"
            variant="discard"
            size="touch-sm"
            onClick={() => void remove()}
            disabled={busy || photos > 0}
            title={photos > 0 ? "사진이 있는 기록은 uniple 앱에서 지워요" : undefined}
          >
            지우기
          </Button>
        ) : (
          <Button type="button" variant="soft" size="touch-sm" onClick={onClose} className="flex-1">
            두기
          </Button>
        )}
        <Button type="submit" variant="raised" size="touch" disabled={!ready || busy} className={cn("flex-1")}>
          {busy ? "저장하는 중…" : "저장"}
        </Button>
      </div>
      {record && photos > 0 ? <p className="text-aside text-faint">사진 {photos}장이 있는 기록이라 지우기는 uniple 앱에서 해요.</p> : null}
    </form>
  );
}
