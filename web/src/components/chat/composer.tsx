"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowUp, ImagePlus, Mic, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { uploadPhoto } from "@/lib/uploads";
import { cn } from "@/lib/utils";

/** One photo on its way in: shown from memory at once, sent by name once uploaded. */
type Attachment = { key: string; preview: string; name?: string; error?: string };

const MAX_PHOTOS = 12;

export function Composer({
  onSend,
  onVoice,
  onFocusChange,
  allowPhotos = false,
}: {
  /** Resolves false when the message did not go out, so what was typed comes back. */
  onSend: (text: string, images: string[]) => Promise<boolean> | void;
  /** Switches to voice. Only offered where the toggle below is hidden. */
  onVoice?: () => void;
  onFocusChange?: (focused: boolean) => void;
  allowPhotos?: boolean;
}) {
  const [value, setValue] = useState("");
  const [photos, setPhotos] = useState<Attachment[]>([]);
  const areaRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const el = areaRef.current;
    if (!el) return;
    el.style.height = "auto";
    // Five lines on a phone would leave the transcript a strip; the cap is
    // lower where the screen is shorter.
    const cap = window.innerWidth < 640 ? 112 : 168;
    el.style.height = `${Math.min(el.scrollHeight, cap)}px`;
  }, [value]);

  const uploading = photos.some((p) => !p.name && !p.error);
  const ready = photos.filter((p) => p.name);
  const canSend = !uploading && (value.trim() !== "" || ready.length > 0);

  function attach(files: FileList | null) {
    if (!files) return;
    const room = MAX_PHOTOS - photos.length;
    const picked = Array.from(files).slice(0, Math.max(0, room));
    const added = picked.map((file) => ({
      key: `${Date.now()}-${Math.random()}`,
      preview: URL.createObjectURL(file),
      file,
    }));
    setPhotos((cur) => [...cur, ...added.map(({ key, preview }) => ({ key, preview }))]);
    for (const a of added) {
      uploadPhoto(a.file)
        .then((name) => setPhotos((cur) => cur.map((p) => (p.key === a.key ? { ...p, name } : p))))
        .catch((e: Error) =>
          setPhotos((cur) => cur.map((p) => (p.key === a.key ? { ...p, error: e.message } : p))),
        );
    }
  }

  function remove(key: string) {
    setPhotos((cur) => {
      const gone = cur.find((p) => p.key === key);
      if (gone) URL.revokeObjectURL(gone.preview);
      return cur.filter((p) => p.key !== key);
    });
  }

  async function send() {
    if (!canSend) return;
    const text = value.trim();
    const sent = photos;
    setValue("");
    setPhotos([]);
    areaRef.current?.focus();
    const ok = await onSend(text, sent.filter((p) => p.name).map((p) => p.name!));
    if (ok === false) {
      setValue(text);
      setPhotos(sent);
      return;
    }
    for (const p of sent) URL.revokeObjectURL(p.preview);
  }

  return (
    <div className="rounded-2xl border border-edge bg-glass px-2 py-2 backdrop-blur-xl transition-colors focus-within:border-white/18">
      {photos.length > 0 ? (
        <ul className="scrollbar-none -mx-0.5 mb-1.5 flex gap-1.5 overflow-x-auto px-0.5 pt-0.5" aria-label="첨부한 사진">
          {photos.map((p) => (
            <li key={p.key} className="relative shrink-0">
              {/* eslint-disable-next-line @next/next/no-img-element -- a local object URL, nothing to optimise */}
              <img
                src={p.preview}
                alt=""
                className={cn(
                  "size-16 rounded-lg object-cover transition-opacity",
                  !p.name && !p.error && "opacity-45",
                  p.error && "opacity-30 grayscale",
                )}
              />
              {!p.name && !p.error ? (
                <span aria-hidden className="absolute inset-0 flex items-center justify-center">
                  <span className="size-4 animate-spin rounded-full border-2 border-foreground/25 border-t-foreground/80 motion-reduce:animate-none" />
                </span>
              ) : null}
              {p.error ? (
                <span className="absolute inset-x-1 bottom-1 truncate rounded bg-background/80 px-1 text-[10px] text-reject" title={p.error}>
                  실패
                </span>
              ) : null}
              <button
                type="button"
                onClick={() => remove(p.key)}
                aria-label="사진 빼기"
                className="absolute -top-0.5 -right-0.5 flex size-6 items-center justify-center rounded-full border border-edge bg-background/90 text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <X aria-hidden className="size-3.5" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      <div className="flex items-end gap-1">
        {allowPhotos ? (
          <>
            <input
              ref={fileRef}
              type="file"
              accept="image/*"
              multiple
              hidden
              onChange={(e) => {
                attach(e.target.files);
                e.target.value = "";
              }}
            />
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => fileRef.current?.click()}
              disabled={photos.length >= MAX_PHOTOS}
              aria-label="사진 첨부"
              className="tap mb-0.5 shrink-0 rounded-lg text-faint hover:text-foreground"
            >
              <ImagePlus aria-hidden className="size-[18px]" />
            </Button>
          </>
        ) : null}

        <textarea
          ref={areaRef}
          rows={1}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onFocus={() => onFocusChange?.(true)}
          onBlur={() => onFocusChange?.(false)}
          onKeyDown={(e) => {
            // Enter on a phone keyboard is a newline, not a send: there is no
            // shift to hold, and a stray send is worse than a stray line.
            if (
              e.key === "Enter" &&
              !e.shiftKey &&
              !e.nativeEvent.isComposing &&
              window.innerWidth >= 640
            ) {
              e.preventDefault();
              void send();
            }
          }}
          placeholder={photos.length ? "사진에 대해 한마디 (예: 중고나라에 올려줘)" : "Jarvis에게 메시지"}
          aria-label="Jarvis에게 보낼 메시지"
          className={cn(
            "scrollbar-hairline max-h-28 flex-1 resize-none bg-transparent py-2.5 text-[14.5px] leading-relaxed text-foreground outline-none placeholder:text-faint sm:max-h-[168px] sm:py-2",
            allowPhotos ? "px-1" : "px-2.5",
          )}
        />

        {/* The mode switch lives here on a phone. Stacking a separate toggle
            under the composer would put three bars between the transcript and
            the bottom of the screen; a mic beside the send button is the
            grammar every messaging app already taught. */}
        {onVoice ? (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onVoice}
            aria-label="음성으로 말하기"
            className="tap mb-0.5 shrink-0 rounded-lg text-faint hover:text-foreground sm:hidden"
          >
            <Mic aria-hidden className="size-[18px]" />
          </Button>
        ) : null}

        {/* Deliberately quiet: the brightest control in this product is 승인. */}
        <Button
          variant="outline"
          size="icon-sm"
          onClick={() => void send()}
          disabled={!canSend}
          aria-label={uploading ? "사진을 올리는 중" : "메시지 보내기"}
          className="tap mb-0.5 shrink-0 rounded-lg border-edge bg-glass-raised text-dim hover:text-foreground disabled:opacity-40 sm:size-8 sm:min-h-0 sm:min-w-0 dark:bg-glass-raised dark:hover:bg-white/12"
        >
          <ArrowUp aria-hidden className="size-[18px] sm:size-4" />
        </Button>
      </div>
    </div>
  );
}
