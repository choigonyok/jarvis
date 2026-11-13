"use client";

import { useRef, useState, type ReactNode } from "react";
import { ImagePlus, X } from "lucide-react";
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
}: {
  onPick: (files: File[]) => void;
  busy?: { done: number; of: number } | null;
  label?: string;
  multiple?: boolean;
  className?: string;
  variant?: "soft" | "raised" | "quiet";
}) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <>
      <input
        ref={input}
        type="file"
        accept="image/jpeg,image/png,image/webp,image/heic,image/heif,image/*"
        multiple={multiple}
        hidden
        onChange={(e) => {
          const files = Array.from(e.target.files ?? []);
          e.target.value = "";
          if (files.length) onPick(files);
        }}
      />
      <Button type="button" variant={variant} size="touch-sm" disabled={!!busy} onClick={() => input.current?.click()} className={cn("gap-1.5", className)}>
        <ImagePlus aria-hidden className="size-4" />
        {busy ? `올리는 중… ${busy.done + 1}/${busy.of}` : label}
      </Button>
    </>
  );
}

/** A row of photos: tap one to look at it; with onRemove, each has a way off. */
export function PhotoTiles({
  assets,
  onOpen,
  onRemove,
  size = "md",
  className,
  trailing,
}: {
  assets: { key: string; asset: string; mark?: ReactNode }[];
  onOpen?: (index: number) => void;
  onRemove?: (index: number) => void;
  size?: "sm" | "md" | "lg";
  className?: string;
  /** What comes after the photos in the row (a way to add one). */
  trailing?: ReactNode;
}) {
  const box = size === "sm" ? "size-16" : size === "lg" ? "size-28 sm:size-32" : "size-20 sm:size-24";
  return (
    <ul className={cn("scrollbar-none -mx-1 flex gap-2 overflow-x-auto px-1 py-1", className)}>
      {assets.map((p, i) => (
        <li key={p.key} className="relative shrink-0">
          <button
            type="button"
            onClick={() => onOpen?.(i)}
            className="block overflow-hidden rounded-xl bg-glass-raised focus-ring"
            aria-label={`사진 ${i + 1} 크게 보기`}
          >
            {/* eslint-disable-next-line @next/next/no-img-element -- through the console's own proxy */}
            <img src={photoSrc(p.asset)} alt="" loading="lazy" className={cn("object-cover", box)} />
          </button>
          {p.mark ? <span className="pointer-events-none absolute bottom-1 left-1">{p.mark}</span> : null}
          {onRemove ? (
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
