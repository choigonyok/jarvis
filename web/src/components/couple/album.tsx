"use client";

import { useMemo, useState } from "react";
import { Camera, Pin } from "lucide-react";
import { PhotoTiles, PickBar, PickPhotos, SaveAction, ViewerAction } from "@/components/couple/photos";
import { BlockTitle, useViewOnly } from "@/components/couple/screen";
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
import { Button } from "@/components/ui/button";
import { Lightbox } from "@/components/ui/lightbox";
import { photoSrc, uploadSized, writeCouple, type AlbumPhoto, type Block, type Space } from "@/lib/couple";
import { dateLabel } from "@/lib/when";
import { cn } from "@/lib/utils";

/** The most photos one upload puts in, as the app lets one go. */
const BATCH = 30;

/** Mine or theirs on the home's album card: the photo pinned, else the newest snap. */
export function shownOf(block: Block, person: string | undefined): string | undefined {
  if (!person) return undefined;
  const config = (block.config ?? {}) as { latest?: Record<string, { asset?: string; at?: string }>; pinned?: Record<string, { asset?: string; at?: string }> };
  const latest = config.latest?.[person];
  const pinned = config.pinned?.[person];
  if (pinned?.asset && (!latest?.at || (pinned.at ?? "") >= latest.at)) return pinned.asset;
  return latest?.asset;
}

/**
 * The shared album: every photo either of us put in, newest taken first. A
 * photo goes in from the phone's library, or straight from its camera - a
 * snap, which the app shows on the home as the latest and tells the partner
 * of. Several can be taken out at once; one can be pinned to the home.
 */
export function AlbumPage({ block, space, onChanged }: { block: Block; space: Space; onChanged: () => Promise<void> }) {
  const viewOnly = useViewOnly();
  const photos = useMemo(() => block.album ?? [], [block.album]);
  const [busy, setBusy] = useState<{ done: number; of: number } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [picking, setPicking] = useState<ReadonlySet<string> | null>(null);
  const [asking, setAsking] = useState(false);
  const [viewing, setViewing] = useState<number | null>(null);
  const shown = useMemo(() => photos.filter((p) => p.media === "photo"), [photos]);
  const mine = shownOf(block, space.me.id);

  async function put(files: File[], snap: boolean) {
    if (files.length > BATCH) return setError(`한 번에 ${BATCH}장까지 올릴 수 있어요. 나눠서 올려 주세요.`);
    setError(null);
    setNote(null);
    const up: { asset: string; width: number; height: number }[] = [];
    for (let i = 0; i < files.length; i++) {
      setBusy({ done: i, of: files.length });
      const sent = await uploadSized(block.id, files[i]);
      if (!sent.ok) {
        setError(files.length > 1 ? `${i + 1}번째 사진: ${sent.error}` : sent.error);
        break;
      }
      up.push(sent.data);
    }
    if (up.length) {
      const sent = await writeCouple("POST", `/blocks/${block.id}/album`, { photos: up, snap });
      if (!sent.ok) setError(sent.error);
      else setNote(snap ? "찍은 사진을 올렸어요. 상대 폰에 알림이 가요." : `${up.length}장을 올렸어요.`);
      await onChanged();
    }
    setBusy(null);
  }

  async function takeOut() {
    if (!picking?.size) return;
    setAsking(false);
    setBusy({ done: 0, of: picking.size });
    const sent = await writeCouple("POST", `/blocks/${block.id}/album/delete`, { ids: [...picking] });
    setBusy(null);
    if (!sent.ok) return setError(sent.error);
    setPicking(null);
    setNote("사진을 지웠어요.");
    await onChanged();
  }

  async function pin(p: AlbumPhoto | null) {
    const sent = await writeCouple("POST", `/blocks/${block.id}/album/pin`, { asset: p?.asset ?? "" });
    if (!sent.ok) return setError(sent.error);
    setNote(p ? "홈에 띄웠어요." : "홈에서 내렸어요.");
    await onChanged();
  }

  return (
    <div>
      <BlockTitle title={block.title} block={block} onRenamed={onChanged} />
      <p className="mt-2 text-ui text-dim">
        <span className="tabular-nums">{photos.length}</span>장
        {photos.length !== shown.length ? <span className="text-faint"> · 동영상 {photos.length - shown.length}개는 uniple 앱에서 봐요</span> : null}
      </p>
      <div className={cn("mt-3 flex flex-wrap gap-2", viewOnly && "hidden")}>
        <PickPhotos camera label="사진 찍기" busy={busy} variant="raised" icon={<Camera aria-hidden className="size-4" />} onPick={(f) => void put(f, true)} />
        <PickPhotos label="사진 추가" busy={busy} onPick={(f) => void put(f, false)} />
        {shown.length > 1 && !picking ? (
          <Button variant="quiet" size="touch-sm" onClick={() => setPicking(new Set())}>
            여러 장 지우기
          </Button>
        ) : null}
      </div>
      {error ? (
        <p role="alert" className="mt-2 text-aside text-reject">
          {error}
        </p>
      ) : note ? (
        <p className="mt-2 text-aside text-dim" role="status">
          {note}
        </p>
      ) : null}
      {picking ? <PickBar count={picking.size} busy={!!busy} onCancel={() => setPicking(null)} onRemove={() => setAsking(true)} /> : null}

      {shown.length === 0 ? (
        <p className="mt-6 text-ui text-faint">둘이 찍은 사진을 여기에 모아요. 사진 찍기로 바로 올리면 상대 홈에도 떠요.</p>
      ) : (
        <ul className="mt-4 grid grid-cols-3 gap-1 sm:grid-cols-4">
          {shown.map((p, i) => (
            <li key={p.id} className="relative">
              <PhotoTile
                photo={p}
                pinned={p.asset === mine}
                picked={picking ? picking.has(p.id) : undefined}
                onTap={() =>
                  picking
                    ? setPicking((s) => {
                        const next = new Set(s);
                        if (next.has(p.id)) next.delete(p.id);
                        else next.add(p.id);
                        return next;
                      })
                    : setViewing(i)
                }
              />
            </li>
          ))}
        </ul>
      )}

      <AlertDialog open={asking} onOpenChange={setAsking}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>사진 {picking?.size ?? 0}장을 지울까요?</AlertDialogTitle>
            <AlertDialogDescription>앨범에서 빠지고 상대의 uniple 앱에서도 사라져요.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>두기</AlertDialogCancel>
            <AlertDialogAction onClick={() => void takeOut()}>지우기</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {viewing !== null && shown[viewing] ? (
        <Lightbox
          items={shown.map((p) => ({ src: photoSrc(p.asset, "display"), title: `${p.by === "me" ? space.me.name || "나" : space.partner?.name || "상대"} · ${dateLabel((p.takenAt ?? p.at).slice(0, 10))}` }))}
          start={viewing}
          onClose={() => setViewing(null)}
          actions={(i) => {
            const p = shown[i];
            if (!p) return null;
            return (
              <>
                <SaveAction asset={p.asset} name={`album-${i + 1}`} />
                <ViewerAction
                  disabled={viewOnly}
                  onClick={() => {
                    setViewing(null);
                    void pin(p.asset === mine ? null : p);
                  }}
                >
                  {p.asset === mine ? "홈에서 내리기" : "홈에 띄우기"}
                </ViewerAction>
              </>
            );
          }}
        />
      ) : null}
    </div>
  );
}

function PhotoTile({ photo, pinned, picked, onTap }: { photo: AlbumPhoto; pinned: boolean; picked?: boolean; onTap: () => void }) {
  return (
    <>
      <PhotoTiles
        className="m-0 block p-0"
        assets={[{ key: photo.id, asset: photo.asset }]}
        picked={picked === undefined ? undefined : new Set(picked ? [photo.id] : [])}
        onPick={onTap}
        onOpen={onTap}
        size="fill"
      />
      {pinned ? (
        <span className="pointer-events-none absolute bottom-1 left-1 flex items-center gap-0.5 rounded bg-black/60 px-1 text-tiny text-white">
          <Pin aria-hidden className="size-3" />홈
        </span>
      ) : null}
    </>
  );
}
