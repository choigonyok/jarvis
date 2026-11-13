"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { Check, ImagePlus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { photoSrc, uploadPhoto } from "@/lib/couple";
import { cn } from "@/lib/utils";

/**
 * Photos of the couple's space, added and taken off here: picked on the phone,
 * made the app's size, uploaded for a block, then put where they belong (a
 * day, a region's face, a date on the map). The uploading says how far it
 * got; a photo that did not go says why beside the button that sent it.
 */

/** Uploads photos one after another for a block, saying how far it got. */
export function useUploads(block: string | null) {
  const [busy, setBusy] = useState<{ done: number; of: number } | null>(null);
  const [error, setError] = useState<string | null>(null);

  /** Resolves to the assets that went up, in order; the first failure stops the rest. */
  async function upload(files: File[]): Promise<string[]> {
    if (!block || files.length === 0) return [];
    setError(null);
    const out: string[] = [];
    for (let i = 0; i < files.length; i++) {
      setBusy({ done: i, of: files.length });
      const sent = await uploadPhoto(block, files[i]);
      if (!sent.ok) {
        setError(files.length > 1 ? `${i + 1}번째 사진: ${sent.error}` : sent.error);
        break;
      }
      out.push(sent.data);
    }
    setBusy(null);
    return out;
  }
  return { busy, error, setError, upload };
}

/** The button that picks photos: the phone's own picker (library or camera). */
export function PickPhotos({
  onPick,
  busy,
  label = "사진 추가",
  multiple = true,
  className,
  variant = "soft",
  camera = false,
  icon,
}: {
  onPick: (files: File[]) => void;
  busy?: { done: number; of: number } | null;
  label?: string;
  multiple?: boolean;
  className?: string;
  variant?: "soft" | "raised" | "quiet";
  /** The phone's camera straight away, one photo. */
  camera?: boolean;
  icon?: ReactNode;
}) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <>
      <input
        ref={input}
        type="file"
        // No HEIC on the list: then the phone hands its photos over as JPEG,
        // which every browser here can read.
        accept="image/jpeg,image/png,image/webp,image/*"
        multiple={multiple && !camera}
        capture={camera ? "environment" : undefined}
        hidden
        onChange={(e) => {
          const files = Array.from(e.target.files ?? []);
          e.target.value = "";
          if (files.length) onPick(files);
        }}
      />
      <Button type="button" variant={variant} size="touch-sm" disabled={!!busy} onClick={() => input.current?.click()} className={cn("gap-1.5", className)}>
        {icon ?? <ImagePlus aria-hidden className="size-4" />}
        {busy ? `올리는 중… ${busy.done + 1}/${busy.of}` : label}
      </Button>
    </>
  );
}

/**
 * A row of photos: tap one to look at it; with onRemove, each has a way off.
 * With picked, a tap picks it instead - several to take off at once.
 */
export function PhotoTiles({
  assets,
  onOpen,
  onRemove,
  picked,
  onPick,
  size = "md",
  className,
  trailing,
}: {
  assets: { key: string; asset: string; mark?: ReactNode }[];
  onOpen?: (index: number) => void;
  onRemove?: (index: number) => void;
  /** The photos picked, by key; set while picking. */
  picked?: ReadonlySet<string>;
  onPick?: (key: string) => void;
  /** fill: one tile as wide as its cell, square (a grid's). */
  size?: "sm" | "md" | "lg" | "fill";
  className?: string;
  /** What comes after the photos in the row (a way to add one). */
  trailing?: ReactNode;
}) {
  const box = size === "sm" ? "size-16" : size === "lg" ? "size-28 sm:size-32" : size === "fill" ? "aspect-square w-full" : "size-20 sm:size-24";
  const fill = size === "fill";
  return (
    <ul className={cn(fill ? "block" : "scrollbar-none -mx-1 flex gap-2 overflow-x-auto px-1 py-1", className)}>
      {assets.map((p, i) => (
        <li key={p.key} className={cn("relative", fill ? "block" : "shrink-0")}>
          <button
            type="button"
            onClick={() => (picked ? onPick?.(p.key) : onOpen?.(i))}
            className={cn("block overflow-hidden bg-glass-raised focus-ring", fill ? "w-full rounded-md" : "rounded-xl")}
            aria-label={picked ? `사진 ${i + 1} ${picked.has(p.key) ? "고르기 취소" : "고르기"}` : `사진 ${i + 1} 크게 보기`}
            aria-pressed={picked ? picked.has(p.key) : undefined}
          >
            {/* eslint-disable-next-line @next/next/no-img-element -- through the console's own proxy */}
            <img src={photoSrc(p.asset)} alt="" loading="lazy" className={cn("object-cover transition-opacity", box, picked && !picked.has(p.key) && "opacity-60")} />
          </button>
          {p.mark ? <span className="pointer-events-none absolute bottom-1 left-1">{p.mark}</span> : null}
          {picked ? (
            <span
              aria-hidden
              className={cn(
                "pointer-events-none absolute top-1 right-1 flex size-6 items-center justify-center rounded-full ring-2 ring-white/90",
                picked.has(p.key) ? "bg-foreground text-background" : "bg-black/30",
              )}
            >
              {picked.has(p.key) ? <Check className="size-3.5" strokeWidth={3} /> : null}
            </span>
          ) : null}
          {onRemove && !picked ? (
            <button
              type="button"
              onClick={() => onRemove(i)}
              aria-label={`사진 ${i + 1} 빼기`}
              className="tap absolute -top-1 -right-1 flex items-center justify-center focus-ring"
            >
              <span className="flex size-6 items-center justify-center rounded-full bg-background/90 text-ink shadow-sm ring-1 ring-edge">
                <X aria-hidden className="size-3.5" />
              </span>
            </button>
          ) : null}
        </li>
      ))}
      {trailing ? <li className="flex shrink-0 items-center">{trailing}</li> : null}
    </ul>
  );
}

/** A button on the dark photo viewer's top bar. */
export function ViewerAction({ onClick, children, danger, disabled }: { onClick: () => void; children: ReactNode; danger?: boolean; disabled?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "flex h-11 items-center rounded-lg px-3 text-ui outline-none focus-visible:ring-3 focus-visible:ring-white/50 disabled:opacity-40",
        danger ? "text-[oklch(0.75_0.15_25)]" : "text-white",
      )}
    >
      {children}
    </button>
  );
}

/**
 * 저장 on the photo viewer: the phone's own share sheet (사진에 저장, 보내기),
 * or a download where there is none. The photo is fetched as the viewer
 * shows it, so the tap hands it over at once - a share sheet opens only
 * straight from a tap.
 */
export function SaveAction({ asset, name }: { asset: string; name?: string }) {
  const [file, setFile] = useState<{ asset: string; file: File } | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    fetch(photoSrc(asset, "display"))
      .then((res) => (res.ok ? res.blob() : Promise.reject(new Error(String(res.status)))))
      .then((blob) => {
        if (!live) return;
        const ext = blob.type === "image/png" ? "png" : blob.type === "image/webp" ? "webp" : "jpg";
        setFile({ asset, file: new File([blob], `${name || "photo"}.${ext}`, { type: blob.type || "image/jpeg" }) });
      })
      .catch(() => live && setFailed(true));
    return () => {
      live = false;
    };
  }, [asset, name]);
  const ready = file?.asset === asset ? file.file : null;

  function save() {
    if (!ready) return;
    if (navigator.canShare?.({ files: [ready] })) {
      navigator.share({ files: [ready] }).catch(() => undefined);
      return;
    }
    const url = URL.createObjectURL(ready);
    const a = document.createElement("a");
    a.href = url;
    a.download = ready.name;
    document.body.append(a);
    a.click();
    a.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
  }

  return (
    <ViewerAction onClick={save} disabled={!ready}>
      {failed ? "저장 못 함" : "저장"}
    </ViewerAction>
  );
}

/** A tool bar for picking several photos to take off: how many, take them off, or stop. */
export function PickBar({ count, busy, onRemove, onCancel }: { count: number; busy?: boolean; onRemove: () => void; onCancel: () => void }) {
  return (
    <div className="mt-2 flex items-center gap-2">
      <span className="mr-auto text-aside text-dim tabular-nums">{count ? `${count}장 골랐어요` : "지울 사진을 골라요"}</span>
      <Button variant="soft" size="touch-sm" onClick={onCancel} disabled={busy}>
        취소
      </Button>
      <Button variant="discard" size="touch-sm" onClick={onRemove} disabled={!count || busy}>
        {busy ? "지우는 중…" : `${count}장 지우기`}
      </Button>
    </div>
  );
}
