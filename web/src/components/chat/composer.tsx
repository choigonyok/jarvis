"use client";

import { useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { ArrowUp, ImagePlus, Mic, Reply, X } from "lucide-react";
import { uploadPhoto } from "@/lib/uploads";
import { cn } from "@/lib/utils";

/** One photo on its way in: shown from memory at once, sent by name once uploaded. */
type Attachment = { key: string; preview: string; name?: string; error?: string };

const MAX_PHOTOS = 12;

/** The earlier message the next one will quote, as the composer shows it. */
export type Quoting = { label: string; text: string };

/** A round control the height of the bar's inner track, like a tab bar slot. */
const round =
  "tap flex size-12 shrink-0 items-center justify-center rounded-full outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50";

/**
 * What was typed and not sent, kept on this device. The chat page unmounts
 * when another tab is opened, and the field's state went with it - a message
 * half written was gone on coming back. Storage that throws (private mode,
 * blocked) just forgets.
 */
const draft = {
  get(key: string): string {
    try {
      return localStorage.getItem(key) ?? "";
    } catch {
      return "";
    }
  },
  set(key: string, text: string) {
    try {
      if (text.trim()) localStorage.setItem(key, text);
      else localStorage.removeItem(key);
    } catch {}
  },
};

export function Composer({
  onSend,
  onVoice,
  onFocusChange,
  allowPhotos = false,
  quoting,
  onCancelQuote,
  inputRef,
  queued,
  busy = false,
  draftKey,
}: {
  /** Resolves false when the message did not go out, so what was typed comes back. */
  onSend: (text: string, images: string[]) => Promise<boolean> | void;
  /** Switches to voice. Only offered where the toggle below is hidden. */
  onVoice?: () => void;
  onFocusChange?: (focused: boolean) => void;
  allowPhotos?: boolean;
  /** Set while a swiped message waits to be quoted by the next one. */
  quoting?: Quoting | null;
  onCancelQuote?: () => void;
  /** The text field, for whoever needs to focus it in the same gesture. */
  inputRef?: RefObject<HTMLTextAreaElement | null>;
  /** What was sent while Jarvis works, shown at the top of this glass. */
  queued?: ReactNode;
  /** Jarvis is working: what is sent now waits its turn, and the field says so. */
  busy?: boolean;
  /** Where what is typed but not sent is kept, so leaving the tab does not lose it. */
  draftKey?: string;
}) {
  const [value, setValue] = useState("");
  // Text only: a photo's preview is an object URL that does not outlive the page.
  const type = (text: string) => {
    setValue(text);
    if (draftKey) draft.set(draftKey, text);
  };

  // Brought back after mount, not in useState's initializer: the server renders
  // an empty field, and the first client render has to match it.
  useEffect(() => {
    if (!draftKey) return;
    const kept = draft.get(draftKey);
    // Once, from storage the server cannot see; the rule cannot tell that apart.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (kept) setValue((cur) => cur || kept);
  }, [draftKey]);
  const [photos, setPhotos] = useState<Attachment[]>([]);
  const ownRef = useRef<HTMLTextAreaElement>(null);
  const areaRef = inputRef ?? ownRef;
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const el = areaRef.current;
    if (!el) return;
    el.style.height = "auto";
    // Five lines on a phone would leave the transcript a strip; the cap is
    // lower where the screen is shorter.
    const cap = window.innerWidth < 640 ? 112 : 168;
    el.style.height = `${Math.min(el.scrollHeight, cap)}px`;
  }, [value, areaRef]);

  const uploading = photos.some((p) => !p.name && !p.error);
  const ready = photos.filter((p) => p.name);
  const canSend = !uploading && (value.trim() !== "" || ready.length > 0);
  // Something is on its way out: on a phone the send button takes the mic's
  // place, as in every messenger, and the words get the width the second
  // button held.
  const composing = value.trim() !== "" || photos.length > 0;

  // Escape lets go of a quote, the way it closes anything else that is open.
  useEffect(() => {
    if (!quoting) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onCancelQuote?.();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [quoting, onCancelQuote]);

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
    type("");
    setPhotos([]);
    areaRef.current?.focus();
    const ok = await onSend(text, sent.filter((p) => p.name).map((p) => p.name!));
    if (ok === false) {
      type(text);
      setPhotos(sent);
      return;
    }
    for (const p of sent) URL.revokeObjectURL(p.preview);
  }

  // The tab bar's own shape and glass, so the two read as one family: a pill
  // the bar's height (3.75rem) on a single line, round controls the size of a
  // tab slot. It grows from there, keeping the radius, as a message gets long.
  // data-lead tells what sits left of the text - the photo button or only
  // padding - so a queued line above can start where the typing starts.
  return (
    <div className="composer group/composer liquid-glass rounded-[1.875rem] p-1.5" data-lead={allowPhotos ? "icon" : "text"}>
      {queued}
      {quoting ? (
        <div className="flex items-start gap-2 pt-1.5 pr-1 pb-1 pl-3.5">
          <Reply aria-hidden className="mt-[3px] size-3.5 shrink-0 text-faint" />
          <div className="min-w-0 flex-1">
            <p className="text-[12px] leading-snug text-dim">{quoting.label}</p>
            <p className="truncate text-[12.5px] leading-snug text-faint">{quoting.text}</p>
          </div>
          <button
            type="button"
            onClick={onCancelQuote}
            aria-label="참조 빼기"
            className="flex size-8 shrink-0 items-center justify-center rounded-full text-faint outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <X aria-hidden className="size-4" />
          </button>
        </div>
      ) : null}

      {photos.length > 0 ? (
        <ul className="scrollbar-none mb-1 flex gap-1.5 overflow-x-auto px-2.5 pt-1.5" aria-label="첨부한 사진">
          {photos.map((p) => (
            <li key={p.key} className="relative shrink-0">
              {/* eslint-disable-next-line @next/next/no-img-element -- a local object URL, nothing to optimise */}
              <img
                src={p.preview}
                alt=""
                className={cn(
                  "size-16 rounded-2xl object-cover transition-opacity",
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
                className="absolute -top-0.5 -right-0.5 flex size-6 items-center justify-center rounded-full bg-background/90 text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <X aria-hidden className="size-3.5" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      <div className="flex items-end">
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
            <button
              type="button"
              onClick={() => fileRef.current?.click()}
              disabled={photos.length >= MAX_PHOTOS}
              aria-label="사진 첨부"
              className={cn(round, "text-faint hover:text-foreground disabled:opacity-40")}
            >
              <ImagePlus aria-hidden className="size-5" strokeWidth={1.8} />
            </button>
          </>
        ) : null}

        <textarea
          ref={areaRef}
          rows={1}
          value={value}
          onChange={(e) => type(e.target.value)}
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
          placeholder={
            quoting
              ? "이걸로 무엇을 할까요?"
              : photos.length
                ? "사진에 대해 한마디 (예: 중고나라에 올려줘)"
                : busy
                  ? "끝나면 이어서 보낼 말"
                  : "Jarvis에게 메시지"
          }
          aria-label="Jarvis에게 보낼 메시지"
          className={cn(
            "scrollbar-hairline max-h-28 min-h-12 flex-1 resize-none bg-transparent py-3 text-[14.5px] leading-relaxed text-foreground outline-none placeholder:text-faint sm:max-h-[168px]",
            allowPhotos ? "px-1" : "pl-3.5 pr-1",
          )}
        />

        {/* The mode switch lives here on a phone. Stacking a separate toggle
            under the composer would put three bars between the transcript and
            the bottom of the screen; a mic in the send button's place while
            nothing is typed is the grammar every messaging app already taught. */}
        {onVoice && !composing ? (
          <button
            type="button"
            onClick={onVoice}
            aria-label="음성으로 말하기"
            className={cn(round, "text-faint hover:text-foreground sm:hidden")}
          >
            <Mic aria-hidden className="size-5" strokeWidth={1.8} />
          </button>
        ) : null}

        {/* Deliberately quiet: the brightest control in this product is 승인. */}
        <button
          type="button"
          onClick={() => void send()}
          disabled={!canSend}
          aria-label={uploading ? "사진을 올리는 중" : "메시지 보내기"}
          className={cn(round, "group text-dim disabled:opacity-40", onVoice && !composing && "max-sm:hidden")}
        >
          <span className="liquid-glass-flat flex size-9 items-center justify-center rounded-full transition-colors group-enabled:group-hover:text-foreground">
            <ArrowUp aria-hidden className="size-[18px]" />
          </span>
        </button>
      </div>
    </div>
  );
}
